package upstream

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"

	"github.com/looplj/axonhub/internal/objects"
)

func isNewAPIUsage(data map[string]any) bool {
	_, available := number(data["total_available"])
	return available && text(data["object"]) == "token_usage"
}

func (s *Session) quota(value any) *decimal.Decimal {
	amount, ok := number(value)
	if !ok {
		return nil
	}
	if unit, ok := number(s.Metadata["quota_per_unit"]); ok && unit.IsPositive() {
		amount = amount.Div(unit)
	}
	return &amount
}

func (s *Session) quotaBalance(kind objects.UpstreamBalanceKind, available, used any) objects.UpstreamBalance {
	balance := objects.UpstreamBalance{Kind: kind, Amount: s.quota(available), Used: s.quota(used), Unit: "QUOTA"}
	if unit, ok := number(s.Metadata["quota_per_unit"]); ok && unit.IsPositive() {
		balance.Unit = "CREDIT"
		// USD is the quota base unit. CNY display conversion is not a separate balance.
		if display := text(s.Metadata["quota_display_type"]); display == "USD" || display == "CNY" || s.Metadata["display_in_currency"] == true {
			balance.Currency, balance.Unit = "USD", "CURRENCY"
		}
	}
	return balance
}

func (s *Session) identity(ctx context.Context, key string) (int, int, error) {
	response, err := s.Client.request(ctx, "/api/log/token", key, 0)
	if err != nil {
		return 0, 0, err
	}
	logs, _ := response["data"].([]any)
	var tokenID, accountID int
	for _, entry := range logs {
		row := record(entry)
		tid, uid := positiveID(row["token_id"]), positiveID(row["user_id"])
		if tid == 0 || uid == 0 {
			continue
		}
		if tokenID != 0 && (tokenID != tid || accountID != uid) {
			return 0, 0, &QueryError{Code: "KEY_UNMATCHED"}
		}
		tokenID, accountID = tid, uid
	}
	if tokenID == 0 {
		return 0, 0, &QueryError{Code: "KEY_UNMATCHED"}
	}
	return tokenID, accountID, nil
}

// accountKeyIdentity 在已验证账户内精确查询 Key，新建且尚无调用记录的 Key 也能关联。
func (s *Session) accountKeyIdentity(ctx context.Context, key string) (int, int, error) {
	value := strings.TrimPrefix(key, "sk-")
	// New API 的搜索参数支持通配符；这里只允许完整 Key 的精确匹配。
	if value == "" || strings.ContainsAny(value, "%_") {
		return 0, 0, &QueryError{Code: "KEY_UNMATCHED"}
	}
	response, err := s.Client.requestWithQuery(ctx, "/api/token/search", s.Account.Token, s.Account.ID, url.Values{"token": {value}, "page_size": {"2"}})
	if err != nil {
		return 0, 0, err
	}
	data := response["data"]
	items, ok := data.([]any)
	if !ok {
		items, ok = record(data)["items"].([]any)
	}
	if !ok {
		return 0, 0, &QueryError{Code: "INVALID_RESPONSE"}
	}
	if len(items) != 1 {
		return 0, 0, &QueryError{Code: "KEY_UNMATCHED"}
	}
	item := record(items[0])
	tokenID, accountID := positiveID(item["id"]), positiveID(item["user_id"])
	if tokenID == 0 || accountID != s.Account.ID {
		return 0, 0, &QueryError{Code: "ACCOUNT_MISMATCH"}
	}
	return tokenID, accountID, nil
}

func (s *Session) loadNewAPIAccount(ctx context.Context, token string, accountID int, key string) (*NewAPIAccount, error) {
	response, err := s.Client.request(ctx, "/api/user/self", token, accountID)
	if err != nil && ErrorCode(err) == "ACCOUNT_ID_REQUIRED" && accountID == 0 && key != "" {
		_, inferredID, identityErr := s.identity(ctx, key)
		if identityErr == nil {
			response, err = s.Client.request(ctx, "/api/user/self", token, inferredID)
		}
	}
	if err != nil {
		return nil, err
	}
	data := record(response["data"])
	id := positiveID(data["id"])
	if id == 0 || (accountID != 0 && id != accountID) {
		return nil, &QueryError{Code: "ACCOUNT_MISMATCH"}
	}
	balance := s.quotaBalance(objects.UpstreamBalanceAccount, data["quota"], data["used_quota"])
	if balance.Amount == nil {
		return nil, &QueryError{Code: "INVALID_RESPONSE"}
	}
	balance.AccountRef = s.Client.AccountRef(id)
	return &NewAPIAccount{ID: id, Group: text(data["group"]), Token: token, Balance: balance, SyncedAt: time.Now().UTC()}, nil
}

