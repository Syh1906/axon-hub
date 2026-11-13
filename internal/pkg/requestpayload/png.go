package requestpayload

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"hash/crc32"
	"io"
)

const maxPNGExpandedBytes = 64 << 20

// Recompress the original filtered scanlines, not decoded pixels. All ancillary
// chunks (color profile, gamma, EXIF, etc.) remain byte-identical and in order.
// Reject invalid chunk framing, CRCs and zlib data. Animated, signed, oversized,
// or already compact PNGs are left untouched; this is not a full PNG validator.
func recompressPNG(ctx context.Context, original []byte) []byte {
	if len(original) < 8 || !bytes.Equal(original[:8], []byte("\x89PNG\r\n\x1a\n")) {
		return nil
	}
	var compressed bytes.Buffer
	first, last, ended := -1, -1, false
	for pos := 8; pos < len(original); {
		if pos+12 > len(original) {
			return nil
		}
		n := uint64(binary.BigEndian.Uint32(original[pos:]))
		if n > uint64(len(original)-pos-12) {
			return nil
		}
		end := pos + int(n) + 12
		kind := string(original[pos+4 : pos+8])
		if crc32.ChecksumIEEE(original[pos+4:end-4]) != binary.BigEndian.Uint32(original[end-4:end]) {
			return nil
		}
		if kind == "acTL" || kind == "fdAT" || kind == "dSIG" {
			return nil
		}
		if pos == 8 && (kind != "IHDR" || n != 13) {
			return nil
		}
		if kind == "IDAT" {
			if first < 0 {
				first = pos
			} else if pos != last {
				return nil
			}
			compressed.Write(original[pos+8 : end-4])
			last = end
		}
		if kind == "IEND" {
			if ended || n != 0 || end != len(original) {
				return nil
			}
			ended = n == 0 && end == len(original)
		}
		pos = end
	}
	if !ended || first < 0 {
		return nil
	}
	reader, err := zlib.NewReader(bytes.NewReader(compressed.Bytes()))
	if err != nil {
		return nil
	}
	defer reader.Close()
	var encoded bytes.Buffer
	writer, _ := zlib.NewWriterLevel(&encoded, zlib.BestCompression)
	n, err := io.Copy(writer, io.LimitReader(contextReader{ctx, reader}, maxPNGExpandedBytes+1))
	closeErr := writer.Close()
	if err != nil || closeErr != nil || n > maxPNGExpandedBytes || ctx.Err() != nil {
		return nil
	}
	if encoded.Len()+12 >= last-first {
		return nil
	}
	result := make([]byte, 0, len(original)-(last-first)+encoded.Len()+12)
	result = append(result, original[:first]...)
	result = binary.BigEndian.AppendUint32(result, uint32(encoded.Len()))
	result = append(result, "IDAT"...)
	result = append(result, encoded.Bytes()...)
	result = binary.BigEndian.AppendUint32(result, crc32.ChecksumIEEE(result[first+4:]))
	result = append(result, original[last:]...)
	return result
}

type contextReader struct {
	context.Context
	io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}
