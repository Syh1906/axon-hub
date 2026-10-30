package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/looplj/axonhub/internal/pkg/budget"
	"github.com/looplj/axonhub/internal/scopes"
)

// BudgetAccount survives deletion of its key/group for accounting and recovery.
// No foreign-key cascade may couple these records to request or credential GC.
type BudgetAccount struct{ ent.Schema }

func (BudgetAccount) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (BudgetAccount) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Immutable(),
		field.Enum("owner_kind").Values("key", "group").Immutable(),
		field.Int("owner_id").Immutable(),
		field.JSON("state", budget.State{}),
		field.Int64("reserved").Default(0),
		field.Int64("revision").Default(0),
	}
}
func (BudgetAccount) Indexes() []ent.Index {
	return []ent.Index{index.Fields("project_id", "owner_kind", "owner_id").Unique().StorageKey("budget_accounts_by_owner")}
}
func (BudgetAccount) Annotations() []schema.Annotation {
	return []schema.Annotation{entgql.Skip(entgql.SkipAll)}
}
func (BudgetAccount) Policy() ent.Policy {
	return scopes.Policy{Query: scopes.QueryPolicy{scopes.OwnerRule()}, Mutation: scopes.MutationPolicy{scopes.OwnerRule()}}
}
