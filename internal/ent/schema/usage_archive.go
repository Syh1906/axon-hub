package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/looplj/axonhub/internal/scopes"
)

// UsageArchive keeps only compact numeric statistics after detail removal. The
// original timestamp and attribution preserve time filters without retaining
// prompts, responses, headers, IP addresses or price snapshots.
type UsageArchive struct{ ent.Schema }

func (UsageArchive) Fields() []ent.Field {
	fields := []ent.Field{
		field.Enum("kind").Values("request", "usage").Immutable(),
		field.Int("source_id").Immutable(),
		field.Int("request_id").Immutable(),
		field.Int("project_id").Immutable(),
		field.Int("api_key_id").Default(0).Immutable(),
		field.Int("channel_id").Default(0).Immutable(),
		field.String("model_id").Immutable(),
		field.String("request_model_id").Default("").Immutable(),
		field.String("request_status").MaxLen(16).Default("").Immutable(),
		field.String("request_source").MaxLen(16).Default("api").Immutable(),
		field.Time("occurred_at").Immutable(),
		field.Float("total_cost").Optional().Nillable(),
		field.Float("cache_creation_cost").Default(0),
		field.Float("first_token_sum").Default(0),
		field.Float("speed_sum").Default(0),
		field.Bool("pricing_complete").Optional().Nillable(),
		field.Int("speed_version").Default(0),
	}
	for _, name := range []string{"prompt_tokens", "completion_tokens", "total_tokens", "prompt_cached_tokens",
		"prompt_write_cached_tokens", "completion_reasoning_tokens", "cache_requests", "cache_input",
		"success_count", "outcome_count", "first_token_count", "speed_count"} {
		fields = append(fields, field.Int64(name).Default(0))
	}
	return fields
}

func (UsageArchive) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("kind", "source_id").Unique().StorageKey("usage_archive_by_source"),
		index.Fields("kind", "occurred_at").StorageKey("usage_archive_by_kind_time"),
		index.Fields("project_id", "kind", "occurred_at").StorageKey("usage_archive_by_project_kind_time"),
		index.Fields("occurred_at").StorageKey("usage_archive_by_time"),
	}
}

func (UsageArchive) Annotations() []schema.Annotation {
	return []schema.Annotation{entgql.Skip(entgql.SkipAll)}
}
func (UsageArchive) Policy() ent.Policy {
	return scopes.Policy{
		Query:    scopes.QueryPolicy{scopes.OwnerRule(), scopes.UserProjectScopeReadRequestsRule(scopes.ScopeReadRequests), scopes.UserReadScopeRule(scopes.ScopeReadRequests)},
		Mutation: scopes.MutationPolicy{scopes.OwnerRule()},
	}
}
