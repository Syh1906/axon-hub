package static

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	ginstatic "github.com/gin-contrib/static"

	"github.com/looplj/axonhub/internal/objects"
)

func useTestStaticFS(t *testing.T) {
	t.Helper()

	tempDir := t.TempDir()
	require.NoError(t, os.WriteFile(tempDir+"/index.html", []byte("<html><body>test</body></html>"), 0o644))
	require.NoError(t, os.Mkdir(tempDir+"/assets", 0o755))
	require.NoError(t, os.WriteFile(tempDir+"/assets/index-1234abcd.js", []byte("export default 42"), 0o644))
	require.NoError(t, os.WriteFile(tempDir+"/assets/runtime.js", []byte("export default 1"), 0o644))

	originalStaticFS := staticFS
	staticFS = ginstatic.LocalFile(tempDir, false)

	t.Cleanup(func() {
		staticFS = originalStaticFS
	})
}

func TestHandler_CachesOnlyExistingVersionedAssets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useTestStaticFS(t)
	router := gin.New()
	router.NoRoute(Handler())
	for _, path := range []string{"/assets/index-1234abcd.js", "/assets/missing-1234abcd.js", "/assets/runtime.js"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if path == "/assets/index-1234abcd.js" {
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Equal(t, "public, max-age=31536000, immutable", recorder.Header().Get("Cache-Control"))
		} else {
			require.Empty(t, recorder.Header().Get("Cache-Control"))
		}
	}
}

func TestHandler_ReturnsJSON404ForUnknownAPIPaths(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	useTestStaticFS(t)

	router := gin.New()
	router.NoRoute(Handler())

	for _, path := range []string{"/v1/not-found", "/anthropic/not-found", "/admin/not-found", "/self-service/not-found"} {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)

			router.ServeHTTP(recorder, req)

			require.Equal(t, http.StatusNotFound, recorder.Code)
			require.Contains(t, recorder.Header().Get("Content-Type"), "application/json")

			var resp objects.ErrorResponse
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
			require.Equal(t, http.StatusText(http.StatusNotFound), resp.Error.Type)
			require.Equal(t, "path not found: "+path, resp.Error.Message)
		})
	}
}

func TestHandler_ServesSPAIndexForFrontendRoutes(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	useTestStaticFS(t)

	router := gin.New()
	router.NoRoute(Handler())

	for _, path := range []string{"/settings/profile", "/self-usage"} {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			router.ServeHTTP(recorder, req)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Contains(t, recorder.Header().Get("Content-Type"), "text/html")
			require.Equal(t, "no-cache, no-store, must-revalidate", recorder.Header().Get("Cache-Control"))
		})
	}
}

func TestCodexPrefixAPINotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useTestStaticFS(t)
	router := gin.New()
	router.NoRoute(Handler())
	result := httptest.NewRecorder()
	router.ServeHTTP(result, httptest.NewRequest(http.MethodGet, "/codex/alpha/notes/v2/unknown", nil))
	require.Equal(t, http.StatusNotFound, result.Code)
	require.Contains(t, result.Header().Get("Content-Type"), "application/json")
}

func TestHandler_DoesNotFallbackMissingStaticAssetToSPAIndex(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	useTestStaticFS(t)

	router := gin.New()
	router.NoRoute(Handler())

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/definitely-missing.js", nil)

	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.NotContains(t, recorder.Header().Get("Content-Type"), "text/html")
}
