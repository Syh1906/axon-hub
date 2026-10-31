package datatransfer

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func samplePackage(t *testing.T) ([]byte, string) {
	t.Helper()
	dump := []byte("PGDMPtest-data")
	hash := sha256.Sum256(dump)
	m := &Manifest{Format: packageFormat, AppVersion: "v1.0.0", PostgresMajor: 17, DatabaseBytes: 1024, DumpBytes: int64(len(dump)), SHA256: hex.EncodeToString(hash[:]), ExportedAt: time.Now().UTC()}
	f, err := os.Create(filepath.Join(t.TempDir(), "test.dump"))
	require.NoError(t, err)
	defer f.Close()
	_, err = f.Write(dump)
	require.NoError(t, err)
	_, err = f.Seek(0, 0)
	require.NoError(t, err)
	var out bytes.Buffer
	require.NoError(t, writePackage(&out, m, f))
	data, err := json.Marshal(m)
	require.NoError(t, err)
	manifestHash := sha256.Sum256(data)
	return out.Bytes(), hex.EncodeToString(manifestHash[:])
}

func TestPackageRoundTripAndRejectsTampering(t *testing.T) {
	valid, digest := samplePackage(t)
	for _, test := range []struct {
		name   string
		mutate func([]byte) []byte
		hash   string
		want   bool
	}{
		{"valid", func(b []byte) []byte { return b }, digest, true},
		{"modified dump", func(b []byte) []byte { b[1029] ^= 1; return b }, digest, false},
		{"truncated", func(b []byte) []byte { return b[:1100] }, digest, false},
		{"extra bytes", func(b []byte) []byte { return append(b, 'x') }, digest, false},
		{"changed submission", func(b []byte) []byte { return b }, string(bytes.Repeat([]byte("a"), 64)), false},
		{"path traversal", func(b []byte) []byte {
			var out bytes.Buffer
			w := tar.NewWriter(&out)
			require.NoError(t, w.WriteHeader(&tar.Header{Name: "../manifest.json", Size: 1, Mode: 0600}))
			_, _ = w.Write([]byte("x"))
			require.NoError(t, w.Close())
			return out.Bytes()
		}, digest, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			out, err := os.Create(filepath.Join(t.TempDir(), "out.dump"))
			require.NoError(t, err)
			defer out.Close()
			_, err = readPackage(context.Background(), bytes.NewReader(test.mutate(bytes.Clone(valid))), out, "v1.0.0", 1024, 17, nil, test.hash)
			if test.want {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestPackageVersionGate(t *testing.T) {
	valid, digest := samplePackage(t)
	for _, v := range []string{"dev", "v0.9.0"} {
		f, err := os.Create(filepath.Join(t.TempDir(), "out.dump"))
		require.NoError(t, err)
		_, err = readPackage(context.Background(), bytes.NewReader(valid), f, v, 1024, 17, nil, digest)
		f.Close()
		require.ErrorIs(t, err, ErrVersion)
	}
}

func TestLargePackageHeaderHasNoExtraMember(t *testing.T) {
	var out bytes.Buffer
	w := tar.NewWriter(&out)
	require.NoError(t, w.WriteHeader(packageHeader("database.dump", 20*gib)))
	require.Equal(t, 512, out.Len())
	h, err := tar.NewReader(bytes.NewReader(out.Bytes())).Next()
	require.NoError(t, err)
	require.Equal(t, 20*gib, h.Size)
	require.Equal(t, "database.dump", h.Name)
}