func (c *Client) VerifyAccount(ctx context.Context, token string, accountID int, key string) *objects.UpstreamAccountVerification {
	session := &Session{Client: c}
	account, err := session.loadNewAPIAccount(ctx, token, accountID, key)
	if err != nil {
		return &objects.UpstreamAccountVerification{ErrorCode: ErrorCode(err), RequiresAccountID: ErrorCode(err) == "ACCOUNT_ID_REQUIRED"}
	}
	return &objects.UpstreamAccountVerification{Success: true, AccountID: lo.ToPtr(account.ID)}
}

func (s *Session) newAPIBalance(ctx context.Context, key string) ([]objects.UpstreamBalance, error) {
	response, err := s.Client.request(ctx, "/api/usage/token/", key, 0)
	if err != nil {
		return nil, err
	}
	data := record(response["data"])
	if !isNewAPIUsage(data) {
		return nil, &QueryError{Code: "INVALID_RESPONSE"}
	}
	balance := s.quotaBalance(objects.UpstreamBalanceKey, data["total_available"], data["total_used"])
	balance.Limit = s.quota(data["total_granted"])
	balance.Unlimited = data["unlimited_quota"] == true || (balance.Limit != nil && balance.Limit.IsNegative())
	if balance.Unlimited {
		balance.Amount, balance.Limit = nil, nil
	}
	balance.ExpiresAt = timestamp(data["expires_at"])
	balances := []objects.UpstreamBalance{balance}
	if userQuota := record(data["user_quota"]); userQuota != nil {
		// No account identity is returned by this endpoint; never merge by equal amounts.
		balances = append(balances, s.quotaBalance(objects.UpstreamBalanceAccount, userQuota["total_available"], userQuota["total_used"]))
	}
	return balances, nil
}

func (s *Session) newAPIRate(ctx context.Context, key string, previous *objects.UpstreamKeySnapshot) (*objects.UpstreamRateSnapshot, int, int, error) {
	if s.Account == nil {
		code := s.AccountErrorCode
		if code == "" {
			code = "ACCOUNT_NOT_CONNECTED"
		}
		return nil, 0, 0, &QueryError{Code: code}
	}
	tokenID, accountID := previous.UpstreamTokenID, previous.UpstreamAccountID
	if tokenID == 0 || accountID == 0 {
		var err error
		tokenID, accountID, err = s.accountKeyIdentity(ctx, key)
		if err != nil {
			return nil, 0, 0, err
		}
	}
	if accountID != s.Account.ID {
		return nil, 0, 0, &QueryError{Code: "ACCOUNT_MISMATCH"}
	}
	response, err := s.Client.request(ctx, "/api/token/"+strconv.Itoa(tokenID), s.Account.Token, accountID)
	if err != nil {
		return nil, 0, 0, err
	}
	data := record(response["data"])
	if positiveID(data["id"]) != tokenID || positiveID(data["user_id"]) != accountID {
		return nil, 0, 0, &QueryError{Code: "KEY_UNMATCHED"}
	}
	group := text(data["group"])
	if group == "auto" {
		return nil, tokenID, accountID, &QueryError{Code: "AUTO_GROUP_UNSUPPORTED"}
	}
	if group == "" {
		group = s.Account.Group
	}
	if s.Account.Groups == nil {
		groups, groupErr := s.Client.request(ctx, "/api/user/self/groups", s.Account.Token, accountID)
		if groupErr != nil {
			return nil, tokenID, accountID, groupErr
		}
		s.Account.Groups = record(groups["data"])
	}
	ratio, ok := number(record(s.Account.Groups[group])["ratio"])
	if group == "" || !ok || ratio.IsNegative() {
		return nil, tokenID, accountID, &QueryError{Code: "UNSUPPORTED_RATE_SCHEMA"}
	}
	now := time.Now().UTC()
	return &objects.UpstreamRateSnapshot{Multiplier: ratio, Group: group, ObservedAt: now, SyncedAt: now}, tokenID, accountID, nil
}
