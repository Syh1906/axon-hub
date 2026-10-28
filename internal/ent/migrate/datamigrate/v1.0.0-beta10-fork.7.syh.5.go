package datamigrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/channelupstreamstatus"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz/upstream"
)

type V1_0_0_Beta10_Fork_7_Syh_5 struct{}

func NewV1_0_0_Beta10_Fork_7_Syh_5() DataMigrator   { return &V1_0_0_Beta10_Fork_7_Syh_5{} }
func (*V1_0_0_Beta10_Fork_7_Syh_5) Version() string { return "v1.0.0-beta10-fork.7.syh.5" }

// 全部读写通过 Ent 的 JSON 字段完成，SQLite、MySQL、PostgreSQL 使用同一升级逻辑。
func (*V1_0_0_Beta10_Fork_7_Syh_5) Migrate(ctx context.Context, client *ent.Client) error {
	ctx = authz.WithSystemBypass(ctx, "channel-accounts-and-addresses-migration")
	channels, err := client.Channel.Query().All(ctx)
	if err != nil {
		return err
	}
	for _, ch := range channels {
		settings := objects.ChannelSettings{}
		if ch.Settings != nil {
			settings = *ch.Settings
		}
		changed := false
		if len(settings.UpstreamAddresses) == 0 && ch.BaseURL != "" {
			parsed, err := url.Parse(ch.BaseURL)
			if err == nil && parsed.Host != "" && parsed.RawQuery == "" && parsed.Fragment == "" && parsed.User == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") {
				settings.UpstreamAddresses = []objects.ChannelUpstreamAddress{{ID: "primary", Name: "主站", URL: ch.BaseURL, Priority: 10, Enabled: true}}
				settings.AddressFailover, changed = true, true
			}
		}
		credentials := ch.UpstreamCredentials
		if credentials != nil && credentials.AccountToken != "" {
			legacy := struct {
				AccountToken string `json:"accountToken"`
				AccountID    int    `json:"accountId"`
			}{credentials.AccountToken, credentials.AccountID}
			data, _ := json.Marshal(struct {
				BaseURL     string `json:"BaseURL"`
				Credentials any    `json:"Credentials"`
			}{ch.BaseURL, &legacy})
			digest := sha256.Sum256(data)
			rows, err := client.ChannelUpstreamStatus.Query().Where(channelupstreamstatus.ChannelID(ch.ID), channelupstreamstatus.ConfigFingerprint(hex.EncodeToString(digest[:]))).All(ctx)
			if err != nil {
				return err
			}
			credentials = objects.UpgradeAccountCredentials(credentials)
			account := credentials.Account("legacy-account")
			validKeys := make(map[string]bool)
			for _, key := range ch.Credentials.GetAllAPIKeys() {
				validKeys[upstream.KeyRef(key)] = true
			}
			for _, row := range rows {
				if account == nil || !validKeys[row.KeyFingerprint] || row.Snapshot == nil || row.Snapshot.Account == nil {
					continue
				}
				if account.AccountID == 0 {
					account.AccountID = row.Snapshot.Account.AccountID
				}
				if row.Snapshot.Account.AccountID != account.AccountID || account.AccountID == 0 {
					continue
				}
				if settings.KeySettingsFor(row.KeyFingerprint).AccountRef == "" {
					settings.KeySettings = append(settings.KeySettings, objects.ChannelKeySettings{KeyRef: row.KeyFingerprint, AccountRef: account.Ref})
				}
			}
			changed = true
		}
		if !changed {
			continue
		}
		update := client.Channel.UpdateOneID(ch.ID).Where(channel.UpdatedAtEQ(ch.UpdatedAt)).SetSettings(&settings)
		if credentials != nil {
			update.SetUpstreamCredentials(credentials)
		}
		if _, err := update.Save(ctx); err != nil {
			return fmt.Errorf("upgrade channel %d accounts and addresses: %w", ch.ID, err)
		}
	}
	return nil
}
