// Package clientsession identifies the caller's conversation independently of trace and routing.
package clientsession

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"
)

const Header = "AH-Session-Id"

type contextKey struct{}

func Normalize(id string) string {
	id = strings.TrimSpace(id)
	if len(id) > 256 || !utf8.ValidString(id) || strings.IndexFunc(id, unicode.IsControl) >= 0 {
		return ""
	}
	return id
}

func WithID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

func FromContext(ctx context.Context) string {
	id, _ := ctx.Value(contextKey{}).(string)
	return id
}
