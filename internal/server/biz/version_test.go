package biz

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{
			name:    "latest version is newer - major version",
			current: "v1.0.0",
			latest:  "v2.0.0",
			want:    true,
		},
		{
			name:    "latest version is newer - minor version",
			current: "v1.0.0",
			latest:  "v1.1.0",
			want:    true,
		},
		{
			name:    "latest version is newer - patch version",
			current: "v1.0.0",
			latest:  "v1.0.1",
			want:    true,
		},
		{
			name:    "latest version is same",
			current: "v1.0.0",
			latest:  "v1.0.0",
			want:    false,
		},
		{
			name:    "latest version is older",
			current: "v2.0.0",
			latest:  "v1.0.0",
			want:    false,
		},
		{
			name:    "versions without v prefix - latest newer",
			current: "1.0.0",
			latest:  "1.1.0",
			want:    true,
		},
		{
			name:    "mixed v prefix - current has v, latest doesn't",
			current: "v1.0.0",
			latest:  "1.1.0",
			want:    true,
		},
		{
			name:    "mixed v prefix - latest has v, current doesn't",
			current: "1.0.0",
			latest:  "v1.1.0",
			want:    true,
		},
		{
			name:    "complex version comparison",
			current: "v1.2.3",
			latest:  "v2.0.0",
			want:    true,
		},
		{
			name:    "same major, higher minor",
			current: "v1.5.0",
			latest:  "v1.6.0",
			want:    true,
		},
		{
			name:    "same major and minor, higher patch",
			current: "v1.5.2",
			latest:  "v1.5.3",
			want:    true,
		},
		{
			name:    "invalid current version",
			current: "invalid",
			latest:  "v1.0.0",
			want:    false,
		},
		{
			name:    "invalid latest version",
			current: "v1.0.0",
			latest:  "invalid",
			want:    false,
		},
		{
			name:    "both invalid versions",
			current: "invalid",
			latest:  "invalid",
			want:    false,
		},
		{
			name:    "empty current version",
			current: "",
			latest:  "v1.0.0",
			want:    false,
		},
		{
			name:    "empty latest version",
			current: "v1.0.0",
			latest:  "",
			want:    false,
		},
		{
			name:    "both empty versions",
			current: "",
			latest:  "",
			want:    false,
		},
		{
			name:    "prerelease versions - current is prerelease",
			current: "v1.0.0-beta",
			latest:  "v1.0.0",
			want:    true,
		},
		{
			name:    "prerelease versions - latest is prerelease",
			current: "v1.0.0",
			latest:  "v1.0.1-beta",
			want:    true,
		},
		{
			name:    "beta sequence is newer",
			current: "v1.0.0-beta5",
			latest:  "v1.0.0-beta6",
			want:    true,
		},
		{
			name:    "build metadata",
			current: "v1.0.0+build.1",
			latest:  "v1.0.0+build.2",
			want:    false, // build metadata doesn't affect version comparison
		},
		{
			name:    "numeric beta ordering matches data migrations",
			current: "v1.0.0-beta9-fork.7",
			latest:  "v1.0.0-beta10-fork.8",
			want:    true,
		},
		{
			name:    "fork numbers compare numerically",
			current: "v1.0.0-beta10-fork.9",
			latest:  "v1.0.0-beta10-fork.10",
			want:    true,
		},
		{
			name:    "own releases stay below the next direct upstream baseline",
			current: "v1.0.0-beta10-fork.7.syh.10",
			latest:  "v1.0.0-beta10-fork.8.syh.1",
			want:    true,
		},
		{
			name:    "version with many digits",
			current: "v1.2.3",
			latest:  "v1.2.3.4",
			want:    false, // semver only supports 3-part versions
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsNewerVersion(tt.current, tt.latest)
			require.Equal(t, tt.want, got, "IsNewerVersion(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		})
	}
}

func TestIsAxonHubTag(t *testing.T) {
	tests := []struct {
		name string
		tag  string
		want bool
	}{
		{
			name: "standard axonhub tag",
			tag:  "v1.0.0",
			want: true,
		},
		{
			name: "axonhub prerelease tag",
			tag:  "v1.0.0-beta",
			want: true,
		},
		{
			name: "axonclaw prefixed tag",
			tag:  "axonclaw/v1.0.0",
			want: false,
		},
		{
			name: "other service prefixed tag",
			tag:  "other-service/v2.0.0",
			want: false,
		},
		{
			name: "empty tag",
			tag:  "",
			want: false,
		},
		{
			name: "non-version tag",
			tag:  "release-2024",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAxonHubTag(tt.tag)
			require.Equal(t, tt.want, got, "isAxonHubTag(%q) = %v, want %v", tt.tag, got, tt.want)
		})
	}
}

