package orchestrator

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

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
	candidate := p.state.CurrentCandidate
	addresses := candidate.Channel.EnabledUpstreamAddresses(candidate.APIFormat)
	if candidate.geminiCacheBase != "" {
		for i, address := range addresses {
			if biz.GeminiCacheAPIBase(address.URL) == candidate.geminiCacheBase {
				return addresses[i : i+1]
			}
		}
		return nil
	}
	return addresses
}

// Native HTTP has separate upload and first-response allowances. A secondary
// address inherits the absolute end of the original pair of allowances.
func (p *PersistentOutboundTransformer) applyAddressDeadline(ctx context.Context, request *httpclient.Request) {
	if p.state.CurrentCandidate == nil {
		return
	}
	ch := p.GetCurrentChannel()
	if ch.Settings == nil || !ch.Settings.AddressFailover || len(p.upstreamAddresses()) < 2 {
		return
	}
	if p.addressDeadlineChannel != ch.ID || p.addressIndex == 0 {
		p.addressDeadlineChannel = ch.ID
		p.addressDeadline, _ = ctx.Deadline()
		first, nonStream := channelResponseTimeouts(&p.state.settings().RetryPolicy, ch)
		phase := nonStream
		if p.state.OriginalRequestStream != nil && *p.state.OriginalRequestStream {
			phase = first
		}
		if phase > 0 {
			end := time.Now().Add(phase).Add(phase)
			if p.addressDeadline.IsZero() || end.Before(p.addressDeadline) {
				p.addressDeadline = end
			}
		}
	}
	request.StartDeadline = p.addressDeadline
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
	return pipeline.OnRawRequest("upstream_address", func(ctx context.Context, request *httpclient.Request) (*httpclient.Request, error) {
		addresses := p.upstreamAddresses()
		if len(addresses) == 0 {
			return request, nil
		}
		if p.addressIndex >= len(addresses) {
			return request, nil
		}
		channel := p.GetCurrentChannel()
		format := p.state.CurrentCandidate.APIFormat
		baseURL := channel.NormalizeUpstreamAddress(channel.BaseURL, format)
		if p.state.CurrentCandidate.geminiCacheBase != "" {
			baseURL = biz.GeminiCacheAPIBase(channel.BaseURL)
		}
		route, err := biz.MatchUpstreamAddress(request.URL, baseURL)
		if err != nil {
			return nil, err
		}
		if route == nil {
			return request, nil
		}
		addresses = p.selectContentAddress(ctx, request, addresses)
		targetURL := channel.NormalizeUpstreamAddress(addresses[p.addressIndex].URL, format)
		if owner := p.state.CurrentCandidate.geminiCacheBase; owner != "" {
			targetURL = owner
		}
		selectedURL, err := route.URLAt(targetURL)
		if err != nil {
			return nil, err
		}
		if origin := p.state.CurrentCandidate.continuationUpstreamRef; origin != "" {
			// Prefer the actual origin even when address priorities changed.
			for index, address := range addresses {
				candidateURL, err := route.URLAt(channel.NormalizeUpstreamAddress(address.URL, format))
				if err != nil {
					return nil, err
				}
				if biz.ResponsesUpstreamRef(candidateURL, request.Headers) == origin {
					selectedURL = candidateURL
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
		copy.URL = selectedURL
		if p.state.contentAddressOwner != "" {
			// Retry order may be rotated. Persist the actual successful address
			// against the original, versioned configuration.
			p.state.contentAddressPosition = 0
			for i, configured := range channel.EnabledUpstreamAddresses(format) {
				if configured.Priority != addresses[0].Priority {
					break
				}
				if configured.URL == addresses[p.addressIndex].URL {
					p.state.contentAddressPosition = i + 1
					break
				}
			}
		}
		return &copy, nil
	})
}
