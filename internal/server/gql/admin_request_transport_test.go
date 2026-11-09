package gql

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/stretchr/testify/require"
)

type failedRequestBody struct {
	err error
}

func (b failedRequestBody) Read([]byte) (int, error) { return 0, b.err }
func (b failedRequestBody) Close() error             { return nil }

func TestGraphQLRequestReadFailureKeepsOriginalCategory(t *testing.T) {
	handler := NewGraphqlHandlers(Dependencies{}).Graphql
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"incomplete", io.ErrUnexpectedEOF, http.StatusBadRequest, "REQUEST_BODY_INCOMPLETE"},
		{"canceled", context.Canceled, http.StatusBadRequest, "REQUEST_BODY_INCOMPLETE"},
		{"timeout", context.DeadlineExceeded, http.StatusRequestTimeout, "REQUEST_BODY_TIMEOUT"},
		{"too large", &http.MaxBytesError{Limit: 8}, http.StatusRequestEntityTooLarge, "REQUEST_BODY_TOO_LARGE"},
		{"opaque error", errors.New("private-body-fixture"), http.StatusBadRequest, "REQUEST_BODY_READ_FAILED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/admin/graphql", failedRequestBody{tc.err})
			req.Header.Set("Content-Type", "application/json")
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			require.Equal(t, tc.status, res.Code)
			var body struct {
				Errors []struct {
					Message    string
					Extensions map[string]string
				}
			}
			require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
			require.Len(t, body.Errors, 1)
			require.Equal(t, tc.code, body.Errors[0].Extensions["code"])
			require.Equal(t, "request_body", body.Errors[0].Extensions["phase"])
			require.NotContains(t, res.Body.String(), "missing operation context")
			require.NotContains(t, res.Body.String(), "private-body-fixture")
		})
	}
}

func TestGraphQLMalformedJSONDoesNotEchoBodyOrPanic(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/admin/graphql", strings.NewReader(`{"query":"private-body-fixture"`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	NewGraphqlHandlers(Dependencies{}).Graphql.ServeHTTP(res, req)
	require.Equal(t, http.StatusBadRequest, res.Code)
	require.Contains(t, res.Body.String(), "REQUEST_BODY_INVALID")
	require.NotContains(t, res.Body.String(), "private-body-fixture")
	require.NotContains(t, res.Body.String(), "missing operation context")
}

func TestGraphQLRequestDiagnosticsLeaveSuccessfulQueriesUnchanged(t *testing.T) {
	for _, path := range []string{"/admin/graphql", "/admin/graphql?query=%7B__typename%7D"} {
		method := http.MethodPost
		if strings.Contains(path, "?") {
			method = http.MethodGet
		}
		req := httptest.NewRequest(method, path, strings.NewReader(`{"query":"{__typename}"}`))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		NewGraphqlHandlers(Dependencies{}).Graphql.ServeHTTP(res, req)
		require.Equal(t, http.StatusOK, res.Code)
		require.JSONEq(t, `{"data":{"__typename":"Query"}}`, res.Body.String())
	}
}

func TestGraphQLRequestDiagnosticsCountBytesWithoutKeepingBody(t *testing.T) {
	var observed *requestDiagnostics
	handler := withRequestDiagnostics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed = r.Context().Value(requestDiagnosticsKey{}).(*requestDiagnostics)
		n, err := io.Copy(io.Discard, r.Body)
		require.NoError(t, err)
		require.EqualValues(t, 12, n)
		w.WriteHeader(http.StatusNoContent)
	}))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/admin/graphql", strings.NewReader("body-fixture")))
	require.Equal(t, http.StatusNoContent, res.Code)
	require.EqualValues(t, 12, observed.bytesRead)
	require.Equal(t, "decode", observed.phase)
	require.False(t, observed.readEnded.Before(observed.started))
	require.Nil(t, observed.readErr)
}

func TestGraphQLVariableValidationKeepsValidationPhase(t *testing.T) {
	var diagnostics *requestDiagnostics
	graph := handler.New(NewExecutableSchema(Config{Resolvers: &Resolver{}}))
	graph.AddTransport(transport.POST{})
	graph.Use(&loggingTracer{})
	graph.AroundOperations(withOperationTimeout)
	graph.AroundResponses(func(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
		diagnostics = ctx.Value(requestDiagnosticsKey{}).(*requestDiagnostics)
		return next(ctx)
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/graphql", strings.NewReader(`{"query":"query ReadNode($id: ID!) { node(id: $id) { id } }"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	withRequestDiagnostics(graph).ServeHTTP(res, req)
	require.Contains(t, res.Body.String(), "must be defined")
	require.Equal(t, "validation", diagnostics.phase)
}

func TestGraphQLMultipartDeclaredLimitIsClassified(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/admin/graphql", strings.NewReader(""))
	req.ContentLength = maxBackupUploadSize + 1
	req.Header.Set("Content-Type", "multipart/form-data; boundary=fixture")
	res := httptest.NewRecorder()
	NewGraphqlHandlers(Dependencies{}).Graphql.ServeHTTP(res, req)
	require.Equal(t, http.StatusRequestEntityTooLarge, res.Code)
	require.Contains(t, res.Body.String(), "REQUEST_BODY_TOO_LARGE")
}

func TestGraphQLMultipartReadTimeoutIsClassified(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/admin/graphql", failedRequestBody{context.DeadlineExceeded})
	req.Header.Set("Content-Type", "multipart/form-data; boundary=fixture")
	res := httptest.NewRecorder()
	NewGraphqlHandlers(Dependencies{}).Graphql.ServeHTTP(res, req)
	require.Equal(t, http.StatusRequestTimeout, res.Code)
	require.Contains(t, res.Body.String(), "REQUEST_BODY_TIMEOUT")
}

type oversizedUploadReader struct{}

func (oversizedUploadReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestGraphQLMultipartStreamingLimitIsClassified(t *testing.T) {
	var received int64
	var observed *requestDiagnostics
	handler := withRequestDiagnostics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed = r.Context().Value(requestDiagnosticsKey{}).(*requestDiagnostics)
		var err error
		received, err = io.Copy(io.Discard, r.Body)
		require.ErrorAs(t, err, new(*http.MaxBytesError))
		w.Header().Set("Content-Length", "15")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, err = w.Write([]byte("parser failure"))
		require.NoError(t, err)
	}))
	req := httptest.NewRequest(http.MethodPost, "/admin/graphql", oversizedUploadReader{})
	req.ContentLength = -1
	req.Header.Set("Content-Type", "multipart/form-data; boundary=fixture")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	require.EqualValues(t, maxBackupUploadSize, received)
	require.Equal(t, http.StatusRequestEntityTooLarge, res.Code)
	require.Empty(t, res.Header().Get("Content-Length"))
	require.Equal(t, "application/json", res.Header().Get("Content-Type"))
	require.Contains(t, res.Body.String(), "REQUEST_BODY_TOO_LARGE")
	require.NotContains(t, res.Body.String(), "parser failure")
	require.True(t, observed.failed)
	require.Equal(t, "REQUEST_BODY_TOO_LARGE", observed.code)
}