func TestSelectLatestGitHubRelease(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	t.Run("fork releases use the same numeric beta order as migrations", func(t *testing.T) {
		got, err := selectLatestGitHubRelease([]GitHubRelease{
			{TagName: "v1.0.0-beta9-fork.7", Prerelease: true, PublishedAt: now.Add(-time.Hour)},
			{TagName: "v1.0.0-beta10-fork.8", Prerelease: true, PublishedAt: now.Add(-time.Hour)},
		}, true, now)
		require.NoError(t, err)
		require.Equal(t, "v1.0.0-beta10-fork.8", got)
	})
	t.Run("stable own release remains eligible despite beta provenance", func(t *testing.T) {
		got, err := selectLatestGitHubRelease([]GitHubRelease{
			{TagName: "v1.0.0-beta10-fork.7.syh.1", PublishedAt: now.Add(-time.Hour)},
		}, false, now)
		require.NoError(t, err)
		require.Equal(t, "v1.0.0-beta10-fork.7.syh.1", got)
	})
	t.Run("GitHub prerelease is excluded even without a beta tag", func(t *testing.T) {
		_, err := selectLatestGitHubRelease([]GitHubRelease{
			{TagName: "v1.0.1-syh.1", Prerelease: true, PublishedAt: now.Add(-time.Hour)},
		}, false, now)
		require.Error(t, err)
	})
	releases := []GitHubRelease{
		{TagName: "v1.0.0-beta6", Prerelease: true, PublishedAt: now.Add(-time.Hour)},
		{TagName: "v1.0.0-rc1", Prerelease: true, PublishedAt: now.Add(-2 * time.Hour)},
		{TagName: "v0.9.43", PublishedAt: now.Add(-3 * time.Hour)},
	}

	t.Run("stable check skips beta releases", func(t *testing.T) {
		got, err := selectLatestGitHubRelease(releases, false, now)
		require.NoError(t, err)
		require.Equal(t, "v0.9.43", got)
	})

	t.Run("prerelease opt-in selects the highest eligible prerelease", func(t *testing.T) {
		got, err := selectLatestGitHubRelease(releases, true, now)
		require.NoError(t, err)
		require.Equal(t, "v1.0.0-rc1", got)
	})

	t.Run("selects higher stable release after lower beta release", func(t *testing.T) {
		got, err := selectLatestGitHubRelease([]GitHubRelease{
			{TagName: "v1.9.0-beta1", PublishedAt: now.Add(-time.Hour)},
			{TagName: "v2.0.0", PublishedAt: now.Add(-2 * time.Hour)},
		}, true, now)
		require.NoError(t, err)
		require.Equal(t, "v2.0.0", got)
	})

	t.Run("selects highest eligible stable release", func(t *testing.T) {
		got, err := selectLatestGitHubRelease([]GitHubRelease{
			{TagName: "v0.9.42", PublishedAt: now.Add(-time.Hour)},
			{TagName: "v0.9.43", PublishedAt: now.Add(-2 * time.Hour)},
		}, false, now)
		require.NoError(t, err)
		require.Equal(t, "v0.9.43", got)
	})

	t.Run("prerelease opt-in follows GitHub metadata for rc tags", func(t *testing.T) {
		got, err := selectLatestGitHubRelease([]GitHubRelease{
			{TagName: "v1.0.0-rc1", Prerelease: true, PublishedAt: now.Add(-time.Hour)},
			{TagName: "v0.9.43", PublishedAt: now.Add(-2 * time.Hour)},
		}, true, now)
		require.NoError(t, err)
		require.Equal(t, "v1.0.0-rc1", got)
	})

	t.Run("skips drafts recent releases and other service tags", func(t *testing.T) {
		got, err := selectLatestGitHubRelease([]GitHubRelease{
			{TagName: "v1.0.0-beta6", Draft: true, PublishedAt: now.Add(-time.Hour)},
			{TagName: "v1.0.0-beta5", PublishedAt: now.Add(-releaseCooldownDuration / 2)},
			{TagName: "axonclaw/v2.0.0", PublishedAt: now.Add(-time.Hour)},
			{TagName: "v1.0.0-beta4", Prerelease: true, PublishedAt: now.Add(-2 * time.Hour)},
		}, true, now)
		require.NoError(t, err)
		require.Equal(t, "v1.0.0-beta4", got)
	})

	t.Run("returns an error when no release is eligible", func(t *testing.T) {
		_, err := selectLatestGitHubRelease([]GitHubRelease{
			{TagName: "v1.0.0-beta6", Prerelease: true, PublishedAt: now.Add(-time.Hour)},
		}, false, now)
		require.EqualError(t, err, "no eligible release found")
	})
}
