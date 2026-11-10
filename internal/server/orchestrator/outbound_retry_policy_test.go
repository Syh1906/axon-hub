//nolint:exhaustruct_v5 // Test fixtures intentionally set only fields relevant to each scenario.
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// ========== 429 Retry-After Tests ==========

func TestPersistentOutboundTransformer_CanRetry_429_WithRetryAfter(t *testing.T) {
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test-channel",
		},
		Outbound: &mockTransformer{},
	}

	t.Run("429 with Retry-After should not retry same channel", func(t *testing.T) {
		outbound := &PersistentOutboundTransformer{
			wrapped: &mockTransformer{},
			state: &PersistenceState{
				CurrentCandidate: &ChannelModelsCandidate{
					Channel: channel,
					Models:  []biz.ChannelModelEntry{{RequestModel: "gpt-4", ActualModel: "gpt-4"}},
				},
				CurrentModelIndex: 0,
			},
		}

		// 429 error with Retry-After header
		httpErr := &httpclient.Error{
			StatusCode: http.StatusTooManyRequests,
			Headers:    http.Header{"Retry-After": []string{"30"}},
		}

		require.False(t, outbound.CanRetry(httpErr))
	})

	t.Run("429 with multiple headers including Retry-After should not retry", func(t *testing.T) {
		outbound := &PersistentOutboundTransformer{
			wrapped: &mockTransformer{},
			state: &PersistenceState{
				CurrentCandidate: &ChannelModelsCandidate{
					Channel: channel,
					Models:  []biz.ChannelModelEntry{{RequestModel: "gpt-4", ActualModel: "gpt-4"}},
				},
				CurrentModelIndex: 0,
			},
		}

		// 429 error with multiple headers
		httpErr := &httpclient.Error{
			StatusCode: http.StatusTooManyRequests,
			Headers: http.Header{
				"Retry-After":  []string{"60"},
				"Content-Type": []string{"application/json"},
			},
		}

		require.False(t, outbound.CanRetry(httpErr))
	})
}

func TestPersistentOutboundTransformer_CanRetry_429_WithoutRetryAfter(t *testing.T) {
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test-channel",
		},
		Outbound: &mockTransformer{},
	}

	t.Run("429 without Retry-After (nil headers) should skip same-channel retry", func(t *testing.T) {
		outbound := &PersistentOutboundTransformer{
			wrapped: &mockTransformer{},
			state: &PersistenceState{
				CurrentCandidate: &ChannelModelsCandidate{
					Channel: channel,
					Models:  []biz.ChannelModelEntry{{RequestModel: "gpt-4", ActualModel: "gpt-4"}},
				},
				CurrentModelIndex: 0,
			},
		}

		// 429 error without headers
		httpErr := &httpclient.Error{
			StatusCode: http.StatusTooManyRequests,
			Headers:    nil,
		}

		require.False(t, outbound.CanRetry(httpErr))
	})

	t.Run("429 without Retry-After (empty headers) should skip same-channel retry", func(t *testing.T) {
		outbound := &PersistentOutboundTransformer{
			wrapped: &mockTransformer{},
			state: &PersistenceState{
				CurrentCandidate: &ChannelModelsCandidate{
					Channel: channel,
					Models:  []biz.ChannelModelEntry{{RequestModel: "gpt-4", ActualModel: "gpt-4"}},
				},
				CurrentModelIndex: 0,
			},
		}

		// 429 error with empty headers
		httpErr := &httpclient.Error{
			StatusCode: http.StatusTooManyRequests,
			Headers:    http.Header{},
		}

		require.False(t, outbound.CanRetry(httpErr))
	})

	t.Run("429 without Retry-After (headers but no Retry-After key) should skip same-channel retry", func(t *testing.T) {
		outbound := &PersistentOutboundTransformer{
			wrapped: &mockTransformer{},
			state: &PersistenceState{
				CurrentCandidate: &ChannelModelsCandidate{
					Channel: channel,
					Models:  []biz.ChannelModelEntry{{RequestModel: "gpt-4", ActualModel: "gpt-4"}},
				},
				CurrentModelIndex: 0,
			},
		}

		// 429 error with headers but no Retry-After
		httpErr := &httpclient.Error{
			StatusCode: http.StatusTooManyRequests,
			Headers: http.Header{
				"Content-Type": []string{"application/json"},
			},
		}

		require.False(t, outbound.CanRetry(httpErr))
	})
}

