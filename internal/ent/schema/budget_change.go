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

type BudgetChange struct{ ent.Schema }

func (BudgetChange) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (BudgetChange) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Immutable(), field.Int("account_id").Immutable(), field.Int("actor_id").Immutable(),
		field.String("operation_id").MaxLen(64).Immutable(),
		field.Enum("operation").Values("configure", "reset").Immutable(),
		field.JSON("before", budget.State{}).Immutable(), field.JSON("after", budget.State{}).Immutable(),
	}
}
func (BudgetChange) Indexes() []ent.Index {
	return []ent.Index{index.Fields("project_id", "operation_id").Unique().StorageKey("budget_changes_by_operation"), index.Fields("project_id", "account_id", "created_at").StorageKey("budget_changes_by_account_time")}
}
func (BudgetChange) Annotations() []schema.Annotation {
	return []schema.Annotation{entgql.Skip(entgql.SkipAll)}
}
func (BudgetChange) Policy() ent.Policy {
	return scopes.Policy{Query: scopes.QueryPolicy{scopes.OwnerRule()}, Mutation: scopes.MutationPolicy{scopes.OwnerRule()}}
}
