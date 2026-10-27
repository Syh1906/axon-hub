package orchestrator

import (
	"time"

	"github.com/looplj/axonhub/internal/server/biz"
)

func channelResponseTimeouts(policy *biz.RetryPolicy, channel *biz.Channel) (time.Duration, time.Duration) {
	var firstEvent, nonStream time.Duration
	// Preserve the existing system policy's activation rule. Channel overrides
	// work even when retrying failed requests is disabled.
	if policy.Enabled {
		firstEvent = time.Duration(policy.StreamFirstEventTimeoutSeconds) * time.Second
		nonStream = time.Duration(policy.NonStreamResponseTimeoutSeconds) * time.Second
	}
	if channel != nil && channel.Settings != nil {
		if value := channel.Settings.StreamFirstEventTimeoutSeconds; value != nil {
			firstEvent = time.Duration(*value) * time.Second
		}
		if value := channel.Settings.NonStreamResponseTimeoutSeconds; value != nil {
			nonStream = time.Duration(*value) * time.Second
		}
	}
	return firstEvent, nonStream
}
