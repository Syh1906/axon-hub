package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/budget"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/looplj/axonhub/llm"
)

// BudgetCharge records one upstream attempt, not one logical request. Terminal
// records and attribution remain immutable when callbacks are replayed.
type BudgetCharge struct{ ent.Schema }

func (BudgetCharge) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (BudgetCharge) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Immutable(),
		field.Int("execution_id").Immutable(),
		field.Int("request_id").Immutable(),
		field.Int("key_id").Immutable(),
		field.Int("group_id").Optional().Nillable().Immutable(),
		field.Int("key_account_id").Immutable(),
		field.Int("group_account_id").Optional().Nillable().Immutable(),
		field.Int64("estimate").Immutable(),
		field.Int64("actual").Optional().Nillable(),
		field.JSON("usage", &llm.Usage{}).Optional(),
 field.JSON("async_task", &objects.BudgetAsyncTask{}).Optional(),
		field.JSON("cost_snapshot", &objects.RequestCostSnapshot{}).Optional(),
		field.Int("usage_log_id").Optional().Nillable(),
		field.JSON("key_versions", map[budget.Period]int64{}).Immutable(),
		field.JSON("group_versions", map[budget.Period]int64{}).Immutable(),
		field.Enum("status").Values("prepared", "sent", "unknown", "settled", "released").Default("prepared"),
		field.Time("check_after"),
		field.Time("settled_at").Optional().Nillable(),
	}
}
func (BudgetCharge) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("execution_id").Unique().StorageKey("budget_charges_by_execution"),
		index.Fields("project_id", "request_id").StorageKey("budget_charges_by_request"),
		index.Fields("check_after", "status").StorageKey("budget_charges_by_recovery_time"),
	}
}
func (BudgetCharge) Annotations() []schema.Annotation {
	return []schema.Annotation{entgql.Skip(entgql.SkipAll)}
}
func (BudgetCharge) Policy() ent.Policy {
	return scopes.Policy{Query: scopes.QueryPolicy{scopes.OwnerRule()}, Mutation: scopes.MutationPolicy{scopes.OwnerRule()}}
}