func TestPersistentOutboundTransformer_CanRetry_ChannelRetryableStatusCodes(t *testing.T) {
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test-channel",
			Settings: &objects.ChannelSettings{
				RetryableStatusCodes: []int{http.StatusBadRequest, http.StatusForbidden},
			},
		},
		Outbound: &mockTransformer{},
	}

	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			CurrentCandidate: &ChannelModelsCandidate{
				Channel: channel,
				Models:  []biz.ChannelModelEntry{{RequestModel: "gpt-4", ActualModel: "gpt-4"}},
			},
			CurrentModelIndex: 0,
		},
	}

	require.True(t, outbound.CanRetry(&httpclient.Error{StatusCode: http.StatusBadRequest}))
	require.True(t, outbound.CanRetry(&httpclient.Error{StatusCode: http.StatusForbidden}))
	require.False(t, outbound.CanRetry(&httpclient.Error{StatusCode: http.StatusUnauthorized}))
}

func TestPersistentOutboundTransformer_CanRetry_429_WithMultipleModels(t *testing.T) {
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test-channel",
		},
		Outbound: &mockTransformer{},
	}

	t.Run("429 with Retry-After should not retry even with multiple models", func(t *testing.T) {
		outbound := &PersistentOutboundTransformer{
			wrapped: &mockTransformer{},
			state: &PersistenceState{
				CurrentCandidate: &ChannelModelsCandidate{
					Channel: channel,
					Models: []biz.ChannelModelEntry{
						{RequestModel: "gpt-4", ActualModel: "gpt-4"},
						{RequestModel: "gpt-3.5-turbo", ActualModel: "gpt-3.5-turbo"},
					},
				},
				CurrentModelIndex: 0,
			},
		}

		// 429 error with Retry-After header
		httpErr := &httpclient.Error{
			StatusCode: http.StatusTooManyRequests,
			Headers:    http.Header{"Retry-After": []string{"30"}},
		}

		// Should skip retry even though there are more models
		require.False(t, outbound.CanRetry(httpErr))
	})
}

func TestPersistentOutboundTransformer_CanRetry_520_WithMultipleModels(t *testing.T) {
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test-channel",
		},
		Outbound: &mockTransformer{},
	}

	t.Run("520 edge error should switch channel instead of iterating models", func(t *testing.T) {
		outbound := &PersistentOutboundTransformer{
			wrapped: &mockTransformer{},
			state: &PersistenceState{
				CurrentCandidate: &ChannelModelsCandidate{
					Channel: channel,
					Models: []biz.ChannelModelEntry{
						{RequestModel: "gpt-4", ActualModel: "gpt-4"},
						{RequestModel: "gpt-3.5-turbo", ActualModel: "gpt-3.5-turbo"},
					},
				},
				CurrentModelIndex: 0,
			},
		}

		// 520 with empty body from a Cloudflare-fronted relay
		httpErr := &httpclient.Error{
			StatusCode: 520,
		}

		// Should skip same-channel model iteration so the pipeline switches
		// to the next candidate instead of grinding aliases on a dead edge.
		require.False(t, outbound.CanRetry(httpErr))
	})
}

func TestPersistentOutboundTransformer_CanRetry_402_WithMultipleModels(t *testing.T) {
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test-channel",
		},
		Outbound: &mockTransformer{},
	}

	t.Run("402 balance exhausted should switch channel instead of iterating models", func(t *testing.T) {
		outbound := &PersistentOutboundTransformer{
			wrapped: &mockTransformer{},
			state: &PersistenceState{
				CurrentCandidate: &ChannelModelsCandidate{
					Channel: channel,
					Models: []biz.ChannelModelEntry{
						{RequestModel: "gpt-4", ActualModel: "gpt-4"},
						{RequestModel: "gpt-3.5-turbo", ActualModel: "gpt-3.5-turbo"},
					},
				},
				CurrentModelIndex: 0,
			},
		}

		// An exhausted account cannot serve any alias on this channel.
		httpErr := &httpclient.Error{
			StatusCode: http.StatusPaymentRequired,
		}

		// Should skip same-channel model iteration so the pipeline switches
		// to the next candidate (a backup channel with a funded account).
		require.False(t, outbound.CanRetry(httpErr))
	})
}

