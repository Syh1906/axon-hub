package httpclient

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"sync/atomic"
	"time"
)

// StreamStartTimings describes response bytes and decoder progress before the first protocol event.
type StreamStartTimings struct {
	Status              int        `json:"status"`
	MediaType           string     `json:"media_type,omitempty"`
	LastByteBeforeEvent *time.Time `json:"last_byte_before_event,omitempty"`
	DecodedEvents       int64      `json:"decoded_events"`
	DecoderEnd          string     `json:"decoder_end,omitempty"`
}

type streamStartTrace struct {
	status int
	media  string
	last   atomic.Int64
	events atomic.Int64
	end    atomic.Pointer[string]
}

func (t *TransportTrace) startStream(response *http.Response) *streamStartTrace {
	if t == nil {
		return nil
	}
	media, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if len(media) > 128 {
		media = ""
	}
	s := &streamStartTrace{status: response.StatusCode, media: media}
	t.mu.Lock()
	t.streamStart = s
	t.mu.Unlock()
	return s
}

func (s *streamStartTrace) snapshot() *StreamStartTimings {
	if s == nil {
		return nil
	}
	t := &StreamStartTimings{Status: s.status, MediaType: s.media, DecodedEvents: s.events.Load()}
	if stamp := s.last.Load(); stamp != 0 {
		at := time.Unix(0, stamp).UTC()
		t.LastByteBeforeEvent = &at
	}
	if end := s.end.Load(); end != nil {
		t.DecoderEnd = *end
	}
	return t
}

type streamStartBody struct {
	io.ReadCloser
	trace *streamStartTrace
}

func (r *streamStartBody) Read(buf []byte) (int, error) {
	n, err := r.ReadCloser.Read(buf)
	if n > 0 && r.trace.events.Load() == 0 {
		r.trace.last.Store(time.Now().UnixNano())
	}
	return n, err
}

type streamStartDecoder struct {
	StreamDecoder
	trace *streamStartTrace
}

func (d *streamStartDecoder) Next() bool {
	if d.StreamDecoder.Next() {
		d.trace.events.Add(1)
		return true
	}
	end := "eof"
	switch err := d.StreamDecoder.Err(); {
	case errors.Is(err, context.Canceled):
		end = "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		end = "deadline"
	case errors.Is(err, ErrStreamEventTooLarge):
		end = "event_size_limit"
	case err != nil:
		end = "read_or_decode_error"
	}
	d.trace.end.Store(&end)
	return false
}
