package gql

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestUnmarshalInputChannelSettingsInput_ResponseTimeout(t *testing.T) {
	ec := &executionContext{}
	for _, input := range []map[string]any{{}, {"streamFirstEventTimeoutSeconds": nil}, {"streamFirstEventTimeoutSeconds": 0, "nonStreamResponseTimeoutSeconds": 900}} {
		settings, err := ec.unmarshalInputChannelSettingsInput(t.Context(), input)
		require.NoError(t, err)
		if input["streamFirstEventTimeoutSeconds"] == nil {
			require.Nil(t, settings.StreamFirstEventTimeoutSeconds)
		} else {
			require.Equal(t, 0, *settings.StreamFirstEventTimeoutSeconds)
			require.Equal(t, 900, *settings.NonStreamResponseTimeoutSeconds)
		}
	}
}
