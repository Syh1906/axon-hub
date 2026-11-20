package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/looplj/axonhub/internal/scopes"
)

// GeminiCache records the owner and upstream destination, never the cached prompt.
type GeminiCache struct{ ent.Schema }

func (GeminiCache) Fields() []ent.Field {
	return []ent.Field{
		field.String("public_id").MaxLen(36).Immutable().Unique(),
		field.Int("project_id").Immutable(), field.Int("api_key_id").Immutable(),
		field.Int("channel_id").Immutable(), field.String("key_ref").Immutable(),
		field.String("base_url").Immutable(), field.String("upstream_name").Immutable(),
		field.String("model").Immutable(), field.String("upstream_model").Immutable(),
		field.Time("expire_time"), field.Bytes("metadata"),
		// A renewal may have reached the provider before its response was lost.
		// Keep its cleanup route without extending the authorized use of the cache.
		field.Time("retain_until").Optional().Nillable(),
		field.Int("revision").Default(0),
	}
}
func (GeminiCache) Indexes() []ent.Index {
	return []ent.Index{index.Fields("project_id", "api_key_id", "id").StorageKey("gemini_caches_by_owner_id")}
}
func (GeminiCache) Annotations() []schema.Annotation {
	return []schema.Annotation{entgql.Skip(entgql.SkipAll)}
}
func (GeminiCache) Policy() ent.Policy {
	return scopes.Policy{Query: scopes.QueryPolicy{scopes.OwnerRule()}, Mutation: scopes.MutationPolicy{scopes.OwnerRule()}}
}
