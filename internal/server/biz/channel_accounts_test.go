package biz

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channelupstreamstatus"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz/upstream"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestNewAPISelectedAccountBalanceDoesNotDependOnKeyGroup(t *testing.T) {
	accountFails, keyUnsupported, keyBalanceWorks := false, false, false
	accountQueries := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var data string
		switch r.URL.Path {
		case "/api/status":
			data = `{"quota_per_unit":500000,"quota_display_type":"USD"}`
		case "/api/user/self":
			accountQueries++
			if accountFails {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			data = `{"id":1,"quota":5000000,"group":"standard"}`
		case "/api/usage/token/", "/api/token/search":
			if keyUnsupported {
				http.NotFound(w, r)
				return
			}
			if r.URL.Path == "/api/token/search" {
				data = `{"items":[],"total":0}`
			} else if keyBalanceWorks {
				data = `{"object":"token_usage","total_available":500000,"total_granted":500000,"total_used":0}`
			} else {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"success":true,"data":` + data + `}`))
	}))
	defer server.Close()
	svc, db := setupTestChannelService(t)
	defer db.Close()
	svc.httpClient = httpclient.NewHttpClient()
	ctx := channelAutoPriceTestContext(db)
	ch := createAutoPriceTestChannel(t, ctx, db, "independent-account", []string{"model"})
	ch = ch.Update().SetBaseURL(server.URL).SetCredentials(objects.ChannelCredentials{APIKeys: []string{"sk-fixture-full"}}).
		SetSettings(&objects.ChannelSettings{Upstream: &objects.ChannelUpstreamSettings{Enabled: true, RefreshIntervalMinutes: 10, CostMode: objects.ChannelCostModeUpstream}, ModelPriceMultiplier: lo.ToPtr(decimal.RequireFromString("0.8"))}).SaveX(ctx)
	saved, err := svc.SaveChannelUpstreamAccount(ctx, ch.ID, objects.SaveChannelUpstreamAccountInput{Name: "Selected", Provider: objects.UpstreamProviderNewAPI, AccountToken: lo.ToPtr("fixture-account"), AccountID: lo.ToPtr(1)})
	require.NoError(t, err)
	accountRef := saved.Accounts[0].Ref
	keyRef := upstream.KeyRef("sk-fixture-full")
	state, refreshed, err := svc.refreshChannelUpstream(ctx, ch.ID, true, true, false, keyRef)
	require.NoError(t, err)
	require.True(t, refreshed)
	require.Equal(t, accountRef, state.Keys[0].AccountRef, "Key 查询失败不能否定手动关联")
	require.Equal(t, "KEY_SEARCH_NOT_FOUND", state.Keys[0].Snapshot.RateErrorCode)
	require.Equal(t, "10", state.Accounts[0].Balance.Amount.String())
	require.True(t, state.Accounts[0].Complete)
	require.True(t, state.BalanceSummary.Complete)
	require.Equal(t, "0.8", state.Keys[0].Applied.Value.String())
	require.Equal(t, objects.CostMultiplierEstimated, state.Keys[0].Applied.Source, "不得以账号余额成功冒认 Key 的倍率")
	readback, err := svc.ChannelUpstreamStates(ctx, []int{ch.ID})
	require.NoError(t, err)
	require.Equal(t, "10", readback[0].BalanceSummary.Balances[0].Amount.String())

	keyUnsupported = true
	_, _, err = svc.refreshChannelUpstream(ctx, ch.ID, true, true, false, keyRef)
	require.NoError(t, err)
	before := accountQueries
	_, refreshed, err = svc.refreshChannelUpstream(ctx, ch.ID, false, false, false, keyRef)
	require.NoError(t, err)
	require.False(t, refreshed, "未到期时复用快照")
	require.Equal(t, before, accountQueries)
	row := db.ChannelUpstreamStatus.Query().Where(channelupstreamstatus.ChannelID(ch.ID)).OnlyX(ctx)
	snapshot := *row.Snapshot
	snapshot.AccountCheckedAt = lo.ToPtr(time.Now().Add(-11 * time.Minute))
	row.Update().SetSnapshot(&snapshot).SaveX(ctx)
	state, refreshed, err = svc.refreshChannelUpstream(ctx, ch.ID, false, false, false, keyRef)
	require.NoError(t, err)
	require.True(t, refreshed, "Key 接口均不支持时，仍须刷新到期的账号余额")
	require.Equal(t, before+1, accountQueries)
	require.True(t, state.Accounts[0].Complete)
	updatedAt := state.Accounts[0].UpdatedAt

	accountFails = true
	state, _, err = svc.refreshChannelUpstream(ctx, ch.ID, true, true, false, keyRef)
	require.NoError(t, err)
	require.Equal(t, "10", state.Accounts[0].Balance.Amount.String(), "认证失败保留上次余额，但不能假装刷新成功")
	require.Equal(t, updatedAt, state.Accounts[0].UpdatedAt)
	require.False(t, state.Accounts[0].Complete)
	require.Equal(t, "UNAUTHORIZED", state.Accounts[0].BalanceErrorCode)
	require.Equal(t, accountRef, state.Keys[0].AccountRef)
	row = db.ChannelUpstreamStatus.Query().Where(channelupstreamstatus.ChannelID(ch.ID)).OnlyX(ctx)
	snapshot = *row.Snapshot
	snapshot.AccountCheckedAt = lo.ToPtr(time.Now().Add(-11 * time.Minute))
	row.Update().SetSnapshot(&snapshot).SaveX(ctx)
	before = accountQueries
	_, refreshed, err = svc.refreshChannelUpstream(ctx, ch.ID, false, false, false, keyRef)
	require.NoError(t, err)
	require.False(t, refreshed, "已确认账号认证失败时停止自动请求，等待修改认证或手动重试")
	require.Equal(t, before, accountQueries)

	keyUnsupported, keyBalanceWorks = false, true
	_, _, err = svc.refreshChannelUpstream(ctx, ch.ID, true, true, false, keyRef)
	require.NoError(t, err)
	row = db.ChannelUpstreamStatus.Query().Where(channelupstreamstatus.ChannelID(ch.ID)).OnlyX(ctx)
	snapshot = *row.Snapshot
	snapshot.AccountCheckedAt = lo.ToPtr(time.Now().Add(-11 * time.Minute))
	snapshot.NextBalanceCheckAt = lo.ToPtr(time.Now().Add(-time.Minute))
	row.Update().SetSnapshot(&snapshot).SaveX(ctx)
	before = accountQueries
	state, refreshed, err = svc.refreshChannelUpstream(ctx, ch.ID, false, false, false, keyRef)
	require.NoError(t, err)
	require.True(t, refreshed, "账号401不能阻止正常Key额度到期刷新")
	require.Equal(t, before, accountQueries, "Key额度刷新不能顺带重试已经401的账号认证")
	require.Equal(t, objects.UpstreamQuerySuccess, state.Keys[0].Snapshot.BalanceState)
	require.Equal(t, "UNAUTHORIZED", state.Accounts[0].BalanceErrorCode)
	require.False(t, state.Accounts[0].Complete)
}

func TestChannelAccountsIndependentAuthAndBalance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		accountID, quota := 1, 5000000
		if r.Header.Get("Authorization") == "Bearer fixture-account-b" {
			accountID, quota = 2, 10000000
		}
		var data any
		switch r.URL.Path {
		case "/api/status":
			data = map[string]any{"quota_per_unit": 500000, "quota_display_type": "USD"}
		case "/api/user/self":
			data = map[string]any{"id": accountID, "quota": quota, "used_quota": 0, "group": "standard"}
		case "/api/token/search":
			key := r.URL.Query().Get("token")
			belongs := (accountID == 1 && strings.HasPrefix(key, "fixture-a")) || (accountID == 2 && key == "fixture-b1")
			items := []map[string]any{}
			if belongs {
				items = append(items, map[string]any{"id": accountID * 10, "user_id": accountID})
			}
			data = map[string]any{"items": items}
		case "/api/token/10", "/api/token/20":
			data = map[string]any{"id": accountID * 10, "user_id": accountID, "group": "standard"}
		case "/api/user/self/groups":
			data = map[string]any{"standard": map[string]any{"ratio": 0.2}}
		case "/api/usage/token/":
			data = map[string]any{"object": "token_usage", "total_available": 500000, "total_granted": 500000, "total_used": 0}
		default:
			t.Errorf("unexpected upstream path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
	}))
	defer server.Close()
	svc, db := setupTestChannelService(t)
	defer db.Close()
	svc.httpClient = httpclient.NewHttpClient()
	ctx := channelAutoPriceTestContext(db)
	ch := createAutoPriceTestChannel(t, ctx, db, "multi-account", []string{"model"})
	ch = ch.Update().SetBaseURL(server.URL).SetCredentials(objects.ChannelCredentials{APIKeys: []string{"fixture-a1", "fixture-a2", "fixture-b1"}}).SaveX(ctx)
	a, err := svc.SaveChannelUpstreamAccount(ctx, ch.ID, objects.SaveChannelUpstreamAccountInput{Name: "A", Provider: objects.UpstreamProviderNewAPI, AccountToken: lo.ToPtr("fixture-account-a"), AccountID: lo.ToPtr(1)})
	require.NoError(t, err)
	refA := a.Accounts[0].Ref
	b, err := svc.SaveChannelUpstreamAccount(ctx, ch.ID, objects.SaveChannelUpstreamAccountInput{Name: "B", Provider: objects.UpstreamProviderNewAPI, AccountToken: lo.ToPtr("fixture-account-b"), AccountID: lo.ToPtr(2)})
	require.NoError(t, err)
	refB := b.Accounts[1].Ref
	for _, key := range ch.Credentials.GetAllAPIKeys() {
		ref := refA
		if key == "fixture-b1" {
			ref = refB
		}
		_, err := svc.UpdateChannelKeySettings(ctx, ch.ID, objects.ChannelKeySettingsInput{KeyRef: upstream.KeyRef(key), AccountRef: ref, Remark: "备注"})
		require.NoError(t, err)
	}
	result, err := refreshTestUpstreamChannel(svc, ctx, ch.ID, true, true)
	require.NoError(t, err)
	state := result.Channels[0]
	require.Len(t, state.Keys, 3)
	require.Len(t, state.Accounts, 2)
	require.Equal(t, 2, state.Accounts[0].KeyCount)
	require.True(t, state.BalanceSummary.Complete)
	require.Equal(t, "30", state.BalanceSummary.Balances[0].Amount.String(), "同账号两把 Key 只计一次")
	for _, key := range state.Keys {
		require.Equal(t, "0.2", key.UpstreamMultiplier.String())
	}
	encoded, err := json.Marshal(state)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "fixture-account")
	require.NotContains(t, string(encoded), "fixture-a1")
	secret, err := svc.RevealChannelCredential(ctx, ch.ID, nil, &refA)
	require.NoError(t, err)
	require.Equal(t, "fixture-account-a", secret)
	_, err = svc.RevealChannelCredential(context.Background(), ch.ID, nil, &refA)
	require.Error(t, err)
	_, err = svc.SaveChannelUpstreamAccount(ctx, ch.ID, objects.SaveChannelUpstreamAccountInput{Name: "duplicate", Provider: objects.UpstreamProviderNewAPI, AccountToken: lo.ToPtr("fixture-account-a")})
	require.ErrorContains(t, err, "ACCOUNT_ALREADY_EXISTS")
	state, err = svc.SaveChannelUpstreamAccount(ctx, ch.ID, objects.SaveChannelUpstreamAccountInput{Ref: &refB, Name: "B renamed", Provider: objects.UpstreamProviderNewAPI})
	require.NoError(t, err)
	require.Equal(t, objects.UpstreamQuerySuccess, state.Keys[0].Snapshot.RateState, "改备注不作废其他账号的结果")
	state, err = svc.UpdateChannelKeySettings(ctx, ch.ID, objects.ChannelKeySettingsInput{KeyRef: upstream.KeyRef("fixture-b1"), AccountRef: refA})
	require.NoError(t, err)
	require.Equal(t, objects.UpstreamQueryNever, state.Keys[2].Snapshot.RateState, "更换归属必须作废该 Key 的旧账号结果")
	require.Equal(t, objects.UpstreamQuerySuccess, state.Keys[0].Snapshot.RateState)
	result, err = refreshTestUpstreamChannel(svc, ctx, ch.ID, true, true)
	require.NoError(t, err)
	require.Equal(t, objects.UpstreamQueryUnmatched, result.Channels[0].Keys[2].Snapshot.RateState)
	_, err = svc.SetChannelKeySessionReuse(ctx, ch.ID, false)
	require.NoError(t, err)
	_, err = svc.UpdateChannel(ctx, ch.ID, &ent.UpdateChannelInput{Settings: &objects.ChannelSettings{ExtraModelPrefix: "kept"}})
	require.NoError(t, err)
	stored := db.Channel.GetX(ctx, ch.ID)
	require.True(t, stored.Settings.DisableKeySessionReuse)
	require.Len(t, stored.Settings.KeySettings, 3)
	_, err = svc.DeleteChannelUpstreamAccount(ctx, ch.ID, refA)
	require.NoError(t, err)
	require.Len(t, db.Channel.GetX(ctx, ch.ID).Credentials.GetAllAPIKeys(), 3, "删除管理认证不删除调用 Key")
}

func TestManualAccountBalanceUnitsAndDisabledKeys(t *testing.T) {
	now := time.Now()
	ch := &ent.Channel{UpstreamCredentials: &objects.ChannelUpstreamCredentials{Accounts: []objects.UpstreamAccountCredentials{{Ref: "a", Name: "A", Provider: objects.UpstreamProviderSub2API}, {Ref: "b", Name: "B", Provider: objects.UpstreamProviderSub2API}, {Ref: "c", Name: "C", Provider: objects.UpstreamProviderSub2API}}}}
	state := &objects.ChannelUpstreamState{}
	for _, item := range []struct {
		ref, currency string
		disabled      bool
	}{{"a", "USD", false}, {"a", "USD", true}, {"b", "USD", false}, {"c", "CNY", true}, {"", "USD", false}} {
		snapshot := upstream.EmptySnapshot()
		snapshot.BalanceState = objects.UpstreamQuerySuccess
		snapshot.BalanceUpdatedAt = &now
		snapshot.Balances = []objects.UpstreamBalance{{Kind: objects.UpstreamBalanceAccount, Currency: item.currency, Unit: "CURRENCY", Amount: lo.ToPtr(decimal.NewFromInt(20))}}
		state.Keys = append(state.Keys, &objects.ChannelUpstreamKeyState{AccountRef: item.ref, Disabled: item.disabled, Snapshot: snapshot})
	}
	populateUpstreamAccounts(ch, state, now)
	require.Equal(t, 3, state.BalanceSummary.AccountCount)
	require.Equal(t, 1, state.BalanceSummary.UnassignedKeyCount)
	require.False(t, state.BalanceSummary.Complete)
	require.Len(t, state.BalanceSummary.Balances, 2)
	require.Equal(t, "40", state.BalanceSummary.Balances[0].Amount.String(), "同金额的独立账号不能合并，账号A不能重复")
	require.Equal(t, "20", state.BalanceSummary.Balances[1].Amount.String(), "停用 Key 不抹掉账户余额，币种保持独立")
}

func TestChannelAccountsFailedRefreshMarksRetainedBalancePartial(t *testing.T) {
	now := time.Now()
	ch := &ent.Channel{UpstreamCredentials: &objects.ChannelUpstreamCredentials{Accounts: []objects.UpstreamAccountCredentials{{Ref: "a", Provider: objects.UpstreamProviderNewAPI, AccountID: 1}}}}
	for _, queryState := range []objects.UpstreamQueryState{objects.UpstreamQueryError, objects.UpstreamQueryAuthRequired, objects.UpstreamQueryUnmatched} {
		state := &objects.ChannelUpstreamState{Keys: []*objects.ChannelUpstreamKeyState{{AccountRef: "a", Snapshot: &objects.UpstreamKeySnapshot{
			RateState: queryState, Account: &objects.UpstreamAccountSnapshot{AccountID: 1, UpdatedAt: now.Add(-time.Minute), Balance: objects.UpstreamBalance{Amount: lo.ToPtr(decimal.NewFromInt(20)), Currency: "USD", Unit: "CURRENCY"}},
		}}}}
		populateUpstreamAccounts(ch, state, now)
		require.Equal(t, "20", state.BalanceSummary.Balances[0].Amount.String())
		require.False(t, state.BalanceSummary.Complete)
		require.False(t, state.Accounts[0].Complete)
	}
}

func TestChannelAccountsVerificationDoesNotInferFromUnrelatedKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/user/self", r.URL.Path, "新增账号不能拿渠道第一把 Key 的日志推断另一个账号")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"New-Api-User is required"}`))
	}))
	defer server.Close()
	svc, db := setupTestChannelService(t)
	defer db.Close()
	svc.httpClient = httpclient.NewHttpClient()
	ctx := channelAutoPriceTestContext(db)
	ch := createAutoPriceTestChannel(t, ctx, db, "verify-independent-account", []string{"model"})
	ch = ch.Update().SetBaseURL(server.URL).SaveX(ctx)
	result, err := svc.VerifyChannelUpstreamAccount(ctx, ch.ID, lo.ToPtr("fixture-new-account"), nil, nil)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.True(t, result.RequiresAccountID)
}

