package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/looplj/axonhub/internal/scopes"
)

// StatisticsRevision records the committed revision of a reporting date.
// One source change updates one shared revision, regardless of the number of
// leaderboard dimensions, open pages, or cached results.
type StatisticsRevision struct{ ent.Schema }

func (StatisticsRevision) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Immutable(),
		field.Time("occurred_at").Immutable(),
		field.String("version").MaxLen(36),
	}
}

func (StatisticsRevision) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id", "occurred_at").Unique().StorageKey("statistics_revision_by_project_time"),
		index.Fields("occurred_at").StorageKey("statistics_revision_by_time"),
	}
}

func (StatisticsRevision) Annotations() []schema.Annotation {
	return []schema.Annotation{entgql.Skip(entgql.SkipAll)}
}

func (StatisticsRevision) Policy() ent.Policy {
	return scopes.Policy{Query: scopes.QueryPolicy{scopes.OwnerRule()}, Mutation: scopes.MutationPolicy{scopes.OwnerRule()}}
}
