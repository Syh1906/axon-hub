package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/looplj/axonhub/internal/scopes"
)

// APIKeyOperation records committed batch rows without duplicating credentials.
type APIKeyOperation struct{ ent.Schema }

func (APIKeyOperation) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (APIKeyOperation) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Immutable(), field.Int("actor_id").Immutable(),
		field.String("operation_id").MaxLen(64).Immutable(), field.String("row_id").MaxLen(64).Immutable(),
		field.String("digest").MaxLen(64).Immutable(), field.Int("group_id").Default(0),
		field.JSON("key_ids", []int{}).Default([]int{}),
	}
}
func (APIKeyOperation) Indexes() []ent.Index {
	return []ent.Index{index.Fields("project_id", "actor_id", "operation_id", "row_id").Unique().StorageKey("api_key_operations_by_submission")}
}
func (APIKeyOperation) Annotations() []schema.Annotation {
	return []schema.Annotation{entgql.Skip(entgql.SkipAll)}
}
func (APIKeyOperation) Policy() ent.Policy {
	return scopes.Policy{Query: scopes.QueryPolicy{scopes.OwnerRule()}, Mutation: scopes.MutationPolicy{scopes.OwnerRule()}}
}
