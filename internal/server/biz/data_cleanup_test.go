package biz

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/datastorage"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/stretchr/testify/require"
)

func TestDataCleanupWebDAVHonorsBatchCancellation(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := &DataStorageService{}
	batch := service.NewDataCleanup(ctx)
	defer batch.Close()
	ds := &ent.DataStorage{ID: 1, Type: datastorage.TypeWebdav, Settings: &objects.DataStorageSettings{WebDAV: &objects.WebDAV{URL: server.URL}}}
	finished := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				finished <- fmt.Errorf("panic: %v", r)
			}
		}()
		finished <- batch.DeleteData(ctx, ds, "payload.json")
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request never reached test server")
	}
	cancel()
	select {
	case err := <-finished:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("external cleanup ignored cancellation")
	}
}
