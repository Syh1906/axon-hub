package responses

import (
	"github.com/samber/lo"

	"github.com/looplj/axonhub/llm"
)

const encryptedFunctionArgsMetadataKey = "openai_responses_encrypted_function_args"

// Keep Responses-only metadata out of the common function-call wire format.
func encryptedFunctionArgsMetadata(args *[]string) map[string]any {
	if args == nil {
		return nil
	}
	return map[string]any{encryptedFunctionArgsMetadataKey: append([]string{}, (*args)...)}
}

func encryptedFunctionArgs(tc llm.ToolCall) *[]string {
	value, exists := tc.TransformerMetadata[encryptedFunctionArgsMetadataKey]
	if !exists || value == nil {
		return nil
	}
	switch args := value.(type) {
	case []string:
		return lo.ToPtr(append([]string{}, args...))
	case []any: // Metadata may have passed through JSON aggregation/storage.
		result := make([]string, 0, len(args))
		for _, arg := range args {
			name, ok := arg.(string)
			if !ok {
				return nil
			}
			result = append(result, name)
		}
		return &result
	default:
		return nil
	}
}

func retainEncryptedFunctionArgs(tc *llm.ToolCall, args *[]string) {
	if args == nil {
		return
	}
	if tc.TransformerMetadata == nil {
		tc.TransformerMetadata = make(map[string]any)
	}
	tc.TransformerMetadata[encryptedFunctionArgsMetadataKey] = append([]string{}, (*args)...)
}
