package datatransfer

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"regexp"

	"github.com/looplj/axonhub/internal/pkg/xversion"
)

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func validateManifest(m *Manifest, appVersion string, maxSize int64, postgresMajor int) error {
	if m.Format != packageFormat || m.DumpBytes < 5 || m.DumpBytes > maxSize || m.DatabaseBytes <= 0 || m.DatabaseBytes > 1<<60 || !digestPattern.MatchString(m.SHA256) || m.ExportedAt.IsZero() {
		return ErrPackage
	}
	source, sourceErr := xversion.Parse(m.AppVersion)
	target, targetErr := xversion.Parse(appVersion)
	if sourceErr != nil || targetErr != nil || source.GreaterThan(target) || m.PostgresMajor < 12 || m.PostgresMajor > postgresMajor {
		return ErrVersion
	}
	return nil
}

// readPackage accepts two ordinary members with fixed names. It never extracts
// a path supplied by the archive and never buffers the dump in memory.
func readPackage(ctx context.Context, input io.Reader, output *os.File, version string, maxSize int64, pgMajor int, progress func(int64), expectedHash string) (*Manifest, error) {
	reader := tar.NewReader(input)
	header, err := reader.Next()
	if err != nil || header.Name != "manifest.json" || header.Typeflag != tar.TypeReg || header.Size < 1 || header.Size > 64*1024 {
		return nil, ErrPackage
	}
	data, err := io.ReadAll(io.LimitReader(reader, 64*1024+1))
	if err != nil {
		return nil, ErrPackage
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != expectedHash {
		return nil, ErrConflict
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, ErrPackage
	}
	if err := validateManifest(&manifest, version, maxSize, pgMajor); err != nil {
		return nil, err
	}
	header, err = reader.Next()
	if err != nil || header.Name != "database.dump" || header.Typeflag != tar.TypeReg || header.Size != manifest.DumpBytes {
		return nil, ErrPackage
	}
	hash := sha256.New()
	writer := &progressWriter{ctx: ctx, target: io.MultiWriter(output, hash), max: maxSize, progress: progress}
	if _, err := io.CopyBuffer(writer, reader, make([]byte, 128*1024)); err != nil {
		return nil, err
	}
	if writer.written != manifest.DumpBytes || hex.EncodeToString(hash.Sum(nil)) != manifest.SHA256 {
		return nil, ErrPackage
	}
	if _, err := reader.Next(); err != io.EOF {
		return nil, ErrPackage
	}
	// tar.Reader stops at its terminator; reject appended data as well.
	var trailing [1]byte
	if n, err := input.Read(trailing[:]); n != 0 || err != io.EOF {
		return nil, ErrPackage
	}
	if _, err := output.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	var magic [5]byte
	if _, err := io.ReadFull(output, magic[:]); err != nil || string(magic[:]) != "PGDMP" {
		return nil, ErrPackage
	}
	return &manifest, output.Sync()
}

func writePackage(out io.Writer, manifest *Manifest, dump *os.File) error {
	w := tar.NewWriter(out)
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if err := w.WriteHeader(packageHeader("manifest.json", int64(len(data)))); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if err := w.WriteHeader(packageHeader("database.dump", manifest.DumpBytes)); err != nil {
		return err
	}
	if _, err := io.CopyN(w, dump, manifest.DumpBytes); err != nil {
		return err
	}
	return w.Close()
}

type progressWriter struct {
	ctx      context.Context
	target   io.Writer
	max      int64
	written  int64
	progress func(int64)
}

func (w *progressWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if w.written+int64(len(p)) > w.max {
		return 0, ErrSpace
	}
	n, err := w.target.Write(p)
	w.written += int64(n)
	if w.progress != nil {
		w.progress(w.written)
	}
	return n, err
}

// GNU encodes large sizes in the fixed header, avoiding a hidden PAX member
// above 8 GiB that would invalidate the streamed Content-Length.
func packageHeader(name string, size int64) *tar.Header {
	return &tar.Header{Name: name, Mode: 0600, Size: size, Typeflag: tar.TypeReg, Format: tar.FormatGNU}
}
