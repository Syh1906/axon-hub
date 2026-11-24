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

type APIKeyGroupChannelRate struct{ ent.Schema }

func (APIKeyGroupChannelRate) Fields() []ent.Field {
	return []ent.Field{
		field.Int("group_id").Immutable(), field.Int("channel_id").Immutable(),
		field.String("multiplier").GoType(decimal.Decimal{}),
	}
}
func (APIKeyGroupChannelRate) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("group", APIKeyGroup.Type).Ref("channel_rates").Field("group_id").Unique().Required().Immutable().Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.From("channel", Channel.Type).Ref("group_rates").Field("channel_id").Unique().Required().Immutable().Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}
func (APIKeyGroupChannelRate) Indexes() []ent.Index {
	return []ent.Index{index.Fields("group_id", "channel_id").Unique().StorageKey("api_key_group_rates_by_owner_channel")}
}
func (APIKeyGroupChannelRate) Annotations() []schema.Annotation {
	return []schema.Annotation{entgql.Skip(entgql.SkipAll)}
}
func (APIKeyGroupChannelRate) Policy() ent.Policy {
	return scopes.Policy{Query: scopes.QueryPolicy{scopes.OwnerRule()}, Mutation: scopes.MutationPolicy{scopes.OwnerRule()}}
}
