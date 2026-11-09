package biz

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/stretchr/testify/require"
)

func TestChannelUpstreamStatesBatchReads(t *testing.T) {
	db := enttest.NewEntClient(t, "sqlite3", "file:channel-batch-read?mode=memory&_fk=0")
	defer db.Close()
	ctx := authz.WithTestBypass(context.Background())
	var ids []int
	for i := 0; i < 100; i++ {
		row := db.Channel.Create().SetName(fmt.Sprint("batch-", i)).SetType("openai").SetSupportedModels([]string{"model"}).SetDefaultTestModel("model").SetCredentials(objects.ChannelCredentials{}).SaveX(ctx)
		ids = append(ids, row.ID)
	}
	var queries atomic.Int64
	client := ent.NewClient(ent.Driver(db.Driver()), ent.Debug(), ent.Log(func(args ...any) {
		if strings.Contains(fmt.Sprint(args...), "query=") {
			queries.Add(1)
		}
	}))
	svc := &ChannelService{AbstractService: &AbstractService{db: client}}
	for _, count := range []int{1, 20, 100} {
		queries.Store(0)
		states, err := svc.ChannelUpstreamStates(ctx, ids[:count])
		require.NoError(t, err)
		require.Len(t, states, count)
		require.EqualValues(t, 2, queries.Load(), "two batch reads, independent of page size")
		for i, state := range states {
			require.Equal(t, ids[i], state.ChannelID.ID)
		}
	}
	states, err := svc.ChannelUpstreamStates(ctx, []int{ids[2], ids[0], ids[2]})
	require.NoError(t, err)
	require.Len(t, states, 2)
	require.Equal(t, ids[2], states[0].ChannelID.ID)
	_, err = svc.ChannelUpstreamStates(ctx, []int{ids[0], 9999})
	require.True(t, ent.IsNotFound(err))
	_, err = svc.ChannelUpstreamStates(context.Background(), ids[:1])
	require.Error(t, err)
}
