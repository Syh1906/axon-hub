package biz

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/looplj/axonhub/llm/httpclient"
)

// Store new-channel defaults explicitly. Existing channels and their duplicates
// retain the routing and pricing policy they were created with.
func newChannelSettings(settings *objects.ChannelSettings) *objects.ChannelSettings {
	var result objects.ChannelSettings
	if settings != nil {
		result = *settings
	}
	result.ResponsesTierRoutingEnabled = true
	if result.ResponsesServiceTierOverride == objects.ResponsesServiceTierInherit {
		result.ResponsesServiceTierOverride = objects.ResponsesServiceTierSupported
	}
	if result.ResponsesTierSupport == nil {
		result.ResponsesTierSupport = &objects.ResponsesTierSupport{AllowDowngrade: true}
	}
	if result.ServiceTierBilling == nil {
		result.ServiceTierBilling = &objects.ServiceTierSettings{Enabled: true, Basis: objects.ServiceTierBasisUpstream}
	}
	return &result
}

// UpdateChannelSettingsInput patches a channel's settings. Only fields present
// in the request are applied: absent fields keep their stored values, so a
// caller can change one setting without resubmitting the whole object. Nested
// objects replace the stored value as a whole.
//
// Unlike updateChannel's settings input, which maps onto the stored struct and
// therefore cannot distinguish an absent field from a zero value, every field
// here is a pointer (or a slice, where nil means absent and an explicitly empty
// list means "clear"), so field presence survives the GraphQL boundary. This
// mirrors the merge the admin UI performs client-side
// (mergeChannelSettingsForUpdate) and makes those semantics available to
// direct GraphQL callers.
type UpdateChannelSettingsInput struct {
	ProtocolPolicy                  *objects.ChannelProtocolPolicy        `json:"protocolPolicy"`
	ResponsesServiceTierOverride    *objects.ResponsesServiceTierOverride `json:"responsesServiceTierOverride"`
	ServiceTierBilling              *objects.ServiceTierSettings          `json:"serviceTierBilling"`
	ResponsesTierSupport            *objects.ResponsesTierSupport         `json:"responsesTierSupport"`
	StreamFirstEventTimeoutSeconds  *int                                  `json:"streamFirstEventTimeoutSeconds"`
	NonStreamResponseTimeoutSeconds *int                                  `json:"nonStreamResponseTimeoutSeconds"`
	DisableSessionReuse             *bool                                 `json:"disableSessionReuse"`
	AllowImageGenerationTool        *bool                                 `json:"allowImageGenerationTool"`
	ExtraModelPrefix                *string                               `json:"extraModelPrefix"`
	ModelMappings                   []objects.ModelMapping                `json:"modelMappings"`
	AutoTrimedModelPrefixes         []string                              `json:"autoTrimedModelPrefixes"`
	HideOriginalModels              *bool                                 `json:"hideOriginalModels"`
	HideMappedModels                *bool                                 `json:"hideMappedModels"`
	LowercaseModelID                *bool                                 `json:"lowercaseModelId"`
	Proxy                           *httpclient.ProxyConfig               `json:"proxy"`
	TransformOptions                *objects.TransformOptions             `json:"transformOptions"`
	HeaderOverrideOperations        []objects.OverrideOperation           `json:"headerOverrideOperations"`
	BodyOverrideOperations          []objects.OverrideOperation           `json:"bodyOverrideOperations"`
	PassThroughUserAgent            *bool                                 `json:"passThroughUserAgent"`
	PassThroughBody                 *bool                                 `json:"passThroughBody"`
	RateLimit                       *objects.ChannelRateLimit             `json:"rateLimit"`
	RetryableStatusCodes            []int                                 `json:"retryableStatusCodes"`
	RetryableErrorPatterns          []objects.RetryableErrorPattern       `json:"retryableErrorPatterns"`
	ModelProtocols                  []objects.ModelProtocol               `json:"modelProtocols"`
	APIKeyStrategy                  *string                               `json:"apiKeyStrategy"`
	APIKeyRoundRobinSwitchAfter     *int                                  `json:"apiKeyRoundRobinSwitchAfter"`
	ProviderQuota                   *objects.ChannelProviderQuotaSettings `json:"providerQuota"`
	QuotaRoutingMode                *objects.QuotaRoutingMode             `json:"quotaRoutingMode"`
}

