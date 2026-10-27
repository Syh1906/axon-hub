package objects

import (
	"fmt"
	"io"
	"slices"
	"strconv"
)

// ChannelTagPriority is a channel's default priority within one routing tag.
type ChannelTagPriority struct {
	Tag      string `json:"tag"`
	Priority int    `json:"priority"`
}

// PriorityForTags only considers tags that still belong to the channel. An
// unconfigured tag inherits the model association instead of becoming zero.
func (s *ChannelSettings) PriorityForTags(channelTags, selectedTags []string) *int {
	if s == nil {
		return nil
	}
	var priority *int
	for _, entry := range s.TagPriorities {
		if slices.Contains(selectedTags, entry.Tag) && slices.Contains(channelTags, entry.Tag) &&
			(priority == nil || entry.Priority < *priority) {
			value := entry.Priority
			priority = &value
		}
	}
	return priority
}

type ChannelRoutingOrderField string

const (
	ChannelRoutingOrderPriority       ChannelRoutingOrderField = "PRIORITY"
	ChannelRoutingOrderCostMultiplier ChannelRoutingOrderField = "COST_MULTIPLIER"
)

func (f ChannelRoutingOrderField) IsValid() bool {
	return f == ChannelRoutingOrderPriority || f == ChannelRoutingOrderCostMultiplier
}

func (f ChannelRoutingOrderField) MarshalGQL(w io.Writer) {
	_, _ = io.WriteString(w, strconv.Quote(string(f)))
}

func (f *ChannelRoutingOrderField) UnmarshalGQL(value any) error {
	s, ok := value.(string)
	if !ok || !ChannelRoutingOrderField(s).IsValid() {
		return fmt.Errorf("invalid channel routing order field: %v", value)
	}
	*f = ChannelRoutingOrderField(s)
	return nil
}
