package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/looplj/axonhub/internal/scopes"
)

// BudgetPosting attributes a charge to an account. These constraint postings
// must never be summed as separate customer charges in billing reports.
type BudgetPosting struct{ ent.Schema }

func (BudgetPosting) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Immutable(), field.Int("account_id").Immutable(), field.Int("charge_id").Immutable(),
		field.Int64("amount").Immutable(), field.Time("settled_at").Immutable(),
		field.Int64("five_hour_version").Immutable(), field.Int64("daily_version").Immutable(),
		field.Int64("weekly_version").Immutable(), field.Int64("monthly_version").Immutable(), field.Int64("total_version").Immutable(),
	}
}
func (BudgetPosting) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("account_id", "charge_id").Unique().StorageKey("budget_postings_by_account_charge"),
		index.Fields("project_id", "account_id", "settled_at").StorageKey("budget_postings_by_account_time"),
	}
}
func (BudgetPosting) Annotations() []schema.Annotation {
	return []schema.Annotation{entgql.Skip(entgql.SkipAll)}
}
func (BudgetPosting) Policy() ent.Policy {
	return scopes.Policy{Query: scopes.QueryPolicy{scopes.OwnerRule()}, Mutation: scopes.MutationPolicy{scopes.OwnerRule()}}
}