// Apply returns the settings that result from applying the patch to base.
// Fields absent from the patch keep the stored values. A nil receiver is a
// no-op patch, and a nil base starts from the zero settings.
func (i *UpdateChannelSettingsInput) Apply(base *objects.ChannelSettings) objects.ChannelSettings {
	merged := objects.ChannelSettings{}
	if base != nil {
		merged = *base
	}

	if i == nil {
		return merged
	}
	if i.ProtocolPolicy != nil {
		merged.ProtocolPolicy = i.ProtocolPolicy
	}
	if i.ResponsesServiceTierOverride != nil {
		if i.ServiceTierBilling != nil {
			merged.ResponsesTierRoutingEnabled = true
		}
		merged.ResponsesServiceTierOverride = *i.ResponsesServiceTierOverride
	}
	if i.ServiceTierBilling != nil {
		merged.ServiceTierBilling = i.ServiceTierBilling
	}
	if i.ResponsesTierSupport != nil {
		merged.ResponsesTierSupport = i.ResponsesTierSupport
	}
	if i.StreamFirstEventTimeoutSeconds != nil {
		merged.StreamFirstEventTimeoutSeconds = i.StreamFirstEventTimeoutSeconds
	}
	if i.NonStreamResponseTimeoutSeconds != nil {
		merged.NonStreamResponseTimeoutSeconds = i.NonStreamResponseTimeoutSeconds
	}
	if i.DisableSessionReuse != nil {
		merged.DisableSessionReuse = *i.DisableSessionReuse
	}
	if i.AllowImageGenerationTool != nil {
		merged.AllowImageGenerationTool = i.AllowImageGenerationTool
	}

	if i.ExtraModelPrefix != nil {
		merged.ExtraModelPrefix = *i.ExtraModelPrefix
	}
	if i.ModelMappings != nil {
		merged.ModelMappings = i.ModelMappings
	}
	if i.AutoTrimedModelPrefixes != nil {
		merged.AutoTrimedModelPrefixes = i.AutoTrimedModelPrefixes
	}
	if i.HideOriginalModels != nil {
		merged.HideOriginalModels = *i.HideOriginalModels
	}
	if i.HideMappedModels != nil {
		merged.HideMappedModels = *i.HideMappedModels
	}
	if i.LowercaseModelID != nil {
		merged.LowercaseModelID = *i.LowercaseModelID
	}
	if i.Proxy != nil {
		merged.Proxy = i.Proxy
	}
	if i.TransformOptions != nil {
		merged.TransformOptions = *i.TransformOptions
	}
	if i.HeaderOverrideOperations != nil {
		merged.HeaderOverrideOperations = i.HeaderOverrideOperations
	}
	if i.BodyOverrideOperations != nil {
		merged.BodyOverrideOperations = i.BodyOverrideOperations
	}
	if i.PassThroughUserAgent != nil {
		merged.PassThroughUserAgent = i.PassThroughUserAgent
	}
	if i.PassThroughBody != nil {
		merged.PassThroughBody = i.PassThroughBody
	}
	if i.RateLimit != nil {
		merged.RateLimit = i.RateLimit
	}
	if i.RetryableStatusCodes != nil {
		merged.RetryableStatusCodes = i.RetryableStatusCodes
	}
	if i.RetryableErrorPatterns != nil {
		merged.RetryableErrorPatterns = i.RetryableErrorPatterns
	}
	if i.ModelProtocols != nil {
		merged.ModelProtocols = i.ModelProtocols
	}
	if i.APIKeyStrategy != nil {
		merged.APIKeyStrategy = i.APIKeyStrategy
	}
	if i.APIKeyRoundRobinSwitchAfter != nil {
		merged.APIKeyRoundRobinSwitchAfter = i.APIKeyRoundRobinSwitchAfter
	}
	if i.ProviderQuota != nil {
		merged.ProviderQuota = i.ProviderQuota
	}
	if i.QuotaRoutingMode != nil {
		merged.QuotaRoutingMode = *i.QuotaRoutingMode
	}

	return merged
}

// channelExpectedVersionKey carries the updated-at version a guarded channel
// update must be optimistic-locked against. It is set by read-merge-write
// callers via withChannelExpectedVersion and consumed by UpdateChannel; it
// never crosses an API boundary.
type channelExpectedVersionKey struct{}

// withChannelExpectedVersion returns a context whose channel updates are
// optimistic-locked against the given version: UpdateChannel fails with
// ErrChannelUpdatedConcurrently when the stored version differs, so the
// caller can detect the conflict and re-merge.
func withChannelExpectedVersion(ctx context.Context, updatedAt time.Time) context.Context {
	return context.WithValue(ctx, channelExpectedVersionKey{}, &updatedAt)
}