// A transport failure after content was delivered must keep the latency metrics that
// were already captured and persist a classified error, so operators can tell "stalled
// before the first byte" from "cut after N tokens" and the status code is not lost.
func TestOutboundPersistentStream_Close_TransportErrorKeepsMetricsAndClassifiesError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		returned  bool
		streamErr error
		decision  string
	}{
		{"returned stream interrupted", true, fmt.Errorf("read body: %w", io.ErrUnexpectedEOF), "stream_returned"},
		{"pre-read remains retryable", false, fmt.Errorf("read body: %w", io.ErrUnexpectedEOF), ""},
		{"returned stream canceled", true, context.Canceled, "client_canceled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := authz.WithTestBypass(context.Background())

			client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
			defer client.Close()

			ctx = ent.NewContext(ctx, client)
			project := createTestProject(t, ctx, client)
			ch := createTestChannel(t, ctx, client)
			_, requestService, _, usageLogService := setupTestServices(t, client)

			req, err := client.Request.Create().
				SetProjectID(project.ID).
				SetChannelID(ch.ID).
				SetModelID("gpt-4.1").
				SetStatus(request.StatusPending).
				SetRequestBody([]byte(`{"stream":true}`)).
				Save(ctx)
			require.NoError(t, err)

			exec, err := client.RequestExecution.Create().
				SetRequestID(req.ID).
				SetProjectID(project.ID).
				SetChannelID(ch.ID).
				SetModelID("gpt-4.1").
				SetRequestBody([]byte(`{"stream":true}`)).
				SetFormat("openai/chat_completions").
				SetStatus(requestexecution.StatusPending).
				SetStream(true).
				Save(ctx)
			require.NoError(t, err)

			stream := &sliceEventStream{
				events: []*httpclient.StreamEvent{
					{Data: []byte(`{"id":"1","choices":[{"delta":{"content":"He"}}]}`)},
				},
				err: tc.streamErr,
			}

			start := time.Now().Add(-1500 * time.Millisecond)
			firstToken := start.Add(400 * time.Millisecond)
			perf := &biz.PerformanceRecord{StartTime: start, FirstTokenTime: &firstToken, Stream: true}
			transformer := &mockTransformer{apiFormat: llm.APIFormatOpenAIResponse}

			persistentStream := NewOutboundPersistentStream(ctx, stream, req, exec, requestService, usageLogService, transformer, perf, &PersistenceState{StreamReturned: tc.returned})
			for persistentStream.Next() {
				_ = persistentStream.Current()
			}
			require.NoError(t, persistentStream.Close())

			dbExec, err := client.RequestExecution.Get(ctx, exec.ID)
			require.NoError(t, err)
			if errors.Is(tc.streamErr, context.Canceled) {
				require.Equal(t, requestexecution.StatusCanceled, dbExec.Status)
				require.Equal(t, "client_canceled", dbExec.Failure.Source)
			} else {
				require.Equal(t, requestexecution.StatusFailed, dbExec.Status)
				require.Contains(t, dbExec.ErrorMessage, "Upstream provider closed the connection before the response completed")
				require.Contains(t, dbExec.ErrorMessage, "unexpected EOF")
				require.Equal(t, "transport", dbExec.Failure.Source)
				require.Equal(t, ErrCodeUpstreamStreamInterrupted, dbExec.Failure.Code)
			}
			require.Nil(t, dbExec.ResponseStatusCode)
			require.Equal(t, tc.decision, dbExec.RetryDecision)
			require.NotNil(t, dbExec.MetricsFirstTokenLatencyMs)
			require.Equal(t, int64(400), *dbExec.MetricsFirstTokenLatencyMs)
			require.NotNil(t, dbExec.MetricsLatencyMs)
			require.GreaterOrEqual(t, *dbExec.MetricsLatencyMs, int64(1500))
		})
	}
}

