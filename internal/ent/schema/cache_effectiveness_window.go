package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/looplj/axonhub/internal/scopes"
)

// CacheEffectivenessWindow keeps settled CCH-compatible cache samples.
type CacheEffectivenessWindow struct{ ent.Schema }

func (CacheEffectivenessWindow) Annotations() []schema.Annotation {
	return []schema.Annotation{entgql.Skip()}
}

func (CacheEffectivenessWindow) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Immutable(),
		field.Int("channel_id").Immutable(),
		field.String("model_id").MaxLen(255).Immutable(),
		field.String("cache_ttl_bucket").MaxLen(8).Immutable(),
		field.Time("window_start").Immutable(),
		field.Time("window_end").Immutable(),
		field.Int64("sample_count"),
		field.Int64("eligible_count"),
		field.Int64("theoretical_tokens"),
		field.Int64("observed_tokens"),
	}
}

func (CacheEffectivenessWindow) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id", "channel_id", "model_id", "cache_ttl_bucket", "window_end").Unique().StorageKey("cache_effectiveness_window_unique"),
		index.Fields("project_id", "window_end").StorageKey("cache_effectiveness_by_project_window"),
		index.Fields("window_end").StorageKey("cache_effectiveness_by_window"),
	}
}

func (CacheEffectivenessWindow) Policy() ent.Policy {
	return scopes.Policy{} // Only the authorized analytics service and scheduler may bypass privacy.
}
