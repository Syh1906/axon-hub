// Package payloadcache stores bounded, expiring payloads outside the database.
package payloadcache

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrTooLarge = errors.New("payload exceeds cache entry limit")
	ErrMissing  = errors.New("payload expired or was evicted")
)

type Limits struct {
	TTL        time.Duration
	EntryBytes int64
	TotalBytes int64
	Entries    int
}

type entry struct {
	size      int64
	updatedAt time.Time
}

// Cache owns only digest-named gzip files in its private directory. The index
// contains metadata, never payloads. Use a separate directory per application
// instance; this cache deliberately does not coordinate independent writers.
type Cache struct {
	mu        sync.Mutex
	directory string
	limits    Limits
	entries   map[string]entry
	bytes     int64
	loaded    bool
	now       func() time.Time
}

func New(directory string, limits Limits) *Cache {
	return &Cache{directory: directory, limits: limits, entries: make(map[string]entry), now: time.Now}
}

func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validKey(key string) bool {
	if len(key) != 64 {
		return false
	}
	_, err := hex.DecodeString(key)
	return err == nil && strings.ToLower(key) == key
}

func (c *Cache) open(create bool) (*os.Root, error) {
	if create {
		if err := os.MkdirAll(c.directory, 0o700); err != nil {
			return nil, err
		}
	}
	root, err := os.OpenRoot(c.directory)
	if err != nil {
		return nil, err
	}
	if !c.loaded {
		dir, err := root.Open(".")
		if err != nil {
			root.Close()
			return nil, err
		}
		files, err := dir.ReadDir(-1)
		dir.Close()
		if err != nil {
			root.Close()
			return nil, err
		}
		entries := make(map[string]entry)
		var total int64
		for _, file := range files {
			key := strings.TrimSuffix(file.Name(), ".gz")
			if file.Name() != key+".gz" || !validKey(key) || !file.Type().IsRegular() {
				continue
			}
			info, err := file.Info()
			if err != nil {
				root.Close()
				return nil, err
			}
			entries[key] = entry{size: info.Size(), updatedAt: info.ModTime()}
			total += info.Size()
		}
		c.entries, c.bytes = entries, total
		c.loaded = true
	}
	return root, nil
}

func (c *Cache) remove(root *os.Root, key string) error {
	info, err := root.Lstat(key + ".gz")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("cache entry is not a regular file: %s", key)
		}
		if err := root.Remove(key + ".gz"); err != nil {
			return err
		}
	}
	c.bytes -= c.entries[key].size
	delete(c.entries, key)
	return nil
}

func (c *Cache) prune(root *os.Root, reserve int64, extraEntry int) error {
	keys := make([]string, 0, len(c.entries))
	for key := range c.entries {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return c.entries[keys[i]].updatedAt.Before(c.entries[keys[j]].updatedAt) })
	now := c.now()
	for _, key := range keys {
		if now.Before(c.entries[key].updatedAt.Add(c.limits.TTL)) &&
			c.bytes+reserve <= c.limits.TotalBytes && len(c.entries)+extraEntry <= c.limits.Entries {
			break
		}
		if err := c.remove(root, key); err != nil {
			return err
		}
	}
	return nil
}

// Put deduplicates identical keys. Capacity is checked before writing, including
// compressed size, so filling the cache never creates an unbounded disk spike.
func (c *Cache) Put(key string, data []byte) (time.Time, error) {
	if !validKey(key) {
		return time.Time{}, errors.New("invalid payload cache key")
	}
	if int64(len(data)) > c.limits.EntryBytes {
		return time.Time{}, ErrTooLarge
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	root, err := c.open(true)
	if err != nil {
		return time.Time{}, err
	}
	defer root.Close()
	if old, ok := c.entries[key]; ok && c.now().Before(old.updatedAt.Add(c.limits.TTL)) {
		// Keep the original expiry: repeated identical requests must not turn a
		// short-lived debug copy into indefinite storage.
		return old.updatedAt.Add(c.limits.TTL), nil
	}
	var compressed bytes.Buffer
	w := gzip.NewWriter(&compressed)
	if _, err := w.Write(data); err != nil {
		return time.Time{}, err
	}
	if err := w.Close(); err != nil {
		return time.Time{}, err
	}
	size := int64(compressed.Len())
	if size > c.limits.TotalBytes {
		return time.Time{}, ErrTooLarge
	}
	if err := c.prune(root, size, 1); err != nil {
		return time.Time{}, err
	}
	// O_EXCL refuses unexpected existing files or links. A failed write is
	// removed immediately and is never published in the metadata index.
	f, err := root.OpenFile(key+".gz", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return time.Time{}, err
	}
	_, writeErr := f.Write(compressed.Bytes())
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return time.Time{}, errors.Join(err, root.Remove(key+".gz"))
	}
	now := c.now()
	c.entries[key] = entry{size: size, updatedAt: now}
	c.bytes += size
	return now.Add(c.limits.TTL), nil
}

func (c *Cache) Get(key string) ([]byte, error) {
	if !validKey(key) {
		return nil, ErrMissing
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	root, err := c.open(false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrMissing
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	e, ok := c.entries[key]
	if !ok || !c.now().Before(e.updatedAt.Add(c.limits.TTL)) {
		return nil, ErrMissing
	}
	info, err := root.Lstat(key + ".gz")
	if err != nil || !info.Mode().IsRegular() {
		return nil, ErrMissing
	}
	f, err := root.Open(key + ".gz")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, c.limits.EntryBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > c.limits.EntryBytes {
		return nil, ErrTooLarge
	}
	return data, nil
}

func (c *Cache) Sweep() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	root, err := c.open(false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	return c.prune(root, 0, 0)
}

func Directory(base, kind string) string { return filepath.Join(base, "v1", kind) }