func TestChannelAccountCreationLinksOnlyUnassignedSingleKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/user/self", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":17,"quota":1000000,"used_quota":0,"group":"standard"}}`))
	}))
	defer server.Close()
	for _, provider := range []objects.UpstreamProvider{objects.UpstreamProviderNewAPI, objects.UpstreamProviderSub2API} {
		t.Run(string(provider), func(t *testing.T) {
			svc, db := setupTestChannelService(t)
			defer db.Close()
			svc.httpClient = httpclient.NewHttpClient()
			ctx := channelAutoPriceTestContext(db)
			for _, scenario := range []string{"single", "multiple", "assigned"} {
				ch := createAutoPriceTestChannel(t, ctx, db, scenario, []string{"model"})
				keys := []string{"fixture-key"}
				if scenario == "multiple" {
					keys = append(keys, "fixture-other")
				}
				setting := objects.ChannelKeySettings{KeyRef: upstream.KeyRef(keys[0]), Remark: "keep", Priority: 7}
				credentials := objects.ChannelUpstreamCredentials{}
				if scenario == "assigned" {
					setting.AccountRef = "existing"
					credentials.Accounts = []objects.UpstreamAccountCredentials{{Ref: "existing", Name: "Existing", Provider: provider}}
				}
				ch = ch.Update().SetBaseURL(server.URL).SetCredentials(objects.ChannelCredentials{APIKeys: keys}).SetSettings(&objects.ChannelSettings{KeySettings: []objects.ChannelKeySettings{setting}}).SetUpstreamCredentials(&credentials).SaveX(ctx)
				result, err := svc.SaveChannelUpstreamAccount(ctx, ch.ID, objects.SaveChannelUpstreamAccountInput{Name: "New", Provider: provider, AccountToken: lo.ToPtr("fixture-account")})
				require.NoError(t, err)
				key := result.Keys[0]
				if scenario == "single" {
					require.Equal(t, result.Accounts[0].Ref, key.AccountRef)
				} else {
					require.Equal(t, setting.AccountRef, key.AccountRef)
				}
				require.Equal(t, 7, key.Priority)
				require.Equal(t, "keep", key.Remark)
			}
		})
	}
}

func TestChannelKeyPriorityPersistsAcrossLegacyUpdates(t *testing.T) {
	svc, db := setupTestChannelService(t)
	defer db.Close()
	ctx := channelAutoPriceTestContext(db)
	ch := createAutoPriceTestChannel(t, ctx, db, "key-priority", []string{"model"})
	ref := upstream.KeyRef(ch.Credentials.GetAllAPIKeys()[0])
	state, err := svc.UpdateChannelKeySettings(ctx, ch.ID, objects.ChannelKeySettingsInput{KeyRef: ref, Priority: lo.ToPtr(-3)})
	require.NoError(t, err)
	require.Equal(t, -3, state.Keys[0].Priority)
	state, err = svc.UpdateChannelKeySettings(ctx, ch.ID, objects.ChannelKeySettingsInput{KeyRef: ref, Remark: "new remark"})
	require.NoError(t, err)
	require.Equal(t, -3, state.Keys[0].Priority)
	require.Equal(t, -3, db.Channel.GetX(ctx, ch.ID).Settings.KeySettingsFor(ref).Priority)
}

func TestSub2APIDefaultAccountSingleKey(t *testing.T) {
	svc, db := setupTestChannelService(t)
	defer db.Close()
	ctx := channelAutoPriceTestContext(db)
	snapshot := upstream.EmptySnapshot()
	snapshot.Provider = objects.UpstreamProviderSub2API
	snapshot.BalanceState = objects.UpstreamQuerySuccess
	for _, count := range []int{1, 2} {
		name := "default-single"
		if count == 2 {
			name = "default-multiple"
		}
		ch := createAutoPriceTestChannel(t, ctx, db, name, []string{"model"})
		keys := []string{"fixture-only"}
		if count == 2 {
			keys = append(keys, "fixture-other")
		}
		ch = ch.Update().SetCredentials(objects.ChannelCredentials{APIKeys: keys}).SaveX(ctx)
		stored, err := svc.ensureDefaultUpstreamAccount(ctx, ch, snapshot, upstream.KeyRef(keys[0]))
		require.NoError(t, err)
		if count == 2 {
			require.Empty(t, stored.Settings.KeySettingsFor(upstream.KeyRef(keys[1])).AccountRef)
		}
		require.Len(t, stored.UpstreamCredentials.Accounts, 1)
		require.Equal(t, "默认账号", stored.UpstreamCredentials.Accounts[0].Name)
		require.Equal(t, stored.UpstreamCredentials.Accounts[0].Ref, stored.Settings.KeySettingsFor(upstream.KeyRef(keys[0])).AccountRef)
		again, err := svc.ensureDefaultUpstreamAccount(ctx, stored, snapshot, upstream.KeyRef(keys[0]))
		require.NoError(t, err)
		require.Equal(t, stored, again)
	}
}

func TestSub2APIDefaultAccountRefreshReadback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/usage":
			_, _ = w.Write([]byte(`{"isValid":true,"balance":25}`))
		case "/v1/sub2api/billing":
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "sub2api.key_billing", "schema_version": 1, "billing_scope": "token", "effective_rate_multiplier": 0.5, "observed_at": time.Now().UTC().Format(time.RFC3339)})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	svc, db := setupTestChannelService(t)
	defer db.Close()
	svc.httpClient = httpclient.NewHttpClient()
	ctx := channelAutoPriceTestContext(db)
	ch := createAutoPriceTestChannel(t, ctx, db, "default-refresh", []string{"model"})
	ch = ch.Update().SetBaseURL(server.URL).SetCredentials(objects.ChannelCredentials{APIKeys: []string{"fixture-one"}}).SaveX(ctx)
	first, err := refreshTestUpstreamChannel(svc, ctx, ch.ID, true, true)
	require.NoError(t, err)
	state := first.Channels[0]
	require.Len(t, state.Accounts, 1)
	require.Equal(t, state.Accounts[0].Ref, state.Keys[0].AccountRef)
	require.Equal(t, "25", state.BalanceSummary.Balances[0].Amount.String())
	require.Equal(t, "0.5", state.Keys[0].UpstreamMultiplier.String())
	reread, err := svc.upstreamState(ctx, db.Channel.GetX(ctx, ch.ID))
	require.NoError(t, err)
	require.Equal(t, state.Accounts[0].Ref, reread.Accounts[0].Ref)
	require.True(t, reread.BalanceSummary.Complete, "账号归属更新后快照指纹必须同步，不能丢掉刚刷新成功的数据")
	second, err := refreshTestUpstreamChannel(svc, ctx, ch.ID, true, true)
	require.NoError(t, err)
	require.Len(t, second.Channels[0].Accounts, 1)
	require.Equal(t, state.Accounts[0].Ref, second.Channels[0].Accounts[0].Ref)
}

func TestWalletDefaultAccountsAndCurrencySummary(t *testing.T) {
	svc, db := setupTestChannelService(t)
	defer db.Close()
	ctx := channelAutoPriceTestContext(db)
	for _, provider := range []objects.UpstreamProvider{objects.UpstreamProviderDeepSeek, objects.UpstreamProviderMoonshot, objects.UpstreamProviderBilling, objects.UpstreamProviderSub2API} {
		ch := createAutoPriceTestChannel(t, ctx, db, string(provider), []string{"model"})
		ch = ch.Update().SetCredentials(objects.ChannelCredentials{APIKeys: []string{"fixture-first", "fixture-second"}}).SaveX(ctx)
		snapshot := upstream.EmptySnapshot()
		snapshot.Provider = provider
		unchanged, err := svc.ensureDefaultUpstreamAccount(ctx, ch, snapshot, upstream.KeyRef("fixture-second"))
		require.NoError(t, err)
		require.Equal(t, ch, unchanged, "未成功查询不能创建账号")
		snapshot.BalanceState = objects.UpstreamQuerySuccess
		ch, err = svc.ensureDefaultUpstreamAccount(ctx, ch, snapshot, upstream.KeyRef("fixture-second"))
		require.NoError(t, err)
		require.Len(t, ch.UpstreamCredentials.Accounts, 1)
		account := ch.UpstreamCredentials.Accounts[0]
		require.True(t, account.AutoCreated)
		require.Empty(t, ch.Settings.KeySettingsFor(upstream.KeyRef("fixture-first")).AccountRef)
		require.Equal(t, account.Ref, ch.Settings.KeySettingsFor(upstream.KeyRef("fixture-second")).AccountRef)
		_, err = svc.ensureDefaultUpstreamAccount(ctx, ch, snapshot, upstream.KeyRef("fixture-first"))
		require.NoError(t, err)
		require.Len(t, db.Channel.GetX(ctx, ch.ID).UpstreamCredentials.Accounts, 1)
	}
	ch := &ent.Channel{UpstreamCredentials: &objects.ChannelUpstreamCredentials{Accounts: []objects.UpstreamAccountCredentials{{Ref: "wallet", Provider: objects.UpstreamProviderDeepSeek}}}}
	now := time.Now()
	balances := []objects.UpstreamBalance{
		{Kind: objects.UpstreamBalanceAccount, Currency: "CNY", Unit: "CURRENCY", Amount: lo.ToPtr(decimal.NewFromInt(100))},
		{Kind: objects.UpstreamBalanceAccount, Currency: "USD", Unit: "CURRENCY", Amount: lo.ToPtr(decimal.NewFromInt(8))},
	}
	state := &objects.ChannelUpstreamState{Keys: []*objects.ChannelUpstreamKeyState{
		{AccountRef: "wallet", Snapshot: &objects.UpstreamKeySnapshot{Balances: balances, BalanceState: objects.UpstreamQuerySuccess, BalanceUpdatedAt: &now}},
		{AccountRef: "wallet", Snapshot: &objects.UpstreamKeySnapshot{Balances: balances, BalanceState: objects.UpstreamQuerySuccess, BalanceUpdatedAt: &now}},
		{Snapshot: upstream.EmptySnapshot()},
	}}
	populateUpstreamAccounts(ch, state, now)
	require.Len(t, state.Accounts[0].Balances, 2)
	require.Len(t, state.BalanceSummary.Balances, 2)
	require.Equal(t, "100", state.BalanceSummary.Balances[0].Amount.String())
	require.Equal(t, "8", state.BalanceSummary.Balances[1].Amount.String())
	require.Equal(t, 1, state.BalanceSummary.UnassignedKeyCount)
	require.False(t, state.BalanceSummary.Complete)
}

func TestWalletUpgradeRedetectsOnlyOldUnknownResults(t *testing.T) {
	svc, db := setupTestChannelService(t)
	defer db.Close()
	ctx := channelAutoPriceTestContext(db)
	ch := createAutoPriceTestChannel(t, ctx, db, "upgrade-wallet", []string{"model"})
	ch = ch.Update().SetCredentials(objects.ChannelCredentials{APIKeys: []string{"fixture-upgrade"}}).SaveX(ctx)
	ref := upstream.KeyRef("fixture-upgrade")
	snapshot := upstream.EmptySnapshot()
	snapshot.BalanceState, snapshot.RateState = objects.UpstreamQueryUnsupported, objects.UpstreamQueryUnsupported
	row := db.ChannelUpstreamStatus.Create().SetChannelID(ch.ID).SetKeyFingerprint(ref).SetConfigFingerprint(upstreamConfigFingerprint(ch, ref)).SetSnapshot(snapshot).SaveX(ctx)
	state, err := svc.upstreamState(ctx, ch)
	require.NoError(t, err)
	require.Equal(t, objects.UpstreamQueryNever, state.Keys[0].Snapshot.BalanceState)
	require.False(t, state.Settings.Enabled, "升级不得开启原本关闭的同步")
	snapshot.QueryVersion = upstream.QueryVersion
	row.Update().SetSnapshot(snapshot).SaveX(ctx)
	state, err = svc.upstreamState(ctx, ch)
	require.NoError(t, err)
	require.Equal(t, objects.UpstreamQueryUnsupported, state.Keys[0].Snapshot.BalanceState, "新版本检测仍不支持时停止自动探测")
	snapshot.QueryVersion = 0
	snapshot.Provider = objects.UpstreamProviderNewAPI
	snapshot.BalanceState = objects.UpstreamQuerySuccess
	row.Update().SetSnapshot(snapshot).SaveX(ctx)
	state, err = svc.upstreamState(ctx, ch)
	require.NoError(t, err)
	require.Equal(t, objects.UpstreamQuerySuccess, state.Keys[0].Snapshot.BalanceState, "已支持来源的旧结果仍然有效")
}
