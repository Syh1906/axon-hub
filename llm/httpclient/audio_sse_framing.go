package httpclient

import (
	"bufio"
	"errors"
	"io"
)

var errIncompleteAudioEvent = errors.New("audio event stream ended inside an event")

type audioSSEFrameReader struct {
	io.ReadCloser
	scanner *bufio.Scanner
	frame   []byte
}

func newAudioSSEFrameReader(body io.ReadCloser) *audioSSEFrameReader {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), defaultMaxSSEEventBytes)
	offset, lineStart := 0, 0
	scanner.Split(func(data []byte, eof bool) (int, []byte, error) {
		for offset < len(data) {
			ch := data[offset]
			if ch != '\r' && ch != '\n' {
				offset++
				continue
			}
			empty := offset == lineStart
			if ch == '\r' && offset+1 == len(data) && !eof && !empty {
				return 0, nil, nil
			}
			offset++
			if ch == '\r' && offset < len(data) && data[offset] == '\n' {
				offset++
			}
			if empty {
				end := offset
				offset, lineStart = 0, 0
				return end, data[:end], nil
			}
			lineStart = offset
		}
		if eof && len(data) != 0 {
			return 0, nil, errIncompleteAudioEvent
		}
		return 0, nil, nil
	})
	return &audioSSEFrameReader{ReadCloser: body, scanner: scanner}
}

func (r *audioSSEFrameReader) Read(buf []byte) (int, error) {
	if len(r.frame) == 0 {
		if !r.scanner.Scan() {
			if err := r.scanner.Err(); err != nil {
				return 0, err
			}
			return 0, io.EOF
		}
		r.frame = r.scanner.Bytes()
	}
	n := copy(buf, r.frame)
	r.frame = r.frame[n:]
	return n, nil
}
