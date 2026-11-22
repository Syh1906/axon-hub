package biz

import (
	"context"
	"fmt"
	"strconv"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/llm/httpclient"
)

const (
	SystemKeyUpstreamProtocol    = "upstream_http_protocol"
	SystemKeyUpstreamCompression = "upstream_automatic_compression"
)

type UpstreamTransportSettings struct {
	Protocol             httpclient.ProtocolPreference
	AutomaticCompression bool
}

func DefaultUpstreamTransportSettings() UpstreamTransportSettings {
	return UpstreamTransportSettings{Protocol: httpclient.ProtocolAuto, AutomaticCompression: true}
}

func (s *SystemService) UpstreamTransportSettings(ctx context.Context) (UpstreamTransportSettings, error) {
	settings := DefaultUpstreamTransportSettings()
	value, err := s.getSystemValue(ctx, SystemKeyUpstreamProtocol)
	if err == nil {
		mode := httpclient.ProtocolPreference(value)
		if !mode.Valid() {
			return settings, fmt.Errorf("invalid upstream HTTP protocol %q", value)
		}
		settings.Protocol = mode
	} else if !ent.IsNotFound(err) {
		return settings, err
	}
	value, err = s.getSystemValue(ctx, SystemKeyUpstreamCompression)
	if ent.IsNotFound(err) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return settings, fmt.Errorf("invalid upstream compression setting: %w", err)
	}
	settings.AutomaticCompression = enabled
	return settings, nil
}

func (s *SystemService) SetUpstreamHTTPProtocol(ctx context.Context, mode httpclient.ProtocolPreference) error {
	if !mode.Valid() {
		return fmt.Errorf("invalid upstream HTTP protocol %q", mode)
	}
	return s.setSystemValue(ctx, SystemKeyUpstreamProtocol, string(mode))
}

func (s *SystemService) SetUpstreamAutomaticCompression(ctx context.Context, enabled bool) error {
	return s.setSystemValue(ctx, SystemKeyUpstreamCompression, strconv.FormatBool(enabled))
}
