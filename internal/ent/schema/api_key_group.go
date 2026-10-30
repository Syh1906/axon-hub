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
)

// APIKeyGroup groups credentials for reporting without creating login accounts.
type APIKeyGroup struct{ ent.Schema }

func (APIKeyGroup) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (APIKeyGroup) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty().MaxLen(128),
		field.String("note").Default("").MaxLen(1024),
		field.Bool("enabled").Default(true),
		field.Int("revision").Default(0),
		field.Int("project_id").Immutable(),
	}
}
func (APIKeyGroup) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id", "name").Unique().StorageKey("api_key_groups_by_project_name"),
	}
}

func (APIKeyGroup) Edges() []ent.Edge {
	return []ent.Edge{edge.From("project", Project.Type).Ref("api_key_groups").Field("project_id").Unique().Required().Immutable().Annotations(entsql.OnDelete(entsql.Cascade), entgql.Skip(entgql.SkipAll))}
}
func (APIKeyGroup) Annotations() []schema.Annotation {
	return []schema.Annotation{entgql.Skip(entgql.SkipAll)}
}
func (APIKeyGroup) Policy() ent.Policy {
	return scopes.Policy{
		Query:    scopes.QueryPolicy{scopes.UserProjectScopeReadRule(scopes.ScopeReadAPIKeys), scopes.OwnerRule()},
		Mutation: scopes.MutationPolicy{scopes.UserProjectScopeWriteRule(scopes.ScopeWriteAPIKeys), scopes.OwnerRule()},
	}
}
