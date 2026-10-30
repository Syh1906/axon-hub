package scopes

import "github.com/looplj/axonhub/internal/ent/privacy"

// UserProjectScopeReadRequestsRule grants readers access to all records in their authorized project.
// Personal credentials retain their authentication semantics; management reads
// use the project permission rather than credential ownership.
func UserProjectScopeReadRequestsRule(requiredScope ScopeSlug) privacy.QueryRule {
	return UserProjectScopeReadRule(requiredScope)
}
