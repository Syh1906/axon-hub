package jina

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm/httpclient"
)

func TestRerankUsageInputAccounting(t *testing.T) {
	for _, tc := range []struct {
		name, usage string
		input       int64
		missing     bool
	}{
		{"only total", `{"total_tokens":5}`, 5, false},
		{"explicit zero", `{"prompt_tokens":0,"total_tokens":5}`, 0, false},
		{"input only", `{"input_tokens":7}`, 7, false},
		{"missing", `{"cost":0}`, 0, true},
		{"negative total only", `{"total_tokens":-1}`, 0, true},
		{"negative total with input", `{"input_tokens":0,"total_tokens":-1}`, 0, true},
		{"negative input", `{"input_tokens":-1,"total_tokens":5}`, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &httpclient.Response{Body: []byte(`{"model":"rank","results":[],"usage":` + tc.usage + `}`)}
			resp, err := new(OutboundTransformer).transformRerankResponse(t.Context(), upstream)
			require.NoError(t, err)
			if tc.missing {
				require.Nil(t, resp.Usage)
				return
			}
			require.NotNil(t, resp.Usage)
			require.Equal(t, tc.input, resp.Usage.PromptTokens)
		})
	}
}