// UpdateChannelSettings applies a partial settings patch to a channel and
// returns the updated channel. The patch is merged onto the stored settings
// and then flows through UpdateChannel, so it is subject to the same
// validation, normalization and optimistic locking as a full settings update.
//
// The merge is guarded by optimistic locking on the version the patch was
// merged against: when a concurrent update commits in between, the update
// fails and the merge is retried against the new snapshot, so two concurrent
// patches to different fields both survive instead of the last write silently
// erasing the other.
func (svc *ChannelService) UpdateChannelSettings(ctx context.Context, id int, input *UpdateChannelSettingsInput) (*ent.Channel, error) {
	// Each conflict round lets at least one patch commit, so a handful of
	// attempts resolves any realistic contention; the bound keeps a pathologically
	// contended channel from retrying forever.
	const maxMergeAttempts = 8

	var lastErr error
	for range maxMergeAttempts {
		// Read the stored settings under the write scope: callers may hold
		// write_channels without read_channels, and this internal read is
		// required to preserve the patch semantics. It does not expose
		// channel data.
		existing, err := authz.RunWithScopeDecision(ctx, scopes.ScopeWriteChannels, func(queryCtx context.Context) (*ent.Channel, error) {
			return svc.entFromContext(queryCtx).Channel.Query().
				Where(channel.IDEQ(id)).
				Select(channel.FieldSettings, channel.FieldUpdatedAt).
				Only(queryCtx)
		})
		if err != nil {
			return nil, fmt.Errorf("failed to load channel settings: %w", err)
		}

		merged := input.Apply(existing.Settings)

		updated, err := svc.updateChannel(withChannelExpectedVersion(ctx, existing.UpdatedAt), id, &ent.UpdateChannelInput{Settings: &merged})
		if err == nil {
			return updated, nil
		}

		// A concurrent-update conflict means the channel changed between the
		// read and the write: re-read and re-merge the patch. The read above
		// already proved the channel exists, so a conflict on the last attempt
		// is reported as-is.
		if errors.Is(err, ErrChannelUpdatedConcurrently) {
			lastErr = err
			continue
		}

		return nil, err
	}

	return nil, lastErr
}

func validateUpdatedChannelSettings(settings *objects.ChannelSettings, effectiveType channel.Type) error {
	if settings == nil {
		return nil
	}
	if err := validateChannelAddresses(settings, effectiveType); err != nil {
		return err
	}
	if settings.BodyOverrideOperations != nil {
		if err := ValidateBodyOverrideOperations(settings.BodyOverrideOperations); err != nil {
			return fmt.Errorf("invalid body override operations: %w", err)
		}
	}

	if settings.HeaderOverrideOperations != nil {
		if err := ValidateOverrideHeaders(settings.HeaderOverrideOperations); err != nil {
			return fmt.Errorf("invalid header override operations: %w", err)
		}
	}

	if err := ValidateRateLimit(settings.RateLimit); err != nil {
		return fmt.Errorf("invalid rate limit: %w", err)
	}
	if err := ValidateChannelResponseTimeouts(settings); err != nil {
		return err
	}
	if err := settings.ServiceTierBilling.Validate(); err != nil {
		return err
	}
	if err := settings.ResponsesServiceTierOverride.Validate(); err != nil {
		return err
	}

	if err := NormalizeRetryableStatusCodes(settings); err != nil {
		return err
	}

	if err := NormalizeRetryableErrorPatterns(settings); err != nil {
		return err
	}
	return validateChannelModelContexts(settings)
}

// Preserve fields owned by other channel settings operations during a full update.
func mergeUpdatedChannelSettings(input, stored *objects.ChannelSettings) objects.ChannelSettings {
	settings := *input
	settings.ModelPriceMultiplier = nil
	settings.CacheWriteBilling = nil
	settings.TagPriorities = nil
	settings.GlobalPriority = nil
	settings.Upstream = nil
	settings.KeySettings = nil
	settings.DisableKeySessionReuse = false
	if stored != nil {
		settings.ResponsesTierRoutingEnabled = settings.ResponsesTierRoutingEnabled || stored.ResponsesTierRoutingEnabled
		settings.ModelPriceMultiplier = stored.ModelPriceMultiplier
		if settings.ServiceTierBilling == nil {
			settings.ServiceTierBilling = stored.ServiceTierBilling
		}
		settings.CacheWriteBilling = stored.CacheWriteBilling
		settings.TagPriorities = stored.TagPriorities
		settings.GlobalPriority = stored.GlobalPriority
		settings.Upstream = stored.Upstream
		settings.KeySettings = stored.KeySettings
		settings.DisableKeySessionReuse = stored.DisableKeySessionReuse
		if input.UpstreamAddresses == nil {
			settings.UpstreamAddresses = stored.UpstreamAddresses
			settings.AddressFailover = stored.AddressFailover
		}
	}

	return settings
}
