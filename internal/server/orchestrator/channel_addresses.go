package orchestrator

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"strings"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
)

func (p *PersistentOutboundTransformer) upstreamAddresses() []objects.ChannelUpstreamAddress {
	if p.continuationAddresses != nil {
		return p.continuationAddresses
	}
	if p.state.CurrentCandidate == nil {
		return nil
	}
	return p.state.CurrentCandidate.Channel.EnabledUpstreamAddresses(p.state.CurrentCandidate.APIFormat)
}

func isUpstreamAddressFailure(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	if status := ExtractStatusCodeFromError(err); status != 0 {
		return status >= 500 && status <= 599
	}
	var networkError net.Error
	return errors.As(err, &networkError) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, pipeline.ErrStreamFirstEventTimeout) || errors.Is(err, pipeline.ErrNonStreamResponseTimeout) || errors.Is(err, pipeline.ErrRequestUploadTimeout)
}

// CanRetryAfterTimeout only retries when a different configured address remains.
// The pipeline keeps its ordinary channel retry budget and pre-commit boundary.
func (p *PersistentOutboundTransformer) CanRetryAfterTimeout(err error) bool {
	p.retryAddress = false
	if p.state.CurrentCandidate == nil || !isUpstreamAddressFailure(err) {
		return false
	}
	candidate := p.state.CurrentCandidate
	if candidate.continuationStrict && candidate.continuationUpstreamRef != "" {
		return false
	}
	if candidate.healthGate != nil && candidate.healthGate.lastResort || candidate.Channel.Settings == nil || !candidate.Channel.Settings.AddressFailover {
		return false
	}
	p.retryAddress = p.addressIndex+1 < len(p.upstreamAddresses())
	return p.retryAddress
}

func applyUpstreamAddress(p *PersistentOutboundTransformer) pipeline.Middleware {
	return pipeline.OnRawRequest("upstream_address", func(_ context.Context, request *httpclient.Request) (*httpclient.Request, error) {
		addresses := p.upstreamAddresses()
		if len(addresses) == 0 {
			return request, nil
		}
		if p.addressIndex >= len(addresses) {
			return request, nil
		}
		channel := p.GetCurrentChannel()
		format := p.state.CurrentCandidate.APIFormat
		base, err := url.Parse(channel.NormalizeUpstreamAddress(channel.BaseURL, format))
		if err != nil {
			return nil, err
		}
		target, err := url.Parse(channel.NormalizeUpstreamAddress(addresses[p.addressIndex].URL, format))
		if err != nil {
			return nil, err
		}
		current, err := url.Parse(request.URL)
		if err != nil {
			return nil, err
		}
		// 专用服务和接口覆盖地址不属于本地址列表。
		if current.Host != base.Host {
			return request, nil
		}
		basePath := strings.TrimRight(base.Path, "/")
		if current.Path != basePath && !strings.HasPrefix(current.Path, basePath+"/") {
			return request, nil
		}
		current.Scheme, current.Host = target.Scheme, target.Host
		current.Path = strings.TrimRight(target.Path, "/") + strings.TrimPrefix(current.Path, basePath)
		current.RawPath = ""
		if origin := p.state.CurrentCandidate.continuationUpstreamRef; origin != "" {
			// Prefer the actual origin even when address priorities changed.
			for index, address := range addresses {
				endpoint, err := url.Parse(channel.NormalizeUpstreamAddress(address.URL, format))
				if err != nil {
					return nil, err
				}
				candidate := *current
				candidate.Scheme, candidate.Host = endpoint.Scheme, endpoint.Host
				original, _ := url.Parse(request.URL)
				candidate.Path = strings.TrimRight(endpoint.Path, "/") + strings.TrimPrefix(original.Path, basePath)
				if biz.ResponsesUpstreamRef(candidate.String(), request.Headers) == origin {
					current = &candidate
					p.addressIndex = index
					if !p.state.CurrentCandidate.continuationStrict && p.continuationAddresses == nil {
						// Keep the original source first, then every other configured
						// address in priority order, including those before the source.
						p.continuationAddresses = append([]objects.ChannelUpstreamAddress{address}, addresses[:index]...)
						p.continuationAddresses = append(p.continuationAddresses, addresses[index+1:]...)
						p.addressIndex = 0
					}
					break
				}
			}
		}
		copy := *request
		copy.URL = current.String()
		return &copy, nil
	})
}
