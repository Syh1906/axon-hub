package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/looplj/axonhub/internal/scopes"
)

// ResponsesOrigin retains provenance independently of expiring debug bodies.
// Only digests and execution metadata are stored, never opaque history or keys.
type ResponsesOrigin struct{ ent.Schema }

func (ResponsesOrigin) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (ResponsesOrigin) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Immutable(),
		field.Int("api_key_id").Immutable(),
		field.String("fingerprint").MaxLen(64).Immutable(),
		field.String("origin_ref").MaxLen(64).Immutable(),
		field.Int("channel_id").Immutable(),
		field.String("key_ref").MaxLen(64).Immutable(),
		field.String("key_hint").Immutable(),
		field.String("upstream_ref").MaxLen(64).Immutable(),
		field.Int("request_id").Immutable(),
		field.Int("execution_id").Immutable(),
	}
}

func (ResponsesOrigin) Indexes() []ent.Index {
	return []ent.Index{
		// Both strings are SHA-256 hex digests: 512 bytes under utf8mb4.
		index.Fields("project_id", "api_key_id", "fingerprint", "origin_ref").Unique().
			StorageKey("responses_origins_by_scope_artifact_origin"),
	}
}

func (ResponsesOrigin) Annotations() []schema.Annotation {
	return []schema.Annotation{entgql.Skip(entgql.SkipAll)}
}

func (ResponsesOrigin) Policy() ent.Policy {
	return scopes.Policy{Query: scopes.QueryPolicy{scopes.OwnerRule()}, Mutation: scopes.MutationPolicy{scopes.OwnerRule()}}
}
