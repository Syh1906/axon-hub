package biz

import (
	"context"
	"errors"
	"net/http"
	"os"

	"cloud.google.com/go/storage"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/datastorage"
	"github.com/spf13/afero"
	"golang.org/x/oauth2/google"
	googleoption "google.golang.org/api/option"
)

// DataCleanup scopes remote clients to one bounded cleanup batch. It never
// changes the shared read/write filesystem's context or authentication route.
type DataCleanup struct {
	service *DataStorageService
	ctx     context.Context
	gcs     map[int]*storage.Client
	webdav  map[int]afero.Fs
}

func (s *DataStorageService) NewDataCleanup(ctx context.Context) *DataCleanup {
	return &DataCleanup{service: s, ctx: ctx, gcs: make(map[int]*storage.Client), webdav: make(map[int]afero.Fs)}
}

func (c *DataCleanup) Close() {
	for _, client := range c.gcs {
		_ = client.Close()
	}
}

func (c *DataCleanup) DeleteData(ctx context.Context, ds *ent.DataStorage, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch ds.Type {
	case datastorage.TypeGcs:
		client := c.gcs[ds.ID]
		if client == nil {
			cfg := ds.Settings.GCS
			creds, err := google.CredentialsFromJSON(ctx, []byte(cfg.Credential), storage.ScopeFullControl)
			if err != nil {
				return err
			}
			client, err = storage.NewClient(ctx, googleoption.WithCredentials(creds))
			if err != nil {
				return err
			}
			c.gcs[ds.ID] = client
		}
		err := client.Bucket(ds.Settings.GCS.BucketName).Object(normalizeObjectKey(key)).Delete(ctx)
		if errors.Is(err, storage.ErrObjectNotExist) {
			return nil
		}
		return err
	case datastorage.TypeWebdav:
		fs := c.webdav[ds.ID]
		if fs == nil {
			var err error
			fs, err = c.service.createWebDAVFsWithContext(c.ctx, ds, ds.Settings.WebDAV)
			if err != nil {
				return err
			}
			c.webdav[ds.ID] = fs
		}
		err := fs.Remove(key)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	default:
		return c.service.DeleteData(ctx, ds, key)
	}
}

func bindCleanupRequestContext(ctx context.Context) func(string, *http.Request) {
	return func(_ string, request *http.Request) { *request = *request.WithContext(ctx) }
}