// rawErrorTransformer returns a normalized error so the wrapper's raw attachment is observable.
type rawErrorTransformer struct{ mockTransformer }

func (m *rawErrorTransformer) TransformError(_ context.Context, err *httpclient.Error) *llm.ResponseError {
	return &llm.ResponseError{
		StatusCode: err.StatusCode,
		Detail:     llm.ErrorDetail{Type: "api_error", Message: "normalized"},
	}
}

func TestPersistentOutboundTransformer_TransformError_AttachesRawUpstreamOnPassThrough(t *testing.T) {
	httpErr := &httpclient.Error{
		StatusCode: 400,
		Body:       []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"messages.1.output_config: Extra inputs are not permitted"}}`),
	}

	t.Run("pass-through applied", func(t *testing.T) {
		outbound := &PersistentOutboundTransformer{
			wrapped: &rawErrorTransformer{},
			state:   &PersistenceState{PassThroughApplied: true},
		}

		respErr := outbound.TransformError(context.Background(), httpErr)
		require.NotNil(t, respErr)
		require.Same(t, httpErr, respErr.RawUpstream)
		require.Equal(t, "normalized", respErr.Detail.Message)
	})

	t.Run("pass-through not applied", func(t *testing.T) {
		outbound := &PersistentOutboundTransformer{
			wrapped: &rawErrorTransformer{},
			state:   &PersistenceState{},
		}

		respErr := outbound.TransformError(context.Background(), httpErr)
		require.NotNil(t, respErr)
		require.Nil(t, respErr.RawUpstream)
	})
}

func TestPersistentOutboundTransformer_RetrySelectionCounts(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:retry-counts?mode=memory&_fk=0")
	defer client.Close()
	ctx := context.Background()
	service := biz.NewChannelServiceForTest(client)
	defer service.Stop()
	first := &ChannelModelsCandidate{
		Channel: &biz.Channel{Channel: &ent.Channel{ID: 1}, Outbound: &mockTransformer{}},
		Models:  []biz.ChannelModelEntry{{ActualModel: "model-a"}, {ActualModel: "model-b"}},
	}
	second := &ChannelModelsCandidate{
		Channel: &biz.Channel{Channel: &ent.Channel{ID: 2}, Outbound: &mockTransformer{}},
		Models:  []biz.ChannelModelEntry{{ActualModel: "model-c"}},
	}
	lb := NewLoadBalancer(nil, service)
	candidates := (&LoadBalancedSelector{}).sortCandidates(ctx, lb,
		[]*ChannelModelsCandidate{first, second}, &llm.Request{Model: "model"}, 2, 2, true)
	require.Same(t, first, candidates[0])
	processor := &PersistentOutboundTransformer{
		wrapped: first.Channel.Outbound,
		state: &PersistenceState{
			ChannelService: service, CurrentCandidate: first, ChannelModelsCandidates: candidates,
		},
	}
	assertCounts := func(firstCount, secondCount int64) {
		t.Helper()
		for id, want := range map[int]int64{1: firstCount, 2: secondCount} {
			metrics, err := service.GetChannelMetrics(ctx, id)
			require.NoError(t, err)
			require.Equal(t, want, metrics.RequestCount, "channel %d", id)
		}
	}
	assertCounts(1, 0)
	require.NoError(t, processor.PrepareForRetry(ctx)) // next model, same channel
	require.Equal(t, 1, processor.state.CurrentModelIndex)
	assertCounts(2, 0)
	require.NoError(t, processor.PrepareForRetry(ctx)) // same model, same channel
	assertCounts(3, 0)
	require.NoError(t, processor.NextChannel(ctx))
	assertCounts(3, 1)
	require.NoError(t, processor.PrepareForRetry(ctx))
	assertCounts(3, 2)
	require.Error(t, processor.NextChannel(ctx)) // exhausted candidates are not attempts
	assertCounts(3, 2)
}
