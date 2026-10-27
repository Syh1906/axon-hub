package objects

import (
	"fmt"
	"io"
	"strings"

	"github.com/looplj/axonhub/internal/pkg/xregexp"
)

// ModelMatchType describes how a configured model accepts a request model ID.
type ModelMatchType string

const (
	ModelMatchExact  ModelMatchType = "exact"
	ModelMatchPrefix ModelMatchType = "prefix"
	ModelMatchSuffix ModelMatchType = "suffix"
	ModelMatchRegex  ModelMatchType = "regex"
)

func (ModelMatchType) Values() []string {
	return []string{"exact", "prefix", "suffix", "regex"}
}

func (m ModelMatchType) String() string { return string(m) }

func (m ModelMatchType) MarshalGQL(w io.Writer) {
	fmt.Fprintf(w, "%q", m)
}

func (m *ModelMatchType) UnmarshalGQL(value any) error {
	text, ok := value.(string)
	if ok {
		switch ModelMatchType(text) {
		case ModelMatchExact, ModelMatchPrefix, ModelMatchSuffix, ModelMatchRegex:
			*m = ModelMatchType(text)
			return nil
		}
	}
	return fmt.Errorf("invalid model match type %v", value)
}

// MatchModelID is shared by routing, public model validation and the editor's
// unsaved-rule preview. Prefix and suffix patterns are literal strings.
func MatchModelID(matchType ModelMatchType, pattern, modelID string) (bool, error) {
	if err := ValidateModelMatch(matchType, pattern); err != nil {
		return false, err
	}
	switch matchType {
	case "", ModelMatchExact:
		return modelID == pattern, nil
	case ModelMatchPrefix:
		return strings.HasPrefix(modelID, pattern), nil
	case ModelMatchSuffix:
		return strings.HasSuffix(modelID, pattern), nil
	default:
		return xregexp.MatchString(pattern, modelID), nil
	}
}

func ValidateModelMatch(matchType ModelMatchType, pattern string) error {
	if pattern == "" {
		return fmt.Errorf("model ID matching rule must not be empty")
	}
	switch matchType {
	case "", ModelMatchExact, ModelMatchPrefix, ModelMatchSuffix:
		return nil
	case ModelMatchRegex:
		return xregexp.ValidateRegex(pattern)
	default:
		return fmt.Errorf("invalid model match type %q", matchType)
	}
}

type ModelMatchTest struct {
	Matched bool    `json:"matched"`
	Error   *string `json:"error,omitempty"`
}
