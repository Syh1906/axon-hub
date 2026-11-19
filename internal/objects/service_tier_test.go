package objects

import (
	"bytes"
	"testing"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestServiceTierValueSkipsHistoryAndPreservesJSONSemantics(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"service_tier":" first ","input":[{"service_tier":"nested"}],"service\u005ftier":" priority "}`, "priority"},
		{`{"SERVICE_TIER":"wrong case"}`, ""},
		{`{"service_tier":"default","service_tier":null}`, ""},
		{`{"service_tier":42}`, "[invalid]"},
		{`{"service_tier":"default"} false`, ""},
		{`{"service_tier":"default","input":[}`, ""},
		{`{"service_tier":"` + string(bytes.Repeat([]byte{'x'}, 129)) + `"}`, "[invalid]"},
	} {
		require.Equal(t, tc.want, ServiceTierValue([]byte(tc.body)))
	}
}

func BenchmarkServiceTierLargeHistory(b *testing.B) {
	body := append([]byte(`{"input":"`), bytes.Repeat([]byte{'x'}, 25<<20)...)
	body = append(body, `","service_tier":"default"}`...)
	b.ReportAllocs()
	for b.Loop() {
		if ServiceTierValue(body) != "default" {
			b.Fatal("missing outbound tier")
		}
	}
}

func TestServiceTierDecision(t *testing.T) {
	prices := &ServiceTierPrices{Fast: &ServiceTierPrice{Source: ServiceTierPriceCustom, Multiplier: lo.ToPtr(decimal.RequireFromString("1.6"))}, Ultrafast: &ServiceTierPrice{Source: ServiceTierPriceCustom, Multiplier: lo.ToPtr(decimal.NewFromInt(6))}}
	for _, tc := range []struct {
		name, client, upstream string
		basis                  ServiceTierBasis
		conflict               bool
		mode, rate, reason     string
	}{
		{"fixed-standard", "ultrafast", "fast", ServiceTierBasisFixedStandard, false, "standard", "1", "channel_fixed"},
		{"fixed-fast", "ultrafast", "default", ServiceTierBasisFixedFast, false, "fast", "1.6", "channel_fixed"},
		{"fixed-ultrafast", "default", "", ServiceTierBasisFixedUltrafast, false, "ultrafast", "6", "channel_fixed"},
		{"downgrade", "priority", "default", ServiceTierBasisUpstream, false, "standard", "1", "upstream"},
		{"ultra-to-fast", "ultrafast", "Priority", ServiceTierBasisUpstream, false, "fast", "1.6", "upstream"},
		{"missing", "ultrafast", "", ServiceTierBasisUpstream, false, "ultrafast", "6", "missing_upstream"},
		{"unreliable", "fast", "default", ServiceTierBasisClient, false, "fast", "1.6", "channel_client"},
		{"higher", "default", "ultrafast", ServiceTierBasisUpstream, false, "ultrafast", "6", "upstream"},
		{"auto", "auto", "auto", ServiceTierBasisUpstream, false, "standard", "1", "invalid_upstream"},
		{"conflict", "fast", "ultrafast", ServiceTierBasisUpstream, true, "fast", "1.6", "conflicting_upstream"},
		{"flex", "ultrafast", "flex", ServiceTierBasisUpstream, false, "flex", "1", "upstream"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &ServiceTierEvidence{Settings: ServiceTierSettings{Enabled: true, Basis: tc.basis}, Client: tc.client, Upstream: tc.upstream, Observed: tc.upstream != "", Conflict: tc.conflict}
			d, rate := e.Resolve(prices)
			require.Equal(t, tc.mode, d.Mode)
			require.Equal(t, tc.rate, rate.String())
			require.Equal(t, tc.reason, d.Reason)
		})
	}
	e := &ServiceTierEvidence{Settings: ServiceTierSettings{Enabled: true, Basis: ServiceTierBasisUpstream}, Client: "fast"}
	_, rate := e.Resolve(nil)
	require.Nil(t, rate)
	prices.Fast.Multiplier = lo.ToPtr(decimal.Zero)
	_, rate = e.Resolve(prices)
	require.True(t, rate.IsZero())
	e.Settings.Enabled = false
	_, rate = e.Resolve(nil)
	require.Equal(t, "1", rate.String())
}

func TestServiceTierOutboundEvidence(t *testing.T) {
	for _, tc := range []struct {
		client, sent, returned string
		conflict               bool
		mode, reason           string
	}{
		{"default", "priority", "", false, "fast", "outbound_missing_upstream"},
		{"ultrafast", "priority", "wrong", false, "fast", "outbound_invalid_upstream"},
		{"priority", "", "ultrafast", true, "standard", "outbound_conflicting_upstream"},
		{"ultrafast", "auto", "", false, "standard", "outbound_missing_upstream"},
	} {
		e := &ServiceTierEvidence{Client: tc.client, Outbound: tc.sent, UseOutboundTier: true, Settings: ServiceTierSettings{Enabled: true, Basis: ServiceTierBasisUpstream}, Conflict: tc.conflict}
		e.Upstream, e.Observed = tc.returned, tc.returned != ""
		d := e.Decision()
		require.Equal(t, tc.mode, d.Mode)
		require.Equal(t, tc.reason, d.Reason)
		require.Equal(t, "outbound", d.Basis)
		require.False(t, d.Confirmed)
		e.UseOutboundTier = false
		require.Equal(t, "client", e.Decision().Basis)
	}
}

func TestServiceTierEvidencePreservesInvalidAndConflictingDeclarations(t *testing.T) {
	e := &ServiceTierEvidence{}
	e.Observe([]byte(`{"service_tier":42}`))
	require.Equal(t, "[invalid]", e.Upstream)
	e.Observe([]byte(`{"service_tier":"priority"}`))
	require.False(t, e.Conflict)
	e.Observe([]byte(`{"service_tier":"fast"}`))
	require.False(t, e.Conflict)
	e.Observe([]byte(`{"service_tier":"auto"}`))
	require.False(t, e.Conflict)
	e.Observe([]byte(`{"service_tier":"default"}`))
	require.True(t, e.Conflict)
}

func TestServiceTierConfigurationValidation(t *testing.T) {
	for _, row := range OfficialServiceTierPrices() {
		rate := row.Price("fast", "ratio", row.Contexts[0])
		require.NoError(t, (&ServiceTierPrices{Fast: rate}).Validate())
	}
	require.Error(t, (&ServiceTierPrices{Fast: &ServiceTierPrice{Source: ServiceTierPriceCustom, Multiplier: lo.ToPtr(decimal.NewFromInt(-1))}}).Validate())
	require.Error(t, (&ServiceTierPrices{Fast: &ServiceTierPrice{Source: ServiceTierPriceOfficial, Multiplier: lo.ToPtr(decimal.NewFromInt(2)), ReferenceModel: "unknown"}}).Validate())
	require.NoError(t, (&ServiceTierPrices{Fast: &ServiceTierPrice{Source: ServiceTierPriceCustom, Multiplier: lo.ToPtr(decimal.Zero)}}).Validate())
}

func TestServiceTierOfficialItemsScopeAndValidation(t *testing.T) {
	for _, row := range OfficialServiceTierPrices() {
		for _, scope := range row.Contexts {
			for _, mode := range []string{"fast", "ultrafast"} {
				for _, application := range []string{"ratio", "unit_price"} {
					price := row.Price(mode, application, scope)
					rates := &ServiceTierPrices{}
					if mode == "fast" {
						rates.Fast = price
					} else {
						rates.Ultrafast = price
					}
					require.NoError(t, rates.Validate())
					if !price.Unpriced {
						input := int64(1)
						if scope.Above != nil {
							input = *scope.Above + 1
						}
						resolved := ResolveServiceTierItem(mode, input, rates, nil, PriceItemCodeUsage, "")
						require.NotEqual(t, ServiceTierItemUnpriced, resolved.Method)
						if scope.UpTo != nil {
							require.Equal(t, ServiceTierItemUnpriced, ResolveServiceTierItem(mode, *scope.UpTo+1, rates, nil, PriceItemCodeUsage, "").Method)
						}
						require.Equal(t, ServiceTierItemUnpriced, ResolveServiceTierItem(mode, input, rates, nil, PriceItemCodeWriteCachedTokens, PromptWriteCacheVariantCode1Hour).Method)
						price.Items[0].Value = lo.ToPtr(decimal.NewFromInt(999))
						require.Error(t, rates.Validate())
					}
				}
			}
		}
	}
}
