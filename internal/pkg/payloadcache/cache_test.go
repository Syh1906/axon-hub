package payloadcache

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCacheExpiryCapacityAndRestart(t *testing.T) {
	dir := t.TempDir()
	limits := Limits{TTL: 5 * time.Minute, EntryBytes: 1024, TotalBytes: 1024, Entries: 2}
	c := New(dir, limits)
	now := time.Now()
	c.now = func() time.Time { return now }
	for _, value := range []string{"first", "second", "third"} {
		_, err := c.Put(Digest([]byte(value)), []byte(value))
		require.NoError(t, err)
		now = now.Add(time.Second)
	}
	_, err := c.Get(Digest([]byte("first")))
	require.ErrorIs(t, err, ErrMissing)
	require.Len(t, c.entries, 2)
	require.LessOrEqual(t, c.bytes, limits.TotalBytes)
	restarted := New(dir, limits)
	body, err := restarted.Get(Digest([]byte("third")))
	require.NoError(t, err)
	require.Equal(t, "third", string(body))
	now = now.Add(limits.TTL)
	require.NoError(t, c.Sweep())
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, files)
}

func TestCacheDedupeSizeAndDirectoryBoundary(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, Limits{TTL: time.Minute, EntryBytes: 128, TotalBytes: 80, Entries: 10})
	data := bytes.Repeat([]byte("a"), 100)
	key := Digest(data)
	expires, err := c.Put(key, data)
	require.NoError(t, err)
	secondExpiry, err := c.Put(key, data)
	require.NoError(t, err)
	require.Equal(t, expires, secondExpiry)
	require.Len(t, c.entries, 1)
	_, err = c.Put(Digest([]byte("too-large")), bytes.Repeat([]byte("a"), 129))
	require.ErrorIs(t, err, ErrTooLarge)
	_, err = c.Get("../outside")
	require.ErrorIs(t, err, ErrMissing)
	outside := filepath.Join(dir, "unowned.txt")
	require.NoError(t, os.WriteFile(outside, []byte("keep"), 0o600))
	c.now = func() time.Time { return expires.Add(time.Second) }
	require.NoError(t, c.Sweep())
	require.FileExists(t, outside)
	_, err = c.Put(Digest([]byte("replacement")), data)
	require.NoError(t, err)
	require.LessOrEqual(t, c.bytes, int64(80))
}

func TestCacheCompressedByteBudgetEvictsBeforeWrite(t *testing.T) {
	c := New(t.TempDir(), Limits{TTL: time.Hour, EntryBytes: 128, TotalBytes: 70, Entries: 100})
	for _, value := range []string{"first", "second", "third", "fourth", "fifth"} {
		_, err := c.Put(Digest([]byte(value)), []byte(value))
		require.NoError(t, err)
		files, err := os.ReadDir(c.directory)
		require.NoError(t, err)
		var total int64
		for _, file := range files {
			info, err := file.Info()
			require.NoError(t, err)
			total += info.Size()
		}
		require.LessOrEqual(t, total, int64(70))
	}
}
