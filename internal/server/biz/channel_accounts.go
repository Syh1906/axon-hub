package biz

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/looplj/axonhub/internal/server/biz/upstream"
)

func populateUpstreamAccounts(ch *ent.Channel, state *objects.ChannelUpstreamState, now time.Time) {
	state.Accounts = []*objects.ChannelUpstreamAccountState{}
	state.BalanceSummary.Balances = []objects.UpstreamBalance{}
	if ch.UpstreamCredentials != nil {
		for _, credential := range ch.UpstreamCredentials.Accounts {
			account := &objects.ChannelUpstreamAccountState{Ref: credential.Ref, Name: credential.Name, Provider: credential.Provider, HasToken: credential.AccountToken != "", VerifiedAt: credential.VerifiedAt}
			if credential.AccountID > 0 {
				account.AccountID = lo.ToPtr(credential.AccountID)
			}
			for _, key := range state.Keys {
				if key.AccountRef != credential.Ref {
					continue
				}
				account.KeyCount++
				var balance *objects.UpstreamBalance
				var updated *time.Time
				complete := false
				if credential.Provider == objects.UpstreamProviderNewAPI {
					if snapshot := key.Snapshot.Account; snapshot != nil && (credential.AccountID == 0 || snapshot.AccountID == credential.AccountID) {
						balance, updated = &snapshot.Balance, &snapshot.UpdatedAt
						complete = key.Snapshot.RateState != objects.UpstreamQueryError && key.Snapshot.RateState != objects.UpstreamQueryAuthRequired && key.Snapshot.RateState != objects.UpstreamQueryUnmatched
						if account.AccountID == nil {
							account.AccountID = lo.ToPtr(snapshot.AccountID)
						}
					}
				} else {
					for i := range key.Snapshot.Balances {
						if key.Snapshot.Balances[i].Kind == objects.UpstreamBalanceAccount {
							balance, updated = &key.Snapshot.Balances[i], key.Snapshot.BalanceUpdatedAt
							complete = key.Snapshot.BalanceState == objects.UpstreamQuerySuccess
							break
						}
					}
				}
				if balance == nil || balance.Amount == nil || updated == nil {
					continue
				}
				if account.UpdatedAt == nil || updated.After(*account.UpdatedAt) {
					account.Balance, account.UpdatedAt = balance, updated
					account.Complete = complete && now.Sub(*updated) <= 30*time.Minute
				}
			}
			state.Accounts = append(state.Accounts, account)
		}
	}
	byCurrency := make(map[string]int)
	seen := make(map[string]bool)
	allComplete := true
	for _, account := range state.Accounts {
		if account.KeyCount == 0 {
			continue
		}
		identity := account.Ref
		if account.Provider == objects.UpstreamProviderNewAPI && account.AccountID != nil {
			identity = fmt.Sprintf("newapi:%d", *account.AccountID)
		}
		if seen[identity] {
			continue
		}
		seen[identity] = true
		state.BalanceSummary.AccountCount++
		allComplete = allComplete && account.Complete
		if account.Balance == nil || account.Balance.Amount == nil {
			continue
		}
		state.BalanceSummary.KnownAccountCount++
		balance := account.Balance
		unit := balance.Currency + ":" + balance.Unit
		if index, ok := byCurrency[unit]; ok {
			total := state.BalanceSummary.Balances[index].Amount.Add(*balance.Amount)
			state.BalanceSummary.Balances[index].Amount = &total
		} else {
			byCurrency[unit] = len(state.BalanceSummary.Balances)
			state.BalanceSummary.Balances = append(state.BalanceSummary.Balances, objects.UpstreamBalance{Kind: objects.UpstreamBalanceAccount, Currency: balance.Currency, Unit: balance.Unit, Amount: lo.ToPtr(*balance.Amount)})
		}
	}
	for _, key := range state.Keys {
		if ch.UpstreamCredentials.Account(key.AccountRef) == nil {
			state.BalanceSummary.UnassignedKeyCount++
		}
	}
	state.BalanceSummary.Complete = allComplete && state.BalanceSummary.AccountCount > 0 && state.BalanceSummary.UnassignedKeyCount == 0 && state.BalanceSummary.KnownAccountCount == state.BalanceSummary.AccountCount
}

