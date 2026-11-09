package gql

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/tracing"
)

type requestDiagnosticsKey struct{}

type requestDiagnostics struct {
	io.ReadCloser
	started   time.Time
	readEnded time.Time
	bytesRead int64
	readErr   error
	phase     string
	failed    bool
	code      string
}

func (d *requestDiagnostics) Read(p []byte) (int, error) {
	n, err := d.ReadCloser.Read(p)
	d.bytesRead += int64(n)
	d.readEnded = time.Now()
	if err == io.EOF {
		d.phase = "decode"
	} else if err != nil {
		d.readErr = err
	}
	return n, err
}

func requestBodyError(err error) (int, string, string) {
	var timeout net.Error
	var tooLarge *http.MaxBytesError
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &timeout) && timeout.Timeout():
		return http.StatusRequestTimeout, "REQUEST_BODY_TIMEOUT", "request body read timed out"
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, context.Canceled):
		return http.StatusBadRequest, "REQUEST_BODY_INCOMPLETE", "request body was interrupted"
	case errors.As(err, &tooLarge):
		return http.StatusRequestEntityTooLarge, "REQUEST_BODY_TOO_LARGE", "request body exceeds the size limit"
	default:
		return http.StatusBadRequest, "REQUEST_BODY_READ_FAILED", "could not read request body"
	}
}

type diagnosticResponseWriter struct {
	http.ResponseWriter
	diagnostics *requestDiagnostics
	wroteHeader bool
}

func (w *diagnosticResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *diagnosticResponseWriter) WriteHeader(status int) {
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *diagnosticResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		status := http.StatusOK
		if w.diagnostics.readErr != nil {
			status, _, _ = requestBodyError(w.diagnostics.readErr)
		}
		w.WriteHeader(status)
	}
	return w.ResponseWriter.Write(p)
}

func withRequestDiagnostics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d := &requestDiagnostics{ReadCloser: r.Body, started: time.Now(), phase: "request_body"}
		if r.Body != nil {
			r.Body = d
		}
		r = r.WithContext(context.WithValue(r.Context(), requestDiagnosticsKey{}, d))
		next.ServeHTTP(&diagnosticResponseWriter{ResponseWriter: w, diagnostics: d}, r)
		if !d.failed && d.readErr == nil {
			return
		}
		readEnded := d.readEnded
		if readEnded.IsZero() {
			readEnded = d.started
		}
		fields := []log.Field{
			log.String("phase", d.phase),
			log.String("code", d.code),
			log.String("method", r.Method),
			log.String("protocol", r.Proto),
			log.Int64("content_length", r.ContentLength),
			log.Int64("body_bytes_read", d.bytesRead),
			log.Duration("body_read_duration", readEnded.Sub(d.started)),
			log.Duration("processing_duration", time.Since(readEnded)),
		}
		if d.readErr != nil {
			// 只记录错误类别；原始错误或解码错误可能包含正文、凭据或连接地址。
			_, code, cause := requestBodyError(d.readErr)
			fields = append(fields, log.String("read_error_code", code), log.String("read_error", cause), log.String("read_error_type", fmt.Sprintf("%T", d.readErr)))
		}
		if id, ok := tracing.GetRequestID(r.Context()); ok {
			fields = append(fields, log.String("admin_request_id", id))
		}
		log.Warn(r.Context(), "GraphQL request failed", fields...)
	})
}

func recordRequestErrors(ctx context.Context, resp *graphql.Response) {
	d, _ := ctx.Value(requestDiagnosticsKey{}).(*requestDiagnostics)
	if resp == nil || len(resp.Errors) == 0 {
		return
	}
	if !graphql.HasOperationContext(ctx) {
		code, message, phase := "REQUEST_BODY_INVALID", "request body could not be decoded", "decode"
		if d != nil && d.readErr != nil {
			_, code, message = requestBodyError(d.readErr)
			phase = "request_body"
		}
		resp.Errors = gqlerror.List{&gqlerror.Error{Message: message, Extensions: map[string]any{"code": code, "phase": phase}}}
	}
	if d != nil {
		d.failed = true
		if graphql.HasOperationContext(ctx) {
			d.phase = "validation"
			if graphql.GetOperationContext(ctx).Operation != nil {
				d.phase = "execution"
			}
		}
		if code, ok := resp.Errors[0].Extensions["code"].(string); ok {
			d.code = code
		}
	}
}
