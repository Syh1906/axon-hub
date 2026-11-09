package gql

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdminGraphQLSlowBodyGetsFreshProcessingBudget(t *testing.T) {
	budgets := make(chan time.Duration, 1)
	graph := handler.New(NewExecutableSchema(Config{Resolvers: &Resolver{}}))
	graph.AddTransport(transport.POST{})
	graph.AroundOperations(withOperationTimeout)
	graph.AroundOperations(func(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
		deadline, _ := ctx.Deadline()
		budgets <- time.Until(deadline)
		return next(ctx)
	})
	router := gin.New()
	wrapped := WithRequestTimeout(graph, 80*time.Millisecond, 2*time.Second, 80*time.Millisecond)
	router.POST("/admin/graphql", func(c *gin.Context) { wrapped.ServeHTTP(c.Writer, c.Request) })
	server := httptest.NewUnstartedServer(router)
	server.Config.WriteTimeout = 80 * time.Millisecond
	server.Start()
	defer server.Close()
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(server.URL, "http://"), time.Second)
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.SetDeadline(time.Now().Add(4*time.Second)))
	body := `{"query":"{__typename}"}`
	_, err = fmt.Fprintf(conn, "POST /admin/graphql HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body[:1])
	require.NoError(t, err)
	time.Sleep(160 * time.Millisecond)
	_, err = io.WriteString(conn, body[1:])
	require.NoError(t, err)
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.JSONEq(t, `{"data":{"__typename":"Query"}}`, string(data))
	require.Greater(t, <-budgets, 40*time.Millisecond, "接收时间不能消耗执行预算")
}

func TestAdminGraphQLStalledBodyTerminatesRead(t *testing.T) {
	for _, contentType := range []string{"application/json", "multipart/form-data; boundary=fixture"} {
		t.Run(contentType, func(t *testing.T) {
			graph := NewGraphqlHandlers(Dependencies{}).Graphql
			server := httptest.NewServer(WithRequestTimeout(graph, time.Second, 60*time.Millisecond, time.Second))
			defer server.Close()
			conn, err := net.DialTimeout("tcp", strings.TrimPrefix(server.URL, "http://"), time.Second)
			require.NoError(t, err)
			defer conn.Close()
			require.NoError(t, conn.SetDeadline(time.Now().Add(3*time.Second)))
			_, err = fmt.Fprintf(conn, "POST /admin/graphql HTTP/1.1\r\nHost: localhost\r\nContent-Type: %s\r\nContent-Length: 100\r\nConnection: close\r\n\r\n{", contentType)
			require.NoError(t, err)
			started := time.Now()
			response, err := http.ReadResponse(bufio.NewReader(conn), nil)
			require.NoError(t, err)
			defer response.Body.Close()
			data, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, http.StatusRequestTimeout, response.StatusCode)
			require.Contains(t, string(data), "REQUEST_BODY_TIMEOUT")
			require.NotContains(t, string(data), "missing operation context")
			require.Less(t, time.Since(started), 2*time.Second)
		})
	}
}

func TestAdminGraphQLExecutionStillExpiresAndCancels(t *testing.T) {
	operationContexts := make(chan context.Context, 1)
	graph := handler.New(NewExecutableSchema(Config{Resolvers: &Resolver{}}))
	graph.AddTransport(transport.POST{})
	graph.AroundOperations(withOperationTimeout)
	graph.AroundOperations(func(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
		operationContexts <- ctx
		return func(_ context.Context) *graphql.Response {
			<-ctx.Done()
			return graphql.ErrorResponse(ctx, "processing finished: %v", ctx.Err())
		}
	})
	server := httptest.NewServer(WithRequestTimeout(graph, 35*time.Millisecond, time.Second, time.Second))
	defer server.Close()
	response, err := http.Post(server.URL, "application/json", strings.NewReader(`{"query":"{__typename}"}`))
	require.NoError(t, err)
	defer response.Body.Close()
	var data map[string]any
	require.NoError(t, json.NewDecoder(response.Body).Decode(&data))
	require.Contains(t, fmt.Sprint(data), "context deadline exceeded")
	require.ErrorIs(t, (<-operationContexts).Err(), context.DeadlineExceeded)
}

func TestAdminGraphQLDisconnectCancelsExecution(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan error, 1)
	graph := handler.New(NewExecutableSchema(Config{Resolvers: &Resolver{}}))
	graph.AddTransport(transport.POST{})
	graph.AroundOperations(withOperationTimeout)
	graph.AroundOperations(func(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
		close(started)
		return func(_ context.Context) *graphql.Response {
			<-ctx.Done()
			stopped <- ctx.Err()
			return graphql.ErrorResponse(ctx, "request ended")
		}
	})
	server := httptest.NewServer(WithRequestTimeout(graph, 5*time.Second, time.Second, 5*time.Second))
	defer server.Close()
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(server.URL, "http://"), time.Second)
	require.NoError(t, err)
	defer conn.Close()
	body := `{"query":"{__typename}"}`
	_, err = fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
	require.NoError(t, err)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("operation did not start")
	}
	require.NoError(t, conn.Close())
	select {
	case err := <-stopped:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("client disconnect did not cancel execution")
	}
}
