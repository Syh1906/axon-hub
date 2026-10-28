package upstream

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm/httpclient"
)

const RequestTimeout = 8 * time.Second

type Client struct {
	http *httpclient.HttpClient
	base *url.URL
}

// Session 仅在同一账号的 Key 之间共享站点信息和账号认证。
type Session struct {
	Client           *Client
	Provider         objects.UpstreamProvider
	Metadata         map[string]any
	Account          *NewAPIAccount
	AccountErrorCode string
}

type NewAPIAccount struct {
	ID       int
	Group    string
	Token    string
	Balance  objects.UpstreamBalance
	Groups   map[string]any
	SyncedAt time.Time
}

type QueryError struct {
	Code   string
	Status int
}

func (e *QueryError) Error() string { return e.Code }

func NewClient(client *httpclient.HttpClient, baseURL string) (*Client, error) {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || base == nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" || base.User != nil {
		return nil, &QueryError{Code: "INVALID_BASE_URL"}
	}
	base.RawQuery, base.Fragment = "", ""
	base.Path = strings.TrimRight(base.Path, "/")
	base.Path = strings.TrimSuffix(base.Path, "/v1")
	base.RawPath = ""
	return &Client{http: client.WithRejectHTTPSDowngrade(), base: base}, nil
}

func KeyRef(key string) string {
	hash := sha256.Sum256([]byte("axonhub:upstream:key:v1:" + key))
	return hex.EncodeToString(hash[:])
}

func MaskKey(key string) string {
	if len(key) <= 4 {
		return "••••"
	}
	return "••••" + key[len(key)-4:]
}

func (c *Client) AccountRef(id int) string {
	hash := sha256.Sum256([]byte(c.base.String() + ":" + strconv.Itoa(id)))
	return hex.EncodeToString(hash[:])
}

func (c *Client) request(ctx context.Context, path, key string, accountID int) (map[string]any, error) {
	return c.requestWithQuery(ctx, path, key, accountID, nil)
}

func (c *Client) requestWithQuery(ctx context.Context, path, key string, accountID int, query url.Values) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	endpoint := *c.base
	endpoint.Path += path
	endpoint.RawQuery = query.Encode()
	builder := httpclient.NewRequestBuilder().WithMethod(http.MethodGet).WithURL(endpoint.String()).WithHeader("Accept", "application/json")
	if key != "" {
		builder.WithBearerToken(key)
	}
	if accountID > 0 {
		builder.WithHeader("New-Api-User", strconv.Itoa(accountID))
	}
	response, err := c.http.Do(ctx, builder.Build())
	if err != nil {
		if responseError, ok := errors.AsType[*httpclient.Error](err); ok {
			code := "UPSTREAM_UNAVAILABLE"
			switch responseError.StatusCode {
			case http.StatusUnauthorized:
				code = "UNAUTHORIZED"
			case http.StatusForbidden:
				code = "FORBIDDEN"
			case http.StatusNotFound, http.StatusMethodNotAllowed:
				code = "NOT_FOUND"
			case http.StatusTooManyRequests:
				code = "RATE_LIMITED"
			}
			if accountID == 0 && bytes.Contains(bytes.ToLower(responseError.Body), []byte("new-api-user")) {
				code = "ACCOUNT_ID_REQUIRED"
			}
			return nil, &QueryError{Code: code, Status: responseError.StatusCode}
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, &QueryError{Code: "TIMEOUT"}
		}
		return nil, &QueryError{Code: "NETWORK_ERROR"}
	}
	var data map[string]any
	decoder := json.NewDecoder(bytes.NewReader(response.Body))
	decoder.UseNumber()
	if err := decoder.Decode(&data); err != nil || data == nil {
		return nil, &QueryError{Code: "INVALID_RESPONSE"}
	}
	if data["success"] == false || data["code"] == false {
		code := "UPSTREAM_REJECTED"
		if accountID == 0 && strings.Contains(strings.ToLower(text(data["message"])), "new-api-user") {
			code = "ACCOUNT_ID_REQUIRED"
		}
		return nil, &QueryError{Code: code}
	}
	return data, nil
}

