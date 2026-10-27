package objects

// Routing decision reasons for session owner migration (fork: health-gated routing).
const (
	RoutingMigrationReasonOwnerOpen            = "owner_open"
	RoutingMigrationReasonConsecutiveFailovers = "consecutive_failovers"
	RoutingMigrationReasonSessionReuseDisabled = "session_reuse_disabled"
)

// RequestRoutingDecision records why a health-gated request was routed the way it was.
// It is written once when the request finishes; nil for other strategies and older requests.
type RequestRoutingDecision struct {
	// Owner is the session owner at request start; nil when the session had no owner
	// or session stickiness is disabled.
	Owner *RoutingDecisionCombo `json:"owner,omitempty"`
	// Skipped lists channel×model combinations excluded by the health gate.
	Skipped []RoutingDecisionCombo `json:"skipped"`
	// TemporaryFailover is true when a fallback channel completed the request and the owner was kept.
	TemporaryFailover bool `json:"temporary_failover"`
	// LastResort is true when the request was attempted as the last resort of an all-open set.
	LastResort bool `json:"last_resort"`
	// ConsecutiveFailovers is the session's consecutive failover count after this request.
	ConsecutiveFailovers int `json:"consecutive_failovers"`
	// Migration is set when this request moved the session to another channel.
	Migration *RoutingDecisionMigration `json:"migration,omitempty"`
}

// RoutingDecisionCombo identifies a channel and upstream actual model; names are snapshots.
type RoutingDecisionCombo struct {
	ChannelID   int    `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	ActualModel string `json:"actual_model"`
	// State is the health gate state observed during selection (healthy, unstable, open, probing).
	State string `json:"state,omitempty"`
}

// RoutingDecisionMigration describes a session owner change.
type RoutingDecisionMigration struct {
	FromChannelID   int    `json:"from_channel_id"`
	FromChannelName string `json:"from_channel_name"`
	ToChannelID     int    `json:"to_channel_id"`
	ToChannelName   string `json:"to_channel_name"`
	// Reason is one of the RoutingMigrationReason constants.
	Reason string `json:"reason"`
}
