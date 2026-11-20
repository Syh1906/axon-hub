package biz

import (
	"net/url"
	"strings"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/transformer/gemini"
)

// GeminiCacheBase selects the first eligible address for a new resource, or
// the still-enabled creating address for an existing one, regardless of rank.
func (ch *Channel) GeminiCacheBase(owner string) string {
	format := string(llm.APIFormatGeminiContents)
	for _, ep := range ch.ResolveEndpoints() {
		if ep.APIFormat != format || ep.Path != "" {
			continue
		}
		if addresses := ch.EnabledUpstreamAddresses(format); len(addresses) > 0 {
			for _, address := range addresses {
				base := GeminiCacheAPIBase(address.URL)
				if base != "" && (owner == "" || owner == base) {
					return base
				}
			}
			return ""
		}
		base := ep.BaseURL
		if base == "" {
			base = ch.BaseURL
		}
		base = GeminiCacheAPIBase(base)
		if owner == "" || owner == base {
			return base
		}
	}
	return ""
}

func GeminiCacheAPIBase(raw string) string {
	if raw == "" {
		raw = gemini.DefaultBaseURL
	}
	raw = strings.TrimRight(raw, "/")
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.HasSuffix(raw, "/v1") {
		return ""
	}
	return strings.TrimSuffix(raw, "/v1beta") + "/v1beta"
}
