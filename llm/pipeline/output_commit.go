package pipeline

import "github.com/looplj/axonhub/llm"

func hasResponseOutput(response *llm.Response) bool {
	if response == nil {
		return false
	}
	for _, choice := range response.Choices {
		for _, message := range []*llm.Message{choice.Message, choice.Delta} {
			if message == nil {
				continue
			}
			output := *message
			output.Reasoning, output.ReasoningContent, output.ReasoningSignature = nil, nil, nil
			if hasMessageContent(&output) {
				return true
			}
		}
	}
	return false
}
