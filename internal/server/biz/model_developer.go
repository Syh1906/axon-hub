package biz

import "strings"

// NormalizeModelDeveloper canonicalizes built-in IDs while preserving custom names.
func NormalizeModelDeveloper(developer string) string {
	for _, id := range DefaultDeveloperIDs {
		if strings.EqualFold(strings.TrimSpace(developer), id) {
			return id
		}
	}
	return developer
}
