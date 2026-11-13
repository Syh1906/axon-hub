package contexts

import "context"

type primaryReadKey struct{}

// WithPrimaryRead 用于与已授权写入关联的版本/来源定位，不读取可能滞后的副本。
func WithPrimaryRead(ctx context.Context) context.Context {
	return context.WithValue(ctx, primaryReadKey{}, true)
}
func IsPrimaryRead(ctx context.Context) bool {
	value, _ := ctx.Value(primaryReadKey{}).(bool)
	return value
}
