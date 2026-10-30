package objects

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestChannelWebsiteURL(t *testing.T) {
	for _, value := range []string{"", "https://example.com/account?tab=usage", "http://localhost:8090/portal"} {
		require.NoError(t, ValidateChannelWebsite(value))
	}
	for _, value := range []string{"javascript:alert(1)", "//example.com", "https://user:password@example.com", "https:///missing"} {
		require.Error(t, ValidateChannelWebsite(value))
	}
}
