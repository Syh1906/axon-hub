package biz

import (
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
)

// modalityTokenQuantity never allocates an aggregate cache count to an assumed
// modality. Missing evidence is distinct from the provider reporting zero.
func modalityTokenQuantity(usage *llm.Usage, code objects.PriceItemCode) (int64, string) {
	d := usage.PromptTokensDetails
	if d == nil {
		if usage.PromptTokens == 0 {
			return 0, ""
		}
		return 0, "input_modality_missing"
	}
	if usage.PromptTokens < 0 || d.CachedTokens < 0 || d.CachedTokens > usage.PromptTokens {
		return 0, "input_modality_invalid"
	}
	if d.TextTokens != nil && d.ImageTokens != nil &&
		(*d.TextTokens < 0 || *d.ImageTokens < 0 || *d.TextTokens > usage.PromptTokens || *d.ImageTokens != usage.PromptTokens-*d.TextTokens) {
		return 0, "input_modality_invalid"
	}
	if d.CachedTokens > 0 {
		var textCached, imageCached *int64
		if d.CachedTokensDetails != nil {
			textCached, imageCached = d.CachedTokensDetails.TextTokens, d.CachedTokensDetails.ImageTokens
		}
		// A modality with no input cannot have cached input. Use that proven
		// zero only while checking the aggregate, never invent an allocation.
		var textCount, imageCount int64
		for i, pair := range [][2]*int64{{d.TextTokens, textCached}, {d.ImageTokens, imageCached}} {
			if pair[1] == nil && (pair[0] == nil || *pair[0] != 0) {
				return 0, "input_cache_modality_missing"
			}
			if pair[1] != nil {
				if i == 0 {
					textCount = *pair[1]
				} else {
					imageCount = *pair[1]
				}
			}
		}
		if textCount < 0 || imageCount < 0 || textCount > d.CachedTokens || imageCount != d.CachedTokens-textCount {
			return 0, "input_modality_invalid"
		}
	}
	total := d.TextTokens
	var cached *int64
	if d.CachedTokensDetails != nil {
		cached = d.CachedTokensDetails.TextTokens
		c := d.CachedTokensDetails
		if c.TextTokens != nil && c.ImageTokens != nil &&
			(*c.TextTokens < 0 || *c.ImageTokens < 0 || *c.TextTokens > d.CachedTokens || *c.ImageTokens != d.CachedTokens-*c.TextTokens) {
			return 0, "input_modality_invalid"
		}
	}
	if code == objects.PriceItemCodePromptImage || code == objects.PriceItemCodePromptImageCached {
		total = d.ImageTokens
		if d.CachedTokensDetails != nil {
			cached = d.CachedTokensDetails.ImageTokens
		}
	}
	cacheItem := code == objects.PriceItemCodePromptTextCached || code == objects.PriceItemCodePromptImageCached
	if cacheItem && d.CachedTokens == 0 && cached == nil {
		return 0, ""
	}
	if cacheItem && cached != nil {
		if *cached < 0 || *cached > d.CachedTokens || (total != nil && (*total < 0 || *cached > *total)) {
			return 0, "input_modality_invalid"
		}
		// A reported cache breakdown is sufficient for separate cache rates
		// when non-cached input is billed by the aggregate input item.
		return *cached, ""
	}
	if total == nil {
		return 0, "input_modality_missing"
	}
	if *total < 0 || *total > usage.PromptTokens {
		return 0, "input_modality_invalid"
	}
	cacheCount := int64(0)
	if cached != nil {
		cacheCount = *cached
	} else if d.CachedTokens > 0 && *total > 0 {
		return 0, "input_cache_modality_missing"
	}
	if cacheCount < 0 || cacheCount > *total || cacheCount > d.CachedTokens {
		return 0, "input_modality_invalid"
	}
	if cacheItem {
		return cacheCount, ""
	}
	if d.WriteCachedTokens > 0 && *total > 0 {
		return 0, "input_cache_modality_missing"
	}
	return *total - cacheCount, ""
}

func promptTokenDetailsFromLog(row *ent.UsageLog) *llm.PromptTokensDetails {
	d := &llm.PromptTokensDetails{
		TextTokens: row.PromptTextTokens, ImageTokens: row.PromptImageTokens,
		CachedTokens: row.PromptCachedTokens, WriteCachedTokens: row.PromptWriteCachedTokens,
		WriteCached5MinTokens: row.PromptWriteCachedTokens5m, WriteCached1HourTokens: row.PromptWriteCachedTokens1h,
	}
	if row.PromptTextCachedTokens != nil || row.PromptImageCachedTokens != nil {
		d.CachedTokensDetails = &llm.CachedTokensDetails{TextTokens: row.PromptTextCachedTokens, ImageTokens: row.PromptImageCachedTokens}
	}
	return d
}
