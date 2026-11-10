package biz

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xregexp"
)

func validateAssociationVersionOrder(assoc *objects.ModelAssociation) error {
	if assoc == nil || !assoc.PreferLatestVersion {
		return nil
	}
	pattern := ""
	switch assoc.Type {
	case "regex":
		if assoc.Regex != nil {
			pattern = assoc.Regex.Pattern
		}
	case "channel_regex":
		if assoc.ChannelRegex != nil {
			pattern = assoc.ChannelRegex.Pattern
		}
	case "channel_tags_regex":
		if assoc.ChannelTagsRegex != nil {
			pattern = assoc.ChannelTagsRegex.Pattern
		}
	}
	if !xregexp.HasNamedGroup(pattern, "version") {
		return fmt.Errorf("version ordering requires a regex association with a named version group (?<version>...)")
	}
	return nil
}

// Normalize decimal segments once, without integer overflow or floating point rounding.
func numericModelVersion(value string) []string {
	parts := strings.Split(value, ".")
	for i, part := range parts {
		if part == "" {
			return nil
		}
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return nil
			}
		}
		parts[i] = strings.TrimLeft(part, "0")
	}
	return parts
}

func compareModelVersions(a, b []string) int {
	for i := 0; i < max(len(a), len(b)); i++ {
		x, y := "", ""
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if c := cmp.Compare(len(x), len(y)); c != 0 {
			return c
		}
		if c := strings.Compare(x, y); c != 0 {
			return c
		}
	}
	return 0
}

func matchVersionedEntries(assoc *objects.ModelAssociation, pattern string, entries map[string]ChannelModelEntry, tracker *DuplicateKeyTracker, channelID int) []ChannelModelEntry {
	if !assoc.PreferLatestVersion {
		var result []ChannelModelEntry
		for id, entry := range entries {
			if xregexp.MatchString(pattern, id) && tracker.Add(channelID, id) {
				result = append(result, entry)
			}
		}
		return result
	}
	type ranked struct {
		entry   ChannelModelEntry
		version []string
	}
	matches := make([]ranked, 0)
	for id, entry := range entries {
		version, matched := xregexp.NamedCapture(pattern, id, "version")
		if !matched {
			continue
		}
		parts := numericModelVersion(version)
		if parts != nil && tracker.Add(channelID, id) {
			matches = append(matches, ranked{entry, parts})
		}
	}
	slices.SortFunc(matches, func(a, b ranked) int {
		if order := compareModelVersions(a.version, b.version); order != 0 {
			return -order
		}
		if order := strings.Compare(a.entry.RequestModel, b.entry.RequestModel); order != 0 {
			return order
		}
		return strings.Compare(a.entry.ActualModel, b.entry.ActualModel)
	})
	result := make([]ChannelModelEntry, len(matches))
	for i, match := range matches {
		result[i] = match.entry
	}
	return result
}

// appendVersionPreviewWarnings preserves valid matches and reports excluded IDs locally.
func appendVersionPreviewWarnings(result []*ModelChannelConnection, associations []*objects.ModelAssociation, channels []*Channel) []*ModelChannelConnection {
	for _, assoc := range associations {
		if assoc == nil || assoc.Disabled || !assoc.PreferLatestVersion {
			continue
		}
		copy := *assoc
		copy.PreferLatestVersion = false
		for _, connection := range matchSingleAssociation(&copy, channels, NewDuplicateKeyTracker(), "") {
			pattern := ""
			switch assoc.Type {
			case "regex":
				pattern = assoc.Regex.Pattern
			case "channel_regex":
				pattern = assoc.ChannelRegex.Pattern
			case "channel_tags_regex":
				pattern = assoc.ChannelTagsRegex.Pattern
			}
			var excluded []string
			for _, entry := range connection.Models {
				version, ok := xregexp.NamedCapture(pattern, entry.RequestModel, "version")
				if !ok || numericModelVersion(version) == nil {
					excluded = append(excluded, entry.RequestModel)
				}
			}
			if len(excluded) == 0 {
				continue
			}
			slices.Sort(excluded)
			var target *ModelChannelConnection
			for _, existing := range result {
				if existing.Channel.ID == connection.Channel.ID && existing.Priority == assoc.Priority {
					target = existing
					break
				}
			}
			if target == nil {
				target = &ModelChannelConnection{Channel: connection.Channel, Priority: assoc.Priority, PreferLatestVersion: true}
				result = append(result, target)
			}
			target.UnrankedModelIDs = append(target.UnrankedModelIDs, excluded...)
		}
	}
	return result
}
