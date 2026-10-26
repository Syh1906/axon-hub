package xversion

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseComparableVersionOrdersNumericPrereleases(t *testing.T) {
	ordered := []string{
		"v0.4.0",
		"v1.0.0-beta6",
		"v1.0.0-beta7",
		"v1.0.0-beta7-fork.1",
		"v1.0.0-beta7-fork.5",
		"v1.0.0-beta8",
		"v1.0.0-beta9",
		"v1.0.0-beta9-unstable.20260720",
		"v1.0.0-beta10",
		"v1.0.0-beta10-fork.1",
		"v1.0.0-beta10-fork.2",
		"v1.0.0-beta10-fork.7",
		"v1.0.0-beta10-fork.7.syh.1",
		"v1.0.0-beta10-fork.7.syh.9",
		"v1.0.0-beta10-fork.7.syh.10",
		"v1.0.0-beta10-fork.8",
		"v1.0.0-beta10-fork.8.syh.1",
		"v1.0.0-beta11",
		"v1.0.0-rc1",
		"v1.0.0",
	}

	for i := 1; i < len(ordered); i++ {
		previous, err := Parse(ordered[i-1])
		require.NoError(t, err)
		next, err := Parse(ordered[i])
		require.NoError(t, err)
		require.Truef(t, previous.LessThan(next),
			"%s should sort before %s", ordered[i-1], ordered[i])
		require.Falsef(t, next.LessThan(previous),
			"%s should not sort before %s", ordered[i], ordered[i-1])
	}
}

func TestParseComparableVersionKeepsEqualVersionsEqual(t *testing.T) {
	first, err := Parse("v1.0.0-beta10")
	require.NoError(t, err)
	second, err := Parse("v1.0.0-beta10")
	require.NoError(t, err)
	require.True(t, first.Equal(second))
}

func TestParseComparableVersionRejectsInvalidInput(t *testing.T) {
	_, err := Parse("not-a-version")
	require.Error(t, err)
}
