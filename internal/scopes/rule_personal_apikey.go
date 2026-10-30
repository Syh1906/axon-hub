package scopes

import (
	"entgo.io/ent/entql"
	"github.com/looplj/axonhub/internal/ent/privacy"
)

// PersonalKeyProjectFilter is also used by credential-authenticated OpenAPI reads.
type PersonalKeyProjectFilter interface {
	WhereProjectID(entql.IntP)
	Where(entql.P)
}

// UserPersonalAPIKeyReadRule grants readers access to all records in their authorized project.
// Personal credentials retain their authentication semantics; management reads
// use the project permission rather than credential ownership.
func UserPersonalAPIKeyReadRule(requiredScope ScopeSlug) privacy.QueryRule {
	return UserProjectScopeReadRule(requiredScope)
}