func (c *Client) Prepare(ctx context.Context, key string, known objects.UpstreamProvider, credentials *objects.UpstreamAccountCredentials) *Session {
	session := &Session{Client: c, Provider: known}
	if session.Provider == "" {
		session.Provider = objects.UpstreamProviderUnknown
	}
	recordDetectionError := func(err error) {
		if err != nil {
			if code := ErrorCode(err); code != "NOT_FOUND" && code != "INVALID_RESPONSE" {
				session.AccountErrorCode = code
			}
		}
	}
	// 公开站点信息用于识别 New API，同时取得真实额度单位。
	if session.Provider == objects.UpstreamProviderUnknown || session.Provider == objects.UpstreamProviderNewAPI {
		metadata, err := c.request(ctx, "/api/status", "", 0)
		recordDetectionError(err)
		if err == nil {
			data := record(metadata["data"])
			if _, ok := number(data["quota_per_unit"]); ok {
				session.Provider = objects.UpstreamProviderNewAPI
				session.Metadata = data
			}
		}
	}
	if session.Provider == objects.UpstreamProviderUnknown {
		usage, err := c.request(ctx, "/api/usage/token/", key, 0)
		recordDetectionError(err)
		if err == nil && isNewAPIUsage(record(usage["data"])) {
			session.Provider = objects.UpstreamProviderNewAPI
		}
	}
	if session.Provider == objects.UpstreamProviderUnknown {
		usage, err := c.request(ctx, "/v1/usage", key, 0)
		recordDetectionError(err)
		if err == nil {
			if _, ok := usage["isValid"].(bool); ok {
				session.Provider = objects.UpstreamProviderSub2API
			}
		}
	}
	if session.Provider != objects.UpstreamProviderUnknown {
		session.AccountErrorCode = ""
	}
	if session.Provider == objects.UpstreamProviderNewAPI && credentials != nil && credentials.AccountToken != "" {
		account, err := session.loadNewAPIAccount(ctx, credentials.AccountToken, credentials.AccountID, key)
		if err != nil {
			session.AccountErrorCode = ErrorCode(err)
		} else {
			session.Account = account
		}
	}
	return session
}

func ErrorCode(err error) string {
	if queryError, ok := errors.AsType[*QueryError](err); ok {
		return queryError.Code
	}
	return "QUERY_FAILED"
}

func ErrorState(err error) objects.UpstreamQueryState {
	switch ErrorCode(err) {
	case "NOT_FOUND", "UNSUPPORTED", "UNSUPPORTED_RATE_SCHEMA", "AUTO_GROUP_UNSUPPORTED":
		return objects.UpstreamQueryUnsupported
	case "UNAUTHORIZED", "FORBIDDEN", "ACCOUNT_ID_REQUIRED", "ACCOUNT_NOT_CONNECTED", "UPSTREAM_REJECTED":
		return objects.UpstreamQueryAuthRequired
	case "KEY_UNMATCHED", "ACCOUNT_MISMATCH":
		return objects.UpstreamQueryUnmatched
	default:
		return objects.UpstreamQueryError
	}
}

func record(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func text(value any) string {
	result, _ := value.(string)
	return result
}

func number(value any) (decimal.Decimal, bool) {
	var valueString string
	switch value := value.(type) {
	case json.Number:
		valueString = string(value)
	case string:
		valueString = value
	default:
		return decimal.Zero, false
	}
	result, err := decimal.NewFromString(valueString)
	return result, err == nil
}

func optionalNumber(value any) *decimal.Decimal {
	result, ok := number(value)
	if !ok {
		return nil
	}
	return &result
}

func positiveID(value any) int {
	n, ok := number(value)
	if !ok || !n.IsPositive() || !n.Equal(n.Truncate(0)) || n.GreaterThan(decimal.NewFromInt(2147483647)) {
		return 0
	}
	return int(n.IntPart())
}

func timestamp(value any) *time.Time {
	if n, ok := number(value); ok && n.IsPositive() {
		seconds := n.IntPart()
		if seconds > 1_000_000_000_000 {
			seconds /= 1000
		}
		result := time.Unix(seconds, 0).UTC()
		return &result
	}
	if parsed, err := time.Parse(time.RFC3339Nano, text(value)); err == nil && parsed.Year() > 1 {
		return &parsed
	}
	return nil
}
