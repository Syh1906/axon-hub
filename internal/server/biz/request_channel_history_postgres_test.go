package biz

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
)

func TestRequestChannelSessionPostgresHistoryBoundary(t *testing.T) {
	f := newStatisticsPostgresFixture(t)
	otherChannel := f.client.Channel.Create().SetName("alternate").SetType("openai").SetBaseURL("https://example.test").
		SetSupportedModels([]string{"model"}).SetDefaultTestModel("model").SetCredentials(objects.ChannelCredentials{}).SaveX(f.ctx)
	for _, gap := range []int{127, 128, 129} {
		t.Run(fmt.Sprintf("gap_%d", gap), func(t *testing.T) {
			thread := f.client.Thread.Create().SetProjectID(f.project.ID).SetThreadID(t.Name()).SaveX(f.ctx)
			trace := f.client.Trace.Create().SetProjectID(f.project.ID).SetThreadID(thread.ID).SetTraceID(t.Name()).SaveX(f.ctx)
			addRequest := func(channelID int) *ent.Request {
				return f.client.Request.Create().SetProjectID(f.project.ID).SetTraceID(trace.ID).SetChannelID(channelID).
					SetStatus("completed").SetModelID("model").SetRequestBody([]byte("{}")).SaveX(f.ctx)
			}
			adopt := func(r *ent.Request) int {
				attempt := f.client.RequestExecution.Create().SetRequestID(r.ID).SetProjectID(f.project.ID).SetChannelID(r.ChannelID).
					SetStatus("completed").SetModelID("model").SetRequestBody([]byte("{}")).SaveX(f.ctx)
				r.Update().SetResultExecutionID(attempt.ID).ExecX(f.ctx)
				return attempt.ID
			}
			advanceTo := func(id int) {
				t.Helper()
				require.NoError(t, f.client.Driver().Exec(f.ctx, "SELECT setval(pg_get_serial_sequence('requests','id'), $1, false)", []any{id}, nil))
			}

			first := adopt(addRequest(f.channel.ID))
			secondRequest := addRequest(otherChannel.ID)
			second := adopt(secondRequest)
			advanceTo(secondRequest.ID + gap - 1)
			addRequest(f.channel.ID) // 这条已结束的请求没有执行记录，不能算作渠道切换。
			resumedRequest := addRequest(otherChannel.ID)
			require.Equal(t, gap, resumedRequest.ID-secondRequest.ID)
			resumed := adopt(resumedRequest)

			advanceTo(resumedRequest.ID + 130)
			returned := adopt(addRequest(f.channel.ID))
			ids := []int{first, second, resumed, returned}
			require.Equal(t, map[int]string{first: "new", second: "new", resumed: "reused", returned: "returned"},
				RequestChannelSessions(f.ctx, f.client, ids))
		})
	}
}
