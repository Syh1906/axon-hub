package biz

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
	xaisubscription "github.com/looplj/axonhub/llm/transformer/xai/subscription"
)

func parseChannelAddress(raw string) (*url.URL, error) {
	value, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || value == nil || value.Host == "" || (value.Scheme != "http" && value.Scheme != "https") || value.User != nil || value.RawQuery != "" || value.Fragment != "" {
		return nil, fmt.Errorf("upstream address must be an HTTP(S) URL without credentials, query or fragment")
	}
	return value, nil
}

func validateChannelAddresses(settings *objects.ChannelSettings, channelType channel.Type) error {
	if settings == nil || len(settings.UpstreamAddresses) == 0 {
		return nil
	}
	ids, urls := make(map[string]bool), make(map[string]bool)
	enabled := 0
	for i := range settings.UpstreamAddresses {
		address := &settings.UpstreamAddresses[i]
		address.URL, address.Name = strings.TrimRight(strings.TrimSpace(address.URL), "/"), strings.TrimSpace(address.Name)
		if _, err := parseChannelAddress(address.URL); err != nil {
			return err
		}
		if address.ID == "" || address.Name == "" || ids[address.ID] || urls[address.URL] {
			return fmt.Errorf("upstream addresses require unique IDs and URLs and a name")
		}
		if channelType == channel.TypeXaiSubscription && address.URL != xaisubscription.DefaultBaseURL {
			return fmt.Errorf("this channel uses its fixed upstream address")
		}
		if isCommandCodeChannelType(channelType) {
			if err := validateCommandCodeBaseURL(address.URL); err != nil {
				return err
			}
		}
		ids[address.ID], urls[address.URL] = true, true
		if address.Enabled {
			enabled++
		}
	}
	if enabled == 0 {
		return fmt.Errorf("at least one upstream address must remain enabled")
	}
	return nil
}

func (c *Channel) EnabledUpstreamAddresses(apiFormat string) []objects.ChannelUpstreamAddress {
	if c == nil || c.Settings == nil {
		return nil
	}
	// 显式接口专用地址继续只作用于该接口，不能被渠道地址列表覆盖。
	for _, endpoint := range c.Endpoints {
		if endpoint.APIFormat == apiFormat && endpoint.BaseURL != "" {
			return nil
		}
	}
	addresses := make([]objects.ChannelUpstreamAddress, 0, len(c.Settings.UpstreamAddresses))
	for _, address := range c.Settings.UpstreamAddresses {
		if address.Enabled {
			addresses = append(addresses, address)
		}
	}
	slices.SortStableFunc(addresses, func(a, b objects.ChannelUpstreamAddress) int {
		if a.Priority < b.Priority {
			return -1
		}
		if a.Priority > b.Priority {
			return 1
		}
		return 0
	})
	return addresses
}

// 地址切换复用 transformer 的版本补全，避免主站无 /v1 而备用站带 /v1 时重复拼接。
func (c *Channel) NormalizeUpstreamAddress(raw, apiFormat string) string {
	version := ""
	if strings.HasPrefix(apiFormat, "openai/") || apiFormat == "anthropic/messages" {
		version = "v1"
	}
	for _, endpoint := range c.Endpoints {
		if endpoint.APIFormat == apiFormat && endpoint.Path != "" {
			version = ""
			break
		}
	}
	return transformer.NormalizeBaseURL(raw, version)
}

func (svc *ChannelService) ProbeChannelAddress(ctx context.Context, rawURL string, channelID *int) (*objects.ChannelAddressProbe, error) {
	if err := authz.RequireScope(ctx, scopes.ScopeWriteChannels); err != nil {
		return nil, err
	}
	address, err := parseChannelAddress(rawURL)
	if err != nil {
		return nil, err
	}
	client := svc.httpClient
	if channelID != nil {
		ch, err := svc.upstreamChannel(ctx, *channelID, scopes.ScopeWriteChannels)
		if err != nil {
			return nil, err
		}
		client = svc.getHttpClient(ch.Settings)
	}
	queryCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	started := time.Now()
	response, err := client.WithRejectHTTPSDowngrade().Do(queryCtx, httpclient.NewRequestBuilder().WithMethod(http.MethodHead).WithURL(address.String()).Build())
	result := &objects.ChannelAddressProbe{LatencyMs: int(time.Since(started).Milliseconds())}
	if err == nil {
		result.Reachable, result.StatusCode = true, response.StatusCode
		return result, nil
	}
	if responseError, ok := errors.AsType[*httpclient.Error](err); ok {
		// 401/404/405 也证明地址可达；这里测连通而非发送模型生成请求。
		result.Reachable, result.StatusCode = true, responseError.StatusCode
	} else if errors.Is(queryCtx.Err(), context.DeadlineExceeded) {
		result.ErrorCode = "TIMEOUT"
	} else {
		result.ErrorCode = "NETWORK_ERROR"
	}
	return result, nil
}
