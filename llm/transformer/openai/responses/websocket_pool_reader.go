package responses

import (
	"errors"
	"io"
	"log/slog"
	"net"
)

// NextReader services ping, pong and close frames even between turns. A data
// reader is lent only to the active lease; the pump waits until Next has
// consumed and classified it before touching the Gorilla read side again.
type pooledWebSocketFrame struct {
	reader   io.Reader
	consumed chan struct{}
}

func (e *WebSocketExecutor) readPooledSocket(pc *pooledWebSocketConn) {
	defer close(pc.readerExited)
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Error("panic reading upstream Responses WebSocket", "panic", recovered)
		}
		e.evict(pc)
	}()
	for {
		_, reader, err := pc.conn.NextReader()
		pc.mu.Lock()
		lease := pc.readerLease
		if err == nil && lease == nil {
			err = errors.New("unsolicited upstream WebSocket message between responses")
		}
		if err != nil {
			pc.readErr = err
			pc.closeLocked()
			pc.mu.Unlock()
			return
		}
		pc.mu.Unlock()
		frame := pooledWebSocketFrame{reader: reader, consumed: make(chan struct{})}
		select {
		case lease.frames <- frame:
		case <-pc.readerDone:
			return
		}
		select {
		case <-frame.consumed:
		case <-pc.readerDone:
			return
		}
	}
}

func (l *webSocketLease) nextReader() (io.Reader, chan struct{}, error) {
	if l.pooled == nil {
		_, r, err := l.conn.NextReader()
		return r, nil, err
	}
	select {
	case frame := <-l.frames:
		return frame.reader, frame.consumed, nil
	case <-l.pooled.readerDone:
		l.pooled.mu.Lock()
		defer l.pooled.mu.Unlock()
		if l.pooled.readErr != nil {
			return nil, nil, l.pooled.readErr
		}
		return nil, nil, net.ErrClosed
	}
}
