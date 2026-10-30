package pipeline

import (
	"context"
	"errors"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/stretchr/testify/require"
	"testing"
)

type diagnosticOutbound struct {
	*mockOutbound
	reason string
}

func (d *diagnosticOutbound) OnRetryStopped(_ context.Context, reason string) { d.reason = reason }

func TestRetryDiagnosticsReportsActualStop(t *testing.T) {
	for _, tc := range []struct {
		name      string
		retries   int
		retryable bool
		expected  string
	}{
		{"disabled", 0, true, "retry_disabled"}, {"limit", 1, true, "retry_limit"}, {"exhausted", 1, false, "candidates_exhausted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outbound := &diagnosticOutbound{mockOutbound: &mockOutbound{canRetry: func(error) bool { return tc.retryable }, hasMoreChannels: func() bool { return false }}}
			attempts := 0
			p := &pipeline{Executor: &mockExecutor{do: func(context.Context, *httpclient.Request) (*httpclient.Response, error) {
				attempts++
				return nil, errors.New("fixture")
			}}, Inbound: &mockInbound{}, Outbound: outbound, maxSameChannelRetries: tc.retries}
			_, err := p.Process(context.Background(), &httpclient.Request{})
			require.Error(t, err)
			require.Equal(t, tc.expected, outbound.reason)
			if tc.retryable {
				require.Equal(t, tc.retries+1, attempts)
			} else {
				require.Equal(t, 1, attempts)
			}
		})
	}
}
