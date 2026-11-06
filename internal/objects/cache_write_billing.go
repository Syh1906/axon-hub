package objects

import "fmt"

// CacheWriteBilling selects prices while retaining the reported cache duration.
type CacheWriteBilling string

const (
	CacheWriteBillingReported CacheWriteBilling = "reported"
	CacheWriteBillingSwap     CacheWriteBilling = "swap_5m_1h"
)

func (b CacheWriteBilling) Validate() error {
	if b != CacheWriteBillingReported && b != CacheWriteBillingSwap {
		return fmt.Errorf("invalid cache write billing rule: %q", b)
	}
	return nil
}

func (b CacheWriteBilling) PricingVariant(actual PromptWriteCacheVariantCode) PromptWriteCacheVariantCode {
	if b == CacheWriteBillingSwap {
		switch actual {
		case PromptWriteCacheVariantCode1Hour:
			return PromptWriteCacheVariantCode5Min
		case PromptWriteCacheVariantCode5Min:
			return PromptWriteCacheVariantCode1Hour
		}
	}
	return actual
}
