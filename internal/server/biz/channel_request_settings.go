package biz

import (
	"fmt"
	"math"
	"time"

	"github.com/looplj/axonhub/internal/objects"
)

func ValidateChannelResponseTimeouts(settings *objects.ChannelSettings) error {
	for _, value := range []*int{settings.StreamFirstEventTimeoutSeconds, settings.NonStreamResponseTimeoutSeconds} {
		if value != nil && (*value < 0 || int64(*value) > math.MaxInt64/int64(time.Second)) {
			return fmt.Errorf("channel response timeout must be a non-negative duration in seconds")
		}
	}
	return nil
}

func (c *Channel) SessionReuseEnabled() bool {
	return c.Settings == nil || !c.Settings.DisableSessionReuse
}
