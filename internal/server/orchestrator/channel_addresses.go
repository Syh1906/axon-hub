package orchestrator

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"strings"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
)

func (p *PersistentOutboundTransformer) upstreamAddresses() []objects.ChannelUpstreamAddress {
	if p.state.CurrentCandidate == nil {
		return nil
	}
	return p.state.CurrentCandidate.Channel.EnabledUpstreamAddresses(p.state.CurrentCandidate.APIFormat)
}

func isUpstreamAddressFailure(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	if response, ok := errors.AsType[*httpclient.Error](err); ok {
		return response.StatusCode >= 500 && response.StatusCode <= 599
	}
	var networkError net.Error
	return errors.As(err, &networkError) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, pipeline.ErrStreamFirstEventTimeout) || errors.Is(err, pipeline.ErrNonStreamResponseTimeout)
}

// CanRetryAfterTimeout only retries when a different configured address remains.
// The pipeline keeps its ordinary channel retry budget and pre-commit boundary.
func (p *PersistentOutboundTransformer) CanRetryAfterTimeout(err error) bool {
	p.retryAddress = false
	if p.state.CurrentCandidate == nil || !isUpstreamAddressFailure(err) {
		return false
	}
	candidate := p.state.CurrentCandidate
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
		copy := *request
		copy.URL = current.String()
		return &copy, nil
	})
}
