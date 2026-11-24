package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/shopspring/decimal"
)

type APIKeyChannelRate struct{ ent.Schema }

func (APIKeyChannelRate) Fields() []ent.Field {
	return []ent.Field{
		field.Int("key_id").Immutable(), field.Int("channel_id").Immutable(),
		field.String("multiplier").GoType(decimal.Decimal{}),
	}
}
func (APIKeyChannelRate) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("key", APIKey.Type).Ref("channel_rates").Field("key_id").Unique().Required().Immutable().Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.From("channel", Channel.Type).Ref("key_rates").Field("channel_id").Unique().Required().Immutable().Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}
func (APIKeyChannelRate) Indexes() []ent.Index {
	return []ent.Index{index.Fields("key_id", "channel_id").Unique().StorageKey("api_key_rates_by_owner_channel")}
}
func (APIKeyChannelRate) Annotations() []schema.Annotation {
	return []schema.Annotation{entgql.Skip(entgql.SkipAll)}
}
func (APIKeyChannelRate) Policy() ent.Policy {
	return scopes.Policy{Query: scopes.QueryPolicy{scopes.OwnerRule()}, Mutation: scopes.MutationPolicy{scopes.OwnerRule()}}
}
