package selfusage

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/budgetaccount"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/intercept"
	"github.com/looplj/axonhub/internal/ent/project"
	"github.com/looplj/axonhub/internal/ent/user"
	"github.com/looplj/axonhub/internal/pkg/budget"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/server/middleware"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

func TestGraphqlHandlerTransportAndInputErrors(t *testing.T) {
	h := NewGraphqlHandlers(Dependencies{})
	req := httptest.NewRequest(http.MethodPost, "/self-service/v1/graphql", strings.NewReader(`{"query":"{ __typename }"}`))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	h.Graphql.ServeHTTP(response, req)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"__typename":"Query"`) {
		t.Fatalf("POST GraphQL query: status=%d body=%s", response.Code, response.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/self-service/v1/graphql?query=%7B__typename%7D", nil)
	response = httptest.NewRecorder()
	h.Graphql.ServeHTTP(response, req)
	if response.Code < 400 {
		t.Fatalf("GET must be rejected: status=%d body=%s", response.Code, response.Body.String())
	}

	for _, tc := range []struct {
		name, query string
	}{
		{"malformed date", `{ selfUsageStats(start:"2026-02-30",end:"2026-03-01") { start } }`},
		{"reversed range", `{ selfUsageStats(start:"2026-03-02",end:"2026-03-01") { start } }`},
		{"range over 90 days", `{ selfUsageStats(start:"2026-01-01",end:"2026-04-01") { start } }`},
		{"page zero", `{ selfUsageRequests(start:"2026-01-01",end:"2026-01-01",page:0) { total } }`},
		{"page size over 100", `{ selfUsageRequests(start:"2026-01-01",end:"2026-01-01",pageSize:101) { total } }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]string{"query": tc.query})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/self-service/v1/graphql", strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			h.Graphql.ServeHTTP(response, req)
			var result struct {
				Errors []struct {
					Extensions map[string]any `json:"extensions"`
				} `json:"errors"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatalf("GraphQL response: %v: %s", err, response.Body.String())
			}
			if len(result.Errors) != 1 || result.Errors[0].Extensions["code"] != "BAD_USER_INPUT" {
				t.Fatalf("want BAD_USER_INPUT: status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestGraphqlHandlerAuthChainRejectsMissingKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := middleware.NewSelfServiceRateLimiter(nil)
	router := gin.New()
	router.HandleMethodNotAllowed = true
	router.POST("/self-service/v1/graphql",
		limiter.WithSelfServiceAuthRateLimit(),
		middleware.WithSelfServiceAPIKeyAuth(nil),
		limiter.WithSelfServiceQueryRateLimit(),
		func(c *gin.Context) {
			NewGraphqlHandlers(Dependencies{}).Graphql.ServeHTTP(c.Writer, c.Request)
		},
	)
	for i := 1; i <= 21; i++ {
		req := httptest.NewRequest(http.MethodPost, "/self-service/v1/graphql", strings.NewReader(`{"query":"{ __typename }"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "192.0.2.221:1234"
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		want := http.StatusUnauthorized
		if i >= 20 {
			want = http.StatusTooManyRequests
			if response.Header().Get("Retry-After") == "" {
				t.Fatalf("attempt %d: missing Retry-After", i)
			}
		}
		if response.Code != want {
			t.Fatalf("attempt %d: got %d, want %d, body=%s", i, response.Code, want, response.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/self-service/v1/graphql", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET route: got %d, want 405", response.Code)
	}
}

func TestGraphqlHandlerAuthenticatedMiddlewareChain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.NewEntClient(t, "sqlite3", "file:self_usage_graphql_auth?mode=memory&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	seedCtx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	owner := client.User.Create().SetEmail("self-usage-graphql@example.com").
		SetPassword("test-password").SetFirstName("Self").SetLastName("Usage").
		SetStatus(user.StatusActivated).SaveX(seedCtx)
	proj := client.Project.Create().SetName("Self Usage GraphQL").SetStatus(project.StatusActive).SaveX(seedCtx)
	makeKey := func(name string, allowedIPs ...string) string {
		t.Helper()
		value, err := biz.GenerateAPIKey("ah")
		if err != nil {
			t.Fatal(err)
		}
		client.APIKey.Create().SetName(name).SetKey(value).SetUserID(owner.ID).
			SetProjectID(proj.ID).SetType(apikey.TypeUser).SetStatus(apikey.StatusEnabled).
			SetAllowedIps(allowedIPs).SaveX(seedCtx)
		return value
	}
	validKey := makeKey("allowed")
	restrictedKey := makeKey("restricted", "203.0.113.10")
	cache := xcache.Config{Mode: xcache.ModeMemory}
	projectService := &biz.ProjectService{ProjectCache: xcache.NewFromConfig[xcache.Entry[ent.Project]](cache)}
	keyService := biz.NewAPIKeyService(biz.APIKeyServiceParams{
		CacheConfig: cache, Ent: client, ProjectService: projectService, KeyPrefix: "ah",
	})
	t.Cleanup(keyService.Stop)
	authService := biz.NewAuthService(biz.AuthServiceParams{APIKeyService: keyService, Ent: client})
	systemService := biz.NewSystemService(biz.SystemServiceParams{CacheConfig: cache, Ent: client})
	service := biz.NewSelfUsageService(client, systemService)
	limiter := middleware.NewSelfServiceRateLimiter(nil)
	router := gin.New()
	router.Use(middleware.WithEntClient(client))
	router.POST("/self-service/v1/graphql",
		middleware.WithIPBlocklist(systemService),
		limiter.WithSelfServiceAuthRateLimit(),
		middleware.WithSelfServiceAPIKeyAuth(authService),
		limiter.WithSelfServiceQueryRateLimit(),
		middleware.WithTimeout(30*time.Second),
		func(c *gin.Context) {
			NewGraphqlHandlers(Dependencies{SelfUsageService: service}).Graphql.ServeHTTP(c.Writer, c.Request)
		},
	)
	for _, tc := range []struct {
		name, key string
		want      int
	}{
		{"valid key", validKey, http.StatusOK},
		{"IP whitelist mismatch", restrictedKey, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/self-service/v1/graphql", strings.NewReader(`{"query":"{ selfUsageMeta { apiKeyName apiKeyType maxRangeDays } }"}`))
			req.Header.Set("Authorization", "Bearer "+tc.key)
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = "198.51.100.77:12345"
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			if response.Code != tc.want {
				t.Fatalf("status=%d, want=%d body=%s", response.Code, tc.want, response.Body.String())
			}
			if tc.want == http.StatusOK && !strings.Contains(response.Body.String(), `"apiKeyName":"allowed · Self Usage"`) {
				t.Fatalf("unexpected meta response: %s", response.Body.String())
			}
		})
	}
	for attempt := 2; attempt <= 61; attempt++ {
		req := httptest.NewRequest(http.MethodPost, "/self-service/v1/graphql", strings.NewReader(`{"query":"{ __typename }"}`))
		req.Header.Set("Authorization", "Bearer "+validKey)
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "198.51.100.77:12345"
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		want := http.StatusOK
		if attempt == 61 {
			want = http.StatusTooManyRequests
			if response.Header().Get("Retry-After") == "" {
				t.Fatal("query rate limit response missing Retry-After")
			}
		}
		if response.Code != want {
			t.Fatalf("query %d: status=%d want=%d body=%s", attempt, response.Code, want, response.Body.String())
		}
	}
}

