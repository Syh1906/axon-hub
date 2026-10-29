package httpclient

import (
	"io"
	"time"
)

// ObserveFirstByte wraps a response body without changing its contents or close
// behavior. A nil observer preserves the original reader.
func ObserveFirstByte(body io.ReadCloser, observer func(time.Time)) io.ReadCloser {
	if observer == nil {
		return body
	}
	return &firstByteReader{ReadCloser: body, onFirstByte: observer}
}

// firstByteReader observes body data rather than headers or a complete SSE event.
type firstByteReader struct {
	io.ReadCloser
	onFirstByte func(time.Time)
}

func (r *firstByteReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 && r.onFirstByte != nil {
		r.onFirstByte(time.Now())
		r.onFirstByte = nil
	}
	return n, err
}
