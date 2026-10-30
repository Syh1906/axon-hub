package selfusage

import (
	"context"
	"net/http"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"go.uber.org/fx"

	"github.com/looplj/axonhub/internal/server/biz"
)

type GraphqlHandler struct {
	Graphql http.Handler
}

type Dependencies struct {
	fx.In

	SelfUsageService *biz.SelfUsageService
}

func NewGraphqlHandlers(deps Dependencies) *GraphqlHandler {
	server := handler.New(NewSchema(deps.SelfUsageService))
	// The API key is supplied in Authorization; only POST requests are accepted.
	server.AddTransport(transport.POST{})
	server.SetQueryCache(lru.New[*ast.QueryDocument](1024))
	server.SetErrorPresenter(func(ctx context.Context, err error) *gqlerror.Error {
		presented := graphql.DefaultErrorPresenter(ctx, err)
		if strings.HasPrefix(presented.Message, "invalid_range:") || strings.HasPrefix(presented.Message, "invalid_page:") {
			presented.Extensions = map[string]any{"code": "BAD_USER_INPUT"}
		}
		return presented
	})
	// Count AST occurrences rather than collected fields: gqlgen merges selections
	// sharing a response name, while aliases and repeated fragments can execute
	// the same expensive root field more than once.
	server.AroundOperations(func(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
		op := graphql.GetOperationContext(ctx)
		if field := repeatedSelfUsageField(op); field != "" {
			return graphql.OneShot(&graphql.Response{Errors: gqlerror.List{{
				Message:    "only one " + field + " selection is allowed per operation",
				Extensions: map[string]any{"code": "BAD_USER_INPUT"},
			}}})
		}
		return next(ctx)
	})
	return &GraphqlHandler{Graphql: server}
}

// repeatedSelfUsageField traverses only selections that can run in the chosen
// operation. The active-fragment set prevents cycles without suppressing a
// second spread of the same fragment along another path.
func repeatedSelfUsageField(op *graphql.OperationContext) string {
	if op == nil || op.Operation == nil {
		return ""
	}
	counts := make(map[string]int, 3)
	active := make(map[string]bool)
	var walk func(ast.SelectionSet) string
	walk = func(selections ast.SelectionSet) string {
		for _, selection := range selections {
			switch node := selection.(type) {
			case *ast.Field:
				if !selfUsageIncluded(node.Directives, op.Variables) {
					continue
				}
				switch node.Name {
				case "selfUsageMeta", "selfUsageStats", "selfUsageRequests", "selfUsageBudgets":
					counts[node.Name]++
					if counts[node.Name] > 1 {
						return node.Name
					}
				}
			case *ast.InlineFragment:
				if selfUsageIncluded(node.Directives, op.Variables) {
					if field := walk(node.SelectionSet); field != "" {
						return field
					}
				}
			case *ast.FragmentSpread:
				if !selfUsageIncluded(node.Directives, op.Variables) || active[node.Name] {
					continue
				}
				fragment := op.Doc.Fragments.ForName(node.Name)
				if fragment == nil || !selfUsageIncluded(fragment.Directives, op.Variables) {
					continue
				}
				active[node.Name] = true
				field := walk(fragment.SelectionSet)
				delete(active, node.Name)
				if field != "" {
					return field
				}
			}
		}
		return ""
	}
	return walk(op.Operation.SelectionSet)
}

func selfUsageIncluded(directives ast.DirectiveList, variables map[string]any) bool {
	for _, name := range []string{"skip", "include"} {
		if directive := directives.ForName(name); directive != nil {
			if argument := directive.Arguments.ForName("if"); argument != nil {
				value, err := argument.Value.Value(variables)
				if err == nil {
					if enabled, ok := value.(bool); ok && ((name == "skip" && enabled) || (name == "include" && !enabled)) {
						return false
					}
				}
			}
		}
	}
	return true
}
