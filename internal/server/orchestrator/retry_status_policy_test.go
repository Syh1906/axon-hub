package orchestrator

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestExplicitRetryOverrides402And520Default(t *testing.T) {
	for _, status := range []int{402, 520} {
		for _, explicit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/explicit=%t", status, explicit), func(t *testing.T) {
				settings := &objects.ChannelSettings{}
				if explicit {
					settings.RetryableStatusCodes = []int{status}
				}
				ch := &biz.Channel{Channel: &ent.Channel{ID: 1, Settings: settings}}
				out := &PersistentOutboundTransformer{state: &PersistenceState{CurrentCandidate: &ChannelModelsCandidate{Channel: ch, Models: []biz.ChannelModelEntry{{ActualModel: "a"}, {ActualModel: "b"}}}}}
				require.Equal(t, explicit, out.CanRetry(&httpclient.Error{StatusCode: status}))
			})
		}
	}
}
