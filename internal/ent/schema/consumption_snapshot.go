package schema

import (
	"time"

	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/looplj/axonhub/internal/scopes"
)

// ConsumptionSnapshot 保存可重建的自然日汇总；原始账目仍是事实来源。
type ConsumptionSnapshot struct{ ent.Schema }

func (ConsumptionSnapshot) Fields() []ent.Field {
	return []ent.Field{
		field.String("cache_key").MaxLen(64).Immutable(),
		field.Int("project_id").Immutable(),
		field.Time("range_start").Immutable(),
		field.Time("range_end").Immutable(),
		field.Time("generated_at"),
		field.String("source_version").MaxLen(64).Default(""),
		field.String("snapshot_version").MaxLen(64).Default(""),
		field.Bytes("payload").Optional().SchemaType(map[string]string{dialect.MySQL: "longblob"}),
		field.Bytes("query").Optional(),
		field.Time("requested_at").Default(time.Now),
		field.Time("lease_until").Default(func() time.Time { return time.Unix(0, 0).UTC() }),
		field.String("lease_token").MaxLen(36).Default(""),
		field.Bool("pending").Default(true),
		field.Bool("failed").Default(false),
		field.String("fingerprint").MaxLen(64).Default(""),
		field.Time("checked_at").Default(func() time.Time { return time.Unix(0, 0).UTC() }),
	}
}

func (ConsumptionSnapshot) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("cache_key").Unique().StorageKey("consumption_snapshot_by_key"),
		index.Fields("requested_at").StorageKey("consumption_snapshot_by_requested_at"),
		index.Fields("project_id", "range_start").StorageKey("consumption_snapshot_by_project_date"),
		index.Fields("pending", "lease_until", "requested_at").StorageKey("consumption_snapshot_by_pending_work"),
	}
}

func (ConsumptionSnapshot) Annotations() []schema.Annotation {
	return []schema.Annotation{entgql.Skip(entgql.SkipAll)}
}

func (ConsumptionSnapshot) Policy() ent.Policy {
	return scopes.Policy{Query: scopes.QueryPolicy{scopes.OwnerRule()}, Mutation: scopes.MutationPolicy{scopes.OwnerRule()}}
}
