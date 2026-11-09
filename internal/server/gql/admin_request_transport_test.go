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
