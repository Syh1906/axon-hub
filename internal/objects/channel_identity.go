package objects

import (
	"fmt"
	"net/url"
)

// ChannelProvider uses the same display groups as the channel editor. Protocol
// variants belong to one provider; subscription products remain separate.
func ChannelProvider(channelType string) string {
	switch channelType {
	case "openai_responses":
		return "openai"
	case "gemini_vertex", "gemini_openai":
		return "gemini"
	case "anthropic_aws", "anthropic_gcp":
		return "anthropic"
	case "moonshot_anthropic", "moonshot_coding":
		return "moonshot"
	case "zenmux_responses", "zenmux_anthropic", "zenmux_gemini":
		return "zenmux"
	case "xai_responses":
		return "xai"
	case "nanogpt_responses":
		return "nanogpt"
	case "qiniu_anthropic", "deepseek_anthropic", "zhipu_anthropic", "zai_anthropic",
		"doubao_anthropic", "longcat_anthropic", "minimax_anthropic", "volcengine_anthropic",
		"aihubmix_anthropic", "bailian_anthropic", "xiaomi_anthropic", "opencode_go_anthropic",
		"ollama_anthropic", "evolink_anthropic", "commandcode_anthropic":
		return channelType[:len(channelType)-len("_anthropic")]
	default:
		return channelType
	}
}

// ValidateChannelWebsite keeps navigable website links separate from API URLs.
func ValidateChannelWebsite(value string) error {
	if value == "" {
		return nil
	}
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("website URL must be an HTTP or HTTPS URL without credentials")
	}
	return nil
}
