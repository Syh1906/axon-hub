package transformer

import (
	"fmt"

	"github.com/looplj/axonhub/llm"
)

func ValidateRequestConversion(req *llm.Request, target llm.APIFormat) error {
	if feature := llm.UnsupportedRequestFeature(req, target); feature != "" {
		return fmt.Errorf("%w: %w: %s cannot be converted to %s without losing required data", ErrUnsupportedConversion, ErrInvalidRequest, feature, target)
	}
	return nil
}
