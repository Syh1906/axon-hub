package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/looplj/axonhub/internal/objects"
)

// 每把 Key 独立保存结果，不参与现有渠道可用性判断。
type ChannelUpstreamStatus struct {
	ent.Schema
}

func (ChannelUpstreamStatus) Mixin() []ent.Mixin {
	return []ent.Mixin{TimeMixin{}}
}

func (ChannelUpstreamStatus) Fields() []ent.Field {
	return []ent.Field{
		field.Int("channel_id").Immutable(),
		field.String("key_fingerprint").MaxLen(64).Immutable(),
		field.String("config_fingerprint").MaxLen(64),
		field.JSON("snapshot", &objects.UpstreamKeySnapshot{}),
	}
}

func (ChannelUpstreamStatus) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("channel_id", "key_fingerprint").
			Unique().StorageKey("channel_upstream_statuses_by_channel_key"),
	}
}

func (ChannelUpstreamStatus) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("channel", Channel.Type).Ref("upstream_statuses").
			Field("channel_id").Required().Immutable().Unique().
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

func (ChannelUpstreamStatus) Annotations() []schema.Annotation {
	return []schema.Annotation{entgql.Skip()}
}