func TestGraphqlHandlerSelfUsageFieldBudget(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:self_usage_graphql_budget?mode=memory&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	setup := authz.WithTestBypass(context.Background())
	project := client.Project.Create().SetName("budget").SaveX(setup)
	key := client.APIKey.Create().SetName("budget").SetKey("fixture-budget-not-a-secret").SetProjectID(project.ID).SaveX(setup)
	var queries atomic.Int64
	client.Intercept(intercept.Func(func(_ context.Context, _ intercept.Query) error {
		queries.Add(1)
		return nil
	}))
	cache := xcache.Config{Mode: xcache.ModeMemory}
	service := biz.NewSelfUsageService(client, biz.NewSystemService(biz.SystemServiceParams{CacheConfig: cache, Ent: client}))
	handler := NewGraphqlHandlers(Dependencies{SelfUsageService: service}).Graphql

	for _, tc := range []struct {
		name, query, operationName string
		variables                  map[string]any
		reject                     bool
	}{
		{"aliased budgets", `{ a: selfUsageBudgets { ownerID } b: selfUsageBudgets { ownerID } }`, "", nil, true},
		{"aliased requests", `{ first: selfUsageRequests(start:"2026-01-01",end:"2026-01-01") { total } second: selfUsageRequests(start:"2026-01-01",end:"2026-01-01") { total } }`, "", nil, true},
		{"same response name", `{ selfUsageRequests(start:"2026-01-01",end:"2026-01-01") { total } selfUsageRequests(start:"2026-01-01",end:"2026-01-01") { total } }`, "", nil, true},
		{"named fragment", `query { selfUsageRequests(start:"2026-01-01",end:"2026-01-01") { total } ...More } fragment More on Query { ...Nested } fragment Nested on Query { alias: selfUsageRequests(start:"2026-01-01",end:"2026-01-01") { total } }`, "", nil, true},
		{"repeated fragment spread", `query { ...Once ...Once } fragment Once on Query { selfUsageMeta { apiKeyName } }`, "", nil, true},
		{"inline fragment", `{ ... on Query { selfUsageStats(start:"2026-01-01",end:"2026-01-01") { start } } selfUsageStats(start:"2026-01-01",end:"2026-01-01") { start } }`, "", nil, true},
		{"selected operation", `query Chosen { selfUsageRequests(start:"2026-01-01",end:"2026-01-01") { total } ...More } query Other { __typename } fragment More on Query { selfUsageRequests(start:"2026-01-01",end:"2026-01-01") { total } }`, "Chosen", nil, true},
		{"variable includes duplicate", `query Chosen($on:Boolean!) { selfUsageRequests(start:"2026-01-01",end:"2026-01-01") { total } ...More @include(if:$on) } fragment More on Query { selfUsageRequests(start:"2026-01-01",end:"2026-01-01") { total } }`, "Chosen", map[string]any{"on": true}, true},
		{"variable excludes duplicate", `query Chosen($on:Boolean!) { selfUsageRequests(start:"2026-01-01",end:"2026-01-01") { total } ...More @include(if:$on) } fragment More on Query { selfUsageRequests(start:"2026-01-01",end:"2026-01-01") { total } }`, "Chosen", map[string]any{"on": false}, false},
		{"skip duplicate", `query Chosen($off:Boolean!) { selfUsageRequests(start:"2026-01-01",end:"2026-01-01") { total } ...More @skip(if:$off) } fragment More on Query { selfUsageRequests(start:"2026-01-01",end:"2026-01-01") { total } }`, "Chosen", map[string]any{"off": true}, false},
		{"other operation not counted", `query Chosen { selfUsageRequests(start:"2026-01-01",end:"2026-01-01") { total } } query Other { first: selfUsageMeta { apiKeyName } second: selfUsageMeta { apiKeyName } }`, "Chosen", nil, false},
		{"singleton fields", `{ selfUsageMeta { apiKeyName } selfUsageStats(start:"2026-01-01",end:"2026-01-01") { start } selfUsageRequests(start:"2026-01-01",end:"2026-01-01") { total } }`, "", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queries.Store(0)
			body, err := json.Marshal(map[string]any{"query": tc.query, "operationName": tc.operationName, "variables": tc.variables})
			if err != nil {
				t.Fatal(err)
			}
			ctx := contexts.WithAPIKey(context.Background(), key)
			req := httptest.NewRequest(http.MethodPost, "/self-service/v1/graphql", bytes.NewReader(body)).WithContext(ctx)
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			var result struct {
				Errors []struct {
					Extensions map[string]any `json:"extensions"`
				} `json:"errors"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatalf("decode response: %v: %s", err, response.Body.String())
			}
			if response.Code != http.StatusOK {
				t.Fatalf("unexpected HTTP status %d: %s", response.Code, response.Body.String())
			}
			if tc.reject {
				if len(result.Errors) != 1 || result.Errors[0].Extensions["code"] != "BAD_USER_INPUT" || queries.Load() != 0 {
					t.Fatalf("want BAD_USER_INPUT before DB queries (got %d): %s", queries.Load(), response.Body.String())
				}
			} else if len(result.Errors) != 0 || queries.Load() == 0 {
				t.Fatalf("want successful resolver execution and DB query (got %d): %s", queries.Load(), response.Body.String())
			}
		})
	}
}

func TestRepeatedSelfUsageFieldFragmentCycle(t *testing.T) {
	for _, tc := range []struct {
		name, query, repeated string
	}{
		{"single cyclic spread", `query { ...Loop } fragment Loop on Query { ...Loop selfUsageMeta { apiKeyName } }`, ""},
		{"cyclic fragment spread twice", `query { ...Loop ...Loop } fragment Loop on Query { ...Loop selfUsageMeta { apiKeyName } }`, "selfUsageMeta"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := parser.ParseQuery(&ast.Source{Input: tc.query})
			if err != nil {
				t.Fatal(err)
			}
			if field := repeatedSelfUsageField(&graphql.OperationContext{Doc: doc, Operation: doc.Operations[0]}); field != tc.repeated {
				t.Fatalf("repeated field = %q, want %q", field, tc.repeated)
			}
		})
	}
}

func TestBudgetSelfUsageReturnsOnlyCurrentKeyAndGroup(t *testing.T) {
	db := enttest.NewEntClient(t, "sqlite3", "file:self_budget_isolation?mode=memory&_fk=1")
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	seed := authz.WithTestBypass(context.Background())
	p := db.Project.Create().SetName("current").SaveX(seed)
	other := db.Project.Create().SetName("other").SaveX(seed)
	group := db.APIKeyGroup.Create().SetProjectID(p.ID).SetName("shared").SaveX(seed)
	key := db.APIKey.Create().SetName("self").SetKey("fixture-self-budget").SetProjectID(p.ID).SetGroupRef(group.ID).SaveX(seed)
	otherKey := db.APIKey.Create().SetName("other").SetKey("fixture-other-budget").SetProjectID(other.ID).SaveX(seed)
	for _, target := range []struct {
		project, id int
		kind        budgetaccount.OwnerKind
	}{{p.ID, key.ID, budgetaccount.OwnerKindKey}, {p.ID, group.ID, budgetaccount.OwnerKindGroup}, {other.ID, otherKey.ID, budgetaccount.OwnerKindKey}} {
		state := budget.NewState()
		state.Policy.StartedAt = time.Now().UTC()
		state.Policy.Rules = []budget.Rule{{Period: budget.Total, Mode: budget.Fixed, Limit: 100000000}}
		db.BudgetAccount.Create().SetProjectID(target.project).SetOwnerKind(target.kind).SetOwnerID(target.id).SetState(state).SaveX(seed)
	}
	service := biz.NewSelfUsageService(db, biz.NewSystemService(biz.SystemServiceParams{Ent: db, CacheConfig: xcache.Config{Mode: xcache.ModeMemory}}))
	handler := NewGraphqlHandlers(Dependencies{SelfUsageService: service}).Graphql
	ctx := contexts.WithAPIKey(context.Background(), key)
	payload, err := json.Marshal(map[string]string{"query": "{ selfUsageBudgets { ownerKind ownerID windows { period limit settled reserved available } } }"})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/self-service/v1/graphql", bytes.NewReader(payload)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	var result struct {
		Data struct {
			Views []*biz.BudgetView `json:"selfUsageBudgets"`
		}
		Errors []any
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.Empty(t, result.Errors)
	require.Len(t, result.Data.Views, 2)
	require.Equal(t, "key", result.Data.Views[0].OwnerKind)
	require.Equal(t, key.ID, result.Data.Views[0].OwnerID)
	require.Equal(t, "group", result.Data.Views[1].OwnerKind)
	require.Equal(t, group.ID, result.Data.Views[1].OwnerID)
	require.Equal(t, "1.00000000", result.Data.Views[0].Windows[0].Available)
}