func (svc *ChannelService) SaveChannelUpstreamAccount(ctx context.Context, id int, input objects.SaveChannelUpstreamAccountInput) (*objects.ChannelUpstreamState, error) {
	ch, err := svc.upstreamChannel(ctx, id, scopes.ScopeWriteChannels)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return nil, fmt.Errorf("account name is required")
	}
	if input.Provider != objects.UpstreamProviderNewAPI && input.Provider != objects.UpstreamProviderSub2API {
		return nil, fmt.Errorf("unsupported account provider")
	}
	account := objects.UpstreamAccountCredentials{Ref: uuid.NewString(), Name: name, Provider: input.Provider}
	if input.Ref != nil {
		existing := ch.UpstreamCredentials.Account(*input.Ref)
		if existing == nil {
			return nil, fmt.Errorf("upstream account not found")
		}
		account = *existing
		if account.Provider != input.Provider {
			return nil, fmt.Errorf("account provider cannot be changed")
		}
		account.Name = name
	}
	credentialChanged := input.Ref == nil
	if input.AccountToken != nil && strings.TrimSpace(*input.AccountToken) != "" {
		credentialChanged = credentialChanged || account.AccountToken != strings.TrimSpace(*input.AccountToken)
		account.AccountToken = strings.TrimSpace(*input.AccountToken)
	}
	if input.AccountID != nil {
		credentialChanged = credentialChanged || account.AccountID != *input.AccountID
		account.AccountID = *input.AccountID
	}
	if account.Provider == objects.UpstreamProviderNewAPI && credentialChanged {
		if account.AccountToken == "" {
			return nil, fmt.Errorf("ACCOUNT_NOT_CONNECTED")
		}
		verification, err := svc.VerifyChannelUpstreamAccount(ctx, id, &account.AccountToken, &account.AccountID, nil)
		if err != nil {
			return nil, err
		}
		if !verification.Success {
			return nil, fmt.Errorf("%s", verification.ErrorCode)
		}
		account.AccountID, account.VerifiedAt = *verification.AccountID, lo.ToPtr(time.Now().UTC())
	}
	credentials := objects.ChannelUpstreamCredentials{Accounts: []objects.UpstreamAccountCredentials{}}
	if ch.UpstreamCredentials != nil {
		credentials.Accounts = slices.Clone(ch.UpstreamCredentials.Accounts)
	}
	index := -1
	for i, existing := range credentials.Accounts {
		if existing.Ref == account.Ref {
			index = i
			continue
		}
		if account.Provider == objects.UpstreamProviderNewAPI && existing.Provider == account.Provider && account.AccountID > 0 && existing.AccountID == account.AccountID {
			return nil, fmt.Errorf("ACCOUNT_ALREADY_EXISTS")
		}
	}
	if index < 0 {
		credentials.Accounts = append(credentials.Accounts, account)
	} else {
		credentials.Accounts[index] = account
	}
	update := svc.entFromContext(ctx).Channel.UpdateOneID(id).Where(channel.UpdatedAtEQ(ch.UpdatedAt)).SetUpstreamCredentials(&credentials)
	keys := lo.Uniq(ch.Credentials.GetAllAPIKeys())
	if input.Ref == nil && len(keys) == 1 && ch.Settings.KeySettingsFor(upstream.KeyRef(keys[0])).AccountRef == "" {
		update.SetSettings(settingsWithKeyAccount(ch.Settings, upstream.KeyRef(keys[0]), account.Ref))
	}
	stored, err := update.Save(ctx)
	if err != nil {
		return nil, err
	}
	svc.reloadChannelsAfterCommit(ctx)
	return svc.upstreamState(ctx, stored)
}

func (svc *ChannelService) DeleteChannelUpstreamAccount(ctx context.Context, id int, ref string) (*objects.ChannelUpstreamState, error) {
	ch, err := svc.upstreamChannel(ctx, id, scopes.ScopeWriteChannels)
	if err != nil {
		return nil, err
	}
	if ch.UpstreamCredentials.Account(ref) == nil {
		return nil, fmt.Errorf("upstream account not found")
	}
	credentials := *ch.UpstreamCredentials
	credentials.Accounts = lo.Filter(credentials.Accounts, func(account objects.UpstreamAccountCredentials, _ int) bool { return account.Ref != ref })
	settings := objects.ChannelSettings{}
	if ch.Settings != nil {
		settings = *ch.Settings
		settings.KeySettings = slices.Clone(ch.Settings.KeySettings)
	}
	for i := range settings.KeySettings {
		if settings.KeySettings[i].AccountRef == ref {
			settings.KeySettings[i].AccountRef = ""
		}
	}
	stored, err := svc.entFromContext(ctx).Channel.UpdateOneID(id).Where(channel.UpdatedAtEQ(ch.UpdatedAt)).SetSettings(&settings).SetUpstreamCredentials(&credentials).Save(ctx)
	if err != nil {
		return nil, err
	}
	svc.reloadChannelsAfterCommit(ctx)
	return svc.upstreamState(ctx, stored)
}

func (svc *ChannelService) UpdateChannelKeySettings(ctx context.Context, id int, input objects.ChannelKeySettingsInput) (*objects.ChannelUpstreamState, error) {
	ch, err := svc.upstreamChannel(ctx, id, scopes.ScopeWriteChannels)
	if err != nil {
		return nil, err
	}
	if !lo.ContainsBy(ch.Credentials.GetAllAPIKeys(), func(key string) bool { return upstream.KeyRef(key) == input.KeyRef }) {
		return nil, fmt.Errorf("channel key not found")
	}
	if input.AccountRef != "" && ch.UpstreamCredentials.Account(input.AccountRef) == nil {
		return nil, fmt.Errorf("upstream account not found")
	}
	input.Remark = strings.TrimSpace(input.Remark)
	settings := objects.ChannelSettings{}
	if ch.Settings != nil {
		settings = *ch.Settings
		settings.KeySettings = slices.Clone(ch.Settings.KeySettings)
	}
	index := slices.IndexFunc(settings.KeySettings, func(item objects.ChannelKeySettings) bool { return item.KeyRef == input.KeyRef })
	if index < 0 {
		settings.KeySettings = append(settings.KeySettings, objects.ChannelKeySettings{KeyRef: input.KeyRef})
		index = len(settings.KeySettings) - 1
	}
	settings.KeySettings[index].Remark = input.Remark
	settings.KeySettings[index].AccountRef = input.AccountRef
	if input.Priority != nil {
		settings.KeySettings[index].Priority = *input.Priority
	}
	stored, err := svc.entFromContext(ctx).Channel.UpdateOneID(id).Where(channel.UpdatedAtEQ(ch.UpdatedAt)).SetSettings(&settings).Save(ctx)
	if err != nil {
		return nil, err
	}
	svc.reloadChannelsAfterCommit(ctx)
	return svc.upstreamState(ctx, stored)
}

