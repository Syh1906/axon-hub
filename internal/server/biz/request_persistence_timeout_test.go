package biz

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestTerminalWriteSurvivesCanceledPersistenceContext(t *testing.T) {
	for _, mode := range []string{"canceled", "expired", "database_failure"} {
		for _, writer := range []string{"request_final", "execution_final", "request_failed", "execution_failed", "request_canceled", "execution_canceled"} {
			t.Run(mode+"/"+writer, func(t *testing.T) {
				client := enttest.NewEntClient(t, "sqlite3", "file:"+t.Name()+"?mode=memory&_fk=0")
				defer client.Close()
				ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
				system := NewSystemService(SystemServiceParams{Ent: client, CacheConfig: xcache.Config{Mode: xcache.ModeMemory}})
				storage := NewDataStorageService(DataStorageServiceParams{Client: client, SystemService: system, CacheConfig: system.CacheConfig})
				svc := NewRequestService(client, system.CacheConfig, system, nil, storage, NewLiveStreamRegistry(), NewRequestPayloadService(RequestPayloadConfig{Directory: t.TempDir()}))
				policy := defaultStoragePolicy
				policy.StoreChunks = true
				policy.StoreResponseBody = false
				require.NoError(t, system.SetStoragePolicy(ctx, &policy))
				req := client.Request.Create().SetModelID("model").SetRequestBody([]byte(`{}`)).
					SetStatus(request.StatusProcessing).SaveX(ctx)
				execution := client.RequestExecution.Create().SetRequestID(req.ID).SetModelID("model").SetRequestBody([]byte(`{}`)).
					SetStatus(requestexecution.StatusProcessing).SaveX(ctx)
				injected := errors.New("database update failed")
				updates := 0
				client.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(saveCtx context.Context, m ent.Mutation) (ent.Value, error) {
						if m.Type() == "Request" || m.Type() == "RequestExecution" {
							updates++
							require.NoError(t, saveCtx.Err(), "terminal update must have a fresh context")
							require.Same(t, client, ent.FromContext(saveCtx), "request context values must be preserved")
							deadline, ok := saveCtx.Deadline()
							require.True(t, ok)
							require.Greater(t, time.Until(deadline), 8*time.Second)
							if mode == "database_failure" {
								return nil, injected
							}
						}
						return next.Mutate(saveCtx, m)
					})
				})
				_, err := system.StoragePolicy(ctx)
				require.NoError(t, err)
				// Fork payloads use a bounded local debug cache. A canceled caller
				// must still allow one fresh, bounded terminal database write.
				uploadCtx, cancel := context.WithCancel(ctx)
				cancel()
				if mode == "expired" {
					uploadCtx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
					defer cancel()
				}
				chunks := []*httpclient.StreamEvent{{Type: "message", Data: []byte(`{"delta":"hello"}`)}}
				switch writer {
				case "request_final":
					err = svc.UpdateRequestFinalized(uploadCtx, req, request.StatusCompleted, "response", nil, nil, chunks...)
				case "execution_final":
					err = svc.UpdateRequestExecutionFinalized(uploadCtx, execution, requestexecution.StatusCompleted, "", "response", nil, nil, "", nil, chunks...)
				case "request_failed":
					err = svc.UpdateRequestStatusFromError(uploadCtx, req, errors.New("upstream failed"), nil, chunks...)
				case "request_canceled":
					err = svc.UpdateRequestStatusFromError(uploadCtx, req, context.Canceled, nil, chunks...)
				case "execution_failed":
					err = svc.UpdateRequestExecutionStatusWithMetrics(uploadCtx, execution, requestexecution.StatusFailed, "failed", nil, nil, "", nil, chunks...)
				case "execution_canceled":
					err = svc.UpdateRequestExecutionStatusWithMetrics(uploadCtx, execution, requestexecution.StatusCanceled, "canceled", nil, nil, "", nil, chunks...)
				}
				require.Error(t, uploadCtx.Err())
				require.Equal(t, 1, updates)
				expectedStatus := "completed"
				if strings.HasSuffix(writer, "failed") {
					expectedStatus = "failed"
				}
				if strings.HasSuffix(writer, "canceled") || mode == "canceled" && writer == "request_failed" {
					expectedStatus = "canceled"
				}
				if mode == "database_failure" {
					require.ErrorIs(t, err, injected)
					expectedStatus = "processing"
				} else {
					require.NoError(t, err)
				}
				if strings.HasPrefix(writer, "execution") {
					row := client.RequestExecution.GetX(ctx, execution.ID)
					require.Equal(t, expectedStatus, string(row.Status))
				} else {
					row := client.Request.GetX(ctx, req.ID)
					require.Equal(t, expectedStatus, string(row.Status))
				}
			})
		}
	}
}
