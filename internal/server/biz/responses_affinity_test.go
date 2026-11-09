package biz

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
)

func TestResponsesAffinityIdleExpiryAndSuccessfulMigration(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			cfg := xcache.Config{Mode: xcache.ModeMemory}
			ctx, client, service := newRequestStickyTestService(t, cfg, cfg)
			thread, trace, ch := createRequestStickyScope(t, ctx, client)
			ctx = contexts.WithProjectID(contexts.WithTrace(ctx, trace), thread.ProjectID)
			var server *miniredis.Miniredis
			if backend == "redis" {
				server = miniredis.RunT(t)
				rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
				t.Cleanup(func() { require.NoError(t, rdb.Close()) })
				service.responsesAffinity = &responsesAffinityStore{redis: rdb}
			}
			advance := func(d time.Duration) {
				if server != nil {
					server.FastForward(d)
					return
				}
				store := service.responsesAffinity
				for key, item := range store.memory.Items() {
					remaining := time.Until(time.Unix(0, item.Expiration)) - d
					if remaining <= 0 {
						store.memory.Delete(key)
					} else {
						store.memory.Set(key, item.Object, remaining)
					}
				}
			}
			execution := &ent.RequestExecution{ChannelID: ch.ID, Selection: &objects.RequestExecutionSelection{KeyRef: "origin-key"}}
			input := []byte(`{"model":"public-alias","input":[{"encrypted_content":"old-history"}]}`)
			output := []byte(`{"id":"resp_success","status":"completed","output":[{"encrypted_content":"new-history"}]}`)
			require.NoError(t, service.RecordResponsesAffinity(ctx, execution, input, output))
			advance(50 * time.Minute)
			route, found, err := service.GetResponsesAffinity(ctx, "public-alias", "", "new-history")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, ch.ID, route.ChannelID)
			advance(50 * time.Minute)
			_, found, err = service.GetResponsesAffinity(ctx, "public-alias", "", "new-history")
			require.NoError(t, err)
			require.True(t, found, "a hit slides the idle deadline")
			_, found, err = service.GetResponsesAffinity(ctx, "another-model", "", "new-history")
			require.NoError(t, err)
			require.False(t, found)
			_, found, err = service.GetResponsesAffinity(contexts.WithProjectID(ctx, thread.ProjectID+1), "public-alias", "", "new-history")
			require.NoError(t, err)
			require.False(t, found)
			advance(61 * time.Minute)
			_, found, err = service.GetResponsesAffinity(ctx, "public-alias", "", "new-history")
			require.NoError(t, err)
			require.False(t, found, "expired preferences are not restored from origin history")
			execution.ChannelID++
			input = []byte(`{"model":"public-alias","input":[{"encrypted_content":"new-history"}]}`)
			for _, status := range []string{"incomplete", "failed", "canceled"} {
				require.NoError(t, service.RecordResponsesAffinity(ctx, execution, input, []byte(`{"status":"`+status+`","output":[]}`)))
			}
			_, found, _ = service.GetResponsesAffinity(ctx, "public-alias", "", "new-history")
			require.False(t, found)
			require.NoError(t, service.RecordResponsesAffinity(ctx, execution, input, []byte(`{"id":"resp_other","status":"completed","output":[]}`)))
			route, found, err = service.GetResponsesAffinity(ctx, "public-alias", "", "new-history")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, execution.ChannelID, route.ChannelID, "a successful fallback becomes the new preference even without new encrypted output")
		})
	}
}