func (svc *ChannelService) SetChannelKeySessionReuse(ctx context.Context, id int, enabled bool) (*objects.ChannelUpstreamState, error) {
	ch, err := svc.upstreamChannel(ctx, id, scopes.ScopeWriteChannels)
	if err != nil {
		return nil, err
	}
	settings := objects.ChannelSettings{}
	if ch.Settings != nil {
		settings = *ch.Settings
	}
	settings.DisableKeySessionReuse = !enabled
	stored, err := svc.entFromContext(ctx).Channel.UpdateOneID(id).Where(channel.UpdatedAtEQ(ch.UpdatedAt)).SetSettings(&settings).Save(ctx)
	if err != nil {
		return nil, err
	}
	svc.reloadChannelsAfterCommit(ctx)
	return svc.upstreamState(ctx, stored)
}

// RevealChannelCredential 不进入普通列表 DTO 或查询缓存，权限与凭据管理一致。
func (svc *ChannelService) RevealChannelCredential(ctx context.Context, id int, keyRef, accountRef *string) (string, error) {
	ch, err := svc.upstreamChannel(ctx, id, scopes.ScopeWriteChannels)
	if err != nil {
		return "", err
	}
	if (keyRef == nil) == (accountRef == nil) {
		return "", fmt.Errorf("select exactly one credential")
	}
	if keyRef != nil {
		for _, key := range ch.Credentials.GetAllAPIKeys() {
			if upstream.KeyRef(key) == *keyRef {
				return key, nil
			}
		}
	} else if account := ch.UpstreamCredentials.Account(*accountRef); account != nil {
		return account.AccountToken, nil
	}
	return "", fmt.Errorf("credential not found")
}

// ensureSingleKeySub2APIAccount records the sole key's account after Sub2API is identified.
// Multiple keys never imply shared account ownership.
func (svc *ChannelService) ensureSingleKeySub2APIAccount(ctx context.Context, ch *ent.Channel, snapshot *objects.UpstreamKeySnapshot) (*ent.Channel, error) {
	if snapshot.Provider != objects.UpstreamProviderSub2API || (snapshot.BalanceState != objects.UpstreamQuerySuccess && snapshot.RateState != objects.UpstreamQuerySuccess) {
		return ch, nil
	}
	keys := lo.Uniq(ch.Credentials.GetAllAPIKeys())
	if len(keys) != 1 || (ch.UpstreamCredentials != nil && (len(ch.UpstreamCredentials.Accounts) > 0 || ch.UpstreamCredentials.AccountToken != "")) {
		return ch, nil
	}
	ref := upstream.KeyRef(keys[0])
	if ch.Settings.KeySettingsFor(ref).AccountRef != "" {
		return ch, nil
	}
	accountRef := uuid.NewString()
	settings := settingsWithKeyAccount(ch.Settings, ref, accountRef)
	credentials := objects.ChannelUpstreamCredentials{Accounts: []objects.UpstreamAccountCredentials{{Ref: accountRef, Name: "默认账号", Provider: objects.UpstreamProviderSub2API}}}
	stored, err := svc.entFromContext(ctx).Channel.UpdateOneID(ch.ID).Where(channel.UpdatedAtEQ(ch.UpdatedAt)).SetSettings(settings).SetUpstreamCredentials(&credentials).Save(ctx)
	if ent.IsNotFound(err) {
		// A concurrent channel edit wins; the next refresh may initialize the default account.
		return ch, nil
	}
	if err != nil {
		return nil, err
	}
	svc.reloadChannelsAfterCommit(ctx)
	return stored, nil
}

func settingsWithKeyAccount(original *objects.ChannelSettings, keyRef, accountRef string) *objects.ChannelSettings {
	settings := objects.ChannelSettings{}
	if original != nil {
		settings = *original
		settings.KeySettings = slices.Clone(original.KeySettings)
	}
	index := slices.IndexFunc(settings.KeySettings, func(item objects.ChannelKeySettings) bool { return item.KeyRef == keyRef })
	if index < 0 {
		settings.KeySettings = append(settings.KeySettings, objects.ChannelKeySettings{KeyRef: keyRef})
		index = len(settings.KeySettings) - 1
	}
	settings.KeySettings[index].AccountRef = accountRef
	return &settings
}
