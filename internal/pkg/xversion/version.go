package xversion

import (
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// numericSuffixIdentifier matches a prerelease identifier such as "beta10" or
// "beta7-fork": an alphabetic word, a number, and an optional hyphenated tail.
var numericSuffixIdentifier = regexp.MustCompile(`^([A-Za-z]+)(\d+)(?:-(.+))?$`)

// Parse parses v for ordering only. Upstream names
// pre-releases like v1.0.0-beta10, and semver compares "beta10" with "beta9"
// as plain strings, which sorts beta10 first. Splitting the number into its
// own identifier (beta.10) restores numeric order; fork suffixes such as
// beta7-fork.5 become beta.7.fork.5 so they stay between beta7 and beta8.
// The returned value must not be persisted or displayed.
func Parse(v string) (*semver.Version, error) {
	parsed, err := semver.NewVersion(v)
	if err != nil {
		return nil, err
	}

	if parsed.Prerelease() == "" {
		return parsed, nil
	}

	var identifiers []string

	for _, identifier := range strings.Split(parsed.Prerelease(), ".") {
		match := numericSuffixIdentifier.FindStringSubmatch(identifier)
		if match == nil {
			identifiers = append(identifiers, identifier)
			continue
		}

		identifiers = append(identifiers, match[1], match[2])
		if match[3] != "" {
			identifiers = append(identifiers, match[3])
		}
	}

	comparable, err := parsed.SetPrerelease(strings.Join(identifiers, "."))
	if err != nil {
		return nil, err
	}

	return &comparable, nil
}
