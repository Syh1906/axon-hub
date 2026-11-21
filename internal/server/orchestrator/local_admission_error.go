package orchestrator

import (
	"errors"

	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// isLocalAdmissionError identifies local rejections before dispatch.
// They must not penalize upstream performance, credentials, circuit health, or
// cooldown. Their retry/failover and execution-persistence policies may differ:
// plugin overload stops failover and precedes creation of this attempt's record;
// channel queue/RPM rejection may fail over and has a record to finish.
func isLocalAdmissionError(err error) bool {
	if errors.Is(err, httpclient.ErrRequestNotSent) {
		if rejection, ok := errors.AsType[*llm.ResponseError](err); ok && rejection.Detail.Code == "no_compatible_endpoint" {
			return true
		}
	}
	return isChannelQueueError(err) || isLocalRPMExhaustedError(err) || isPluginOverloaded(err) || isChannelCooldownError(err) || biz.IsBudgetDenied(err) || CompactionRetryAfter(err) > 0
}
