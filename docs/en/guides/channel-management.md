# Channel Configuration Guide

This guide explains how to configure AI provider channels (like OpenAI, Anthropic, DeepSeek) in AxonHub.

## Tag priorities and quick list settings

The channel list shows tags and displays **Priority**, **Weight**, and **Cost multiplier** together by default. Clicking a priority opens a compact popover with the global value above all of the channel's tag overrides. A list filter never hides or disables other tags in this editor. Empty tag values inherit the global value; an empty global value inherits model associations. Save writes all values atomically. Cancel, Escape, or clicking outside discards drafts. Long tag lists scroll while the title and actions remain visible.

Without a single-tag filter, the list shows the global value. Channels without a global value retain their previous display: the smallest configured priority on their own tags. A single-tag filter shows its override or inherited global value; **Inherit** means neither is configured. This display does not broaden request tag scope.

**Priority and weight** saves both columns for a tag in one transaction. Global and tag priorities accept integers from -2147483648 to 2147483647 and may repeat; smaller values are tried first (for example, -1 precedes 0). An empty priority or **Reset all priorities** restores only priority inheritance and preserves channel weight. Weight is shared across the channel, with larger values first. Quick multiplier changes retain existing model cost items and create the normal price-history version.

Effective priority is **API Key override → matching tag default → channel global value → model association**. Key tags take precedence over project tags. Requests without tags also use the global value, falling back to model associations when unset. Exclusion-mode tags provide no tag defaults. Overlapping tags use the smallest explicitly configured tag priority, then the global value when none is configured. For example, with global 10, tag A set to 14 and tag B empty, A uses 14, B uses 10, and A+B uses 14. Only already permitted, model-compatible candidates are reordered. Availability, failure backoff, and existing session reuse still apply; the configured load balancer chooses among equal priorities and weights.

Column headers temporarily change list order. **Default sorting** stores a field and direction in the current browser for subsequent visits. Priority sorting uses the displayed value described above. Entries inheriting model associations follow configured numbers. Priority and multiplier sorting covers the entire filtered result before pagination. List order never changes request routing.

## Add supported models from model management

In the channel create/edit form, click **Add from model list** below supported models. Search, select individual entries, or select/clear the current filtered results. Already supported IDs cannot be added twice. Exact models expose their model ID; rule models expose only concrete configured public IDs. Rules without public IDs require manual concrete names, never the rule expression itself.

**Add selected** updates the form; saving the channel persists the change. Entries are manual models and survive automatic synchronization. This does not create model associations or verify provider support.

## Upstream balances and per-key rates

Click **Upstream balance** in the channel list, then open **Sync settings**. Existing channels start with queries disabled. The URL, proxy and API keys are reused from channel configuration; supported endpoints are detected automatically.

| Provider | Additional configuration | Query support |
| --- | --- | --- |
| New API | No account is required for key limits. Current group rates require an account access token. The account ID is filled automatically when possible; otherwise the form asks for it. | Key limits, verified account balance and the user-specific rate of the key's current group |
| Sub2API | No additional account token or ID | Wallet balance, key limits or subscription allowance; versions exposing `/v1/sub2api/billing` also return the current effective token multiplier |
| Official DeepSeek | Reuse the channel URL and key | Wallet balances from `/user/balance`, preserving every currency; no group rate |
| Moonshot / Kimi API platform | Reuse the channel URL and key | Available balance from `/v1/users/me/balance`; CNY for the Chinese endpoint and USD for the international endpoint; no group rate |
| Compatible Billing gateway | Reuse the channel URL and key | Direct balance, or limit and usage; remaining allowance is calculated only for an explicitly matching period; no group rate |
| Unsupported or older versions | Keep the manual multiplier | Unsupported endpoints stop automatic attempts and can be detected again manually |

One channel represents one upstream, group or plan, and multiplier. Its keys may belong to different accounts. Put different rates in separate channels; key selection inside a channel does not prefer the lowest rate.

Add accounts under **Accounts and authentication**, then assign keys in **Key management**. New API account access tokens come from the upstream personal settings and are distinct from calling keys. Saving verifies the account; its keys share that authentication. Key lookup runs directly within the authorized account, including newly created keys without usage logs. Unverified identity or rate information stays explicit; names, suffixes and matching amounts are not identity evidence. Sub2API needs no additional token, and only explicitly returned account balances are totaled. Creating a New API or Sub2API account automatically assigns the sole unassigned channel key to that account; existing assignments are preserved. For Sub2API, DeepSeek, Moonshot and compatible Billing, the first successful query creates a default local account if no account or legacy authentication exists, and assigns only the queried key. Other keys retain their assignments or remain unassigned. This local grouping does not establish a remote account ID. New API retains its account authorization flow.

Saved New API tokens and channel keys can be revealed, hidden and copied through explicit actions requiring channel management permission. Tokens change only when replaced; reopening or verifying a connection does not require re-entry. Missing account authentication or assignments affect balance and rate queries without blocking otherwise valid model calls.

Selecting an account in Key management immediately saves that assignment. New API account balances are queried independently using that account's authentication. Key search failures, unsupported groups or missing rates do not undo your selection or hide a retrieved account balance. Failed group checks are labeled **Group unconfirmed**, rather than **Unmatched**, and use the manual multiplier. If account authentication itself fails, the previous balance is retained with a stale status and the error; multiple keys still count the same account only once.

The main list displays account totals and, for multiple keys, the actual key count. Hover previews up to three keys; clicking opens the full details. Each account is counted once, key quotas are excluded, and different currencies remain separate. Failed, stale or unassigned results are marked as partial or unavailable. Disabling a key does not erase its known account balance. Unknown units remain raw quota units.

Official wallet hostnames select their native endpoint directly, removing inference path suffixes such as `/v1` and `/anthropic`. Gateways preserve their mount path and are detected from response structure, not model names. Old unsupported unknown-source snapshots are eligible for detection by the new version; disabled sync stays disabled.

The three new sources query balances independently of rates and use the manual cost multiplier. Unassigned wallet results remain visible in key details but are excluded from account totals. Billing responses with unspecified ownership scope are shown per key as upstream allowance, not added to wallet totals; a single-key channel can show this allowance in the list. If the limit period is unavailable, details show the calendar-year usage query range, limit and usage without subtracting them. A period with time-of-day or timezone offsets must exactly match the usage request's UTC date boundaries before a remainder can be calculated; the displayed usage range reflects the dates actually sent. Unlimited allowance is not unlimited wallet money.

**Key management** includes labels, account assignments, key priorities, individual enable/disable, reveal/copy, bulk import, tests and deletion. New channels can contain multiple keys, which are queried individually after saving. Each management action takes effect when submitted; at least one key remains enabled, and disabling a key does not interrupt in-flight requests. Click a priority number to edit its integer value. The default is 0, and lower values take precedence. Selection first narrows enabled keys to the lowest priority value. **Reuse session key** reuses a key only within that group; disabling reuse selects randomly within the group for each request. Channel-level session routing is unchanged.

- Opening Channels or changing pages displays cached data first and queries enabled channels only when due, every 10 minutes by default.
- The row refresh button queries one channel immediately; the details view can query one key. **Query once** remains available with sync disabled.
- **Refresh current page** only refreshes channels shown on the current filtered page, skipping disabled sync. It does not query other pages and is disabled while replacement filter results are pending or empty. Entering another page queries only due results and reuses fresh snapshots. Unsupported key endpoints do not prevent querying a configured New API account balance.
- Keys refresh sequentially within each channel, with up to three channels in parallel. A failed key does not stop the remaining keys or roll back successful results. The page reports failures so they can be refreshed again.
- Enabled channels following upstream rates also synchronize rates in the background. Query failures do not affect channel availability or other keys.
- Saving with sync disabled switches to manual mode and retains the previous results. Temporary failures may retry when due. Authentication failures and unsupported endpoints stop automatic attempts until configuration is corrected or a manual refresh is requested.

Manual mode always uses the manual value; refreshing only updates the reference. Following mode applies the actual selected key's multiplier. A channel normally shows one rate; inconsistent groups or rates remain visible in details with a prompt to split channels. Saving a quick multiplier edit switches back to manual mode. List sorting uses the currently applied rate, taking the minimum only when an inconsistent range exists; it does not affect request routing.

Successful rates survive temporary failures for at most 30 minutes. Expiry or a known upstream time boundary switches to the manual fallback, marked as estimated. Price, request time and multiplier are fixed for each attempt; see [cost tracking](cost-tracking.md#fixed-per-key-cost-multipliers).

## Multiple upstream addresses and interface paths

While creating or editing a channel, choose **Manage addresses** below Base URL. Add primary and backup addresses for the same upstream, each with a name, enabled state and priority. Smaller priorities are tried first, and at least one address must remain enabled. Changes save with the channel; cancel discards the draft. Connectivity tests originate from the AxonHub server without generating model output or changing priorities. An HTTP response does not prove credentials or model availability.

With **Automatic failover** enabled, connection failures, response timeouts and 5xx responses can move a model request to the next address while keeping its model and key. Attempts consume the existing same-channel retry budget and overall deadline. HTTP 401, 403 and 429 do not trigger address changes. Exhausting addresses returns control to normal channel handling. Streams are not replayed after content reaches the client. Account synchronization still uses the channel Base URL.

**Interfaces and paths** combines default and custom interfaces, showing paths, selected addresses and final URLs. Interfaces inherit the channel address list unless an explicit interface-specific address is configured. Per-model interface settings remain in the advanced section.

Data migration `v1.0.0-beta10-fork.7.syh.5` turns ordinary legacy single addresses into the first address and retains existing authentication as an account. Only keys with proven ownership in historical snapshots are assigned automatically. Backup restore preserves authentication, labels, assignments and address settings; query caches are excluded.

Regular channel rows are 88px high. Model summaries show at most two lines plus `+N`; hover or focus to preview full names, or click the summary to open the existing model editor. Tags are also bounded. Creation time is hidden by default and can be enabled in column settings. Priority, weight and multiplier editors focus and select the numeric input immediately.

A **channel** is AxonHub's connection to an AI provider. Think of it as a "provider connection line" — each channel connects to one AI service (like OpenAI, Claude, or DeepSeek).

Through channels, you can:
- Connect to multiple AI providers simultaneously
- Set up model name conversion rules
- Enable or disable providers
- Configure multiple API Keys for load balancing

## Where Channel Model Mapping Fits in the Request Flow

Channel model mapping is the **last** step in a three-layer pipeline. For the full picture, see [Request Processing Guide](../getting-started/request-processing.md#core-concept-three-layers-of-model-settings).

In short: **API Key Profile renames → Model Association selects channel → Channel renames → Send upstream**

## Creating a Channel

### Basic Steps

1. Go to AxonHub management interface → **Channel Management**
2. Click **New Channel**
3. Fill in basic information:
   - **Name**: Give your channel a name (e.g., "OpenAI Main", "DeepSeek Backup")
   - **Type**: Select provider type (OpenAI, Anthropic, DeepSeek, etc.)
   - **Base URL**: API address (usually use the default)
   - **API Key**: The key from your provider

### Configuration Examples

**OpenAI Channel:**

| Field | Value |
|-------|-------|
| Name | OpenAI Main |
| Type | openai |
| Base URL | https://api.openai.com/v1 |
| API Key | sk-your-openai-key |
| Supported Models | gpt-4o, gpt-4o-mini, gpt-5 |

**DeepSeek Channel:**

| Field | Value |
|-------|-------|
| Name | DeepSeek China |
| Type | deepseek |
| Base URL | https://api.deepseek.com/v1 |
| API Key | sk-your-deepseek-key |
| Supported Models | deepseek-chat, deepseek-reasoner |

## Multiple API Keys

Multiple API keys for the same upstream, group or plan, and multiplier can share one channel. Assign keys from different accounts to their respective accounts. Put keys with different multipliers in separate channels.

Simply add all keys in the API Keys field, one per line:
```
sk-key-1
sk-key-2
sk-key-3
```

### Load Balancing

- Selection first narrows enabled keys to the lowest priority value; priorities default to 0 and lower values take precedence.
- With session key reuse enabled, the same Trace ID prefers its previous key while that key remains enabled and belongs to the current best-priority group.
- With reuse disabled or no Trace ID, each request randomly selects a key within that group; selection is not round-robin.
- Disabled keys are excluded from subsequent requests. Switching to a backup address within a request keeps the already selected key.

## Model Renaming

AxonHub provides multiple mechanisms to rename or alias models at the channel level. When a request arrives, the channel resolves the request model to the actual upstream model through the following priority chain:

1. **Direct match** — the request model is directly in the Supported Models list
2. **Extra Model Prefix** — adds a prefix alias for all supported models
3. **Auto-Trimmed Model Prefixes** — strips known prefixes from supported models to create trimmed aliases
4. **Model Mappings** — explicit `from → to` alias pairs

> **Note**: If multiple mechanisms produce the same request model name, the first match wins (based on the order above).

### Model Mappings

**When do you need model mappings?**

When you want the client to use one name, but send a different name to the upstream provider.

**Common Scenarios:**

1. **Client uses simplified names**: Client requests `gpt-4`, but OpenAI receives `gpt-4o`
2. **Unify model names across channels**: Both `claude-sonnet` and `gpt-4` point to the same actual model
3. **Legacy compatibility**: Old model names automatically map to newer versions

**How to Configure:**

In the channel's **Settings** → **Model Mappings**, add `from → to` pairs:

| From (Client Requests) | To (Sent to Provider) |
|------------------------|----------------------|
| gpt-4o-mini | gpt-4o |
| claude-3-sonnet | claude-3.5-sonnet |

**Note**: The target model (`to`) must be in the Supported Models list. If the target model is not supported, the mapping is silently ignored.

### Extra Model Prefix

Adds a **prefixed alias** for every model in the Supported Models list, allowing clients to request models with or without the prefix.

**Use case**: You want to namespace all models in a channel under a common prefix (e.g., `deepseek/`).

**Example:**
- Supported Models: `deepseek-chat`, `deepseek-reasoner`
- Extra Model Prefix: `deepseek`

The channel now accepts **both** of the following request formats:
- `deepseek-chat` → sends `deepseek-chat` upstream
- `deepseek/deepseek-chat` → sends `deepseek-chat` upstream

This is useful when you want to differentiate models from different channels that share the same name — clients can prefix the model with the channel's namespace.

### Auto-Trimmed Model Prefixes

Automatically **strips specified prefixes** from model names in the Supported Models list, creating trimmed aliases. This is the inverse of Extra Model Prefix.

**Use case**: Providers like OpenRouter or SiliconFlow add vendor prefixes to model names (e.g., `openai/gpt-5.4`). You want clients to request using the short name `gpt-5.4` without manually creating mappings for every model.

**Example:**
- Supported Models: `openai/gpt-5.4`, `anthropic/claude-sonnet-4`, `deepseek-ai/deepseek-chat`
- Auto-Trimmed Model Prefixes: `openai`, `anthropic`, `deepseek-ai`

The channel now accepts both the original and trimmed names:
- `gpt-5.4` → sends `openai/gpt-5.4` upstream
- `claude-sonnet-4` → sends `anthropic/claude-sonnet-4` upstream
- `deepseek-chat` → sends `deepseek-ai/deepseek-chat` upstream
- `openai/gpt-5.4` → still works as a direct match

> **Tip**: This is the recommended approach for providers that use vendor-prefixed model IDs. It enables batch model ID rewriting without having to create individual model mappings.

### Lowercase Model Name

When enabled, model name matching keys are converted to lowercase. Enable when channel-provided model names contain uppercase letters, to balance load with other lowercase model names. The actual model name sent to the provider retains its original casing.

**Example**: Converts `GLM-5.1` to `glm-5.1`, allowing the channel to share load with other channels that provide `glm-5.1`.

> **Note**: When enabled, model names that differ only in case (e.g., `GPT-4` and `gpt-4`) are treated as the same model. In case of collision, the highest-priority entry wins (direct > auto_trim > mapping > prefix).

### Visibility Controls

Two options control which model names are exposed in the model list (e.g., when clients call the `/v1/models` endpoint):

| Option | Effect |
|--------|--------|
| **Hide Original Models** | Hides the original (direct) model names. Only transformed names (from prefix, auto-trim, or mapping) are shown. |
| **Hide Mapped Models** | Hides the `from` names of model mappings. Only the original model names are shown. |

**Example — Hide Original Models:**
- Supported Models: `openai/gpt-5.4`
- Auto-Trimmed Prefixes: `openai`
- Hide Original Models: enabled

The `/v1/models` response only shows `gpt-5.4`, not `openai/gpt-5.4`. Both names still work for requests.

**Example — Hide Mapped Models:**
- Supported Models: `gpt-4o`
- Model Mapping: `gpt-4` → `gpt-4o`
- Hide Mapped Models: enabled

The `/v1/models` response shows `gpt-4o` but hides `gpt-4`. Both names still work for requests.

## Testing and Enabling Channels

### Test Connection

Before enabling a channel, test the connection:

1. Find your channel in the channel list
2. Click the **Test** button
3. Wait for the result
4. If successful, proceed to enable

### Enable Channel

After testing passes, click **Enable**. The channel status changes to **Active** and can now receive requests.

## Base URL Special Configuration

### Default URLs

| Provider | Default Base URL |
|----------|-----------------|
| OpenAI | `https://api.openai.com/v1` |
| Anthropic | `https://api.anthropic.com` |
| DeepSeek | `https://api.deepseek.com/v1` |
| Gemini | `https://generativelanguage.googleapis.com/v1beta` |

### Custom URLs

For proxies or private deployments, you can modify the Base URL.

**Disable automatic version appending**: Add `#` at the end
```
https://custom-proxy.example.com/api#
# Actual request: /api/messages (no /v1 added)
```

**Fully raw mode**: Add `##` at the end
```
https://custom-gateway.example.com/api##
# Actual request: /api (no version or endpoint added)
```

## Multi-Protocol Endpoints and Model Protocol Overrides

A single channel can host multiple outbound endpoints covering chat, responses, messages and other protocols, so different clients connect with their native protocol.

### Endpoint Configuration

Manage endpoints in the channel **Endpoints** dialog:

- Each endpoint consists of `api_format`, an optional `base_url` (inherits the channel Base URL when empty), and an optional `path` (replaces the protocol's default path; version appending is skipped when set).
- Each channel type ships with built-in default endpoints (e.g. zhipu provides `openai/chat_completions`, `zhipu_anthropic` provides `anthropic/messages`); custom endpoints can add other formats or override same-named ones.

### Model Protocol Overrides (ModelProtocols)

Force the available outbound protocols for a single model in the **Model Protocol Overrides** block:

```json
{ "model": "glm-5.3-flash", "apiFormats": ["openai/responses", "openai/chat_completions"], "enabled": true }
```

- `apiFormats` expresses priority in configuration order; when the client's inbound protocol is in the list it connects directly, otherwise the first listed protocol is used through the unified conversion pipeline.
- Enabled overrides may only reference `api_format`s the channel already provides (default or custom endpoints), validated on save; overrides are cleaned up automatically when a model is removed from the channel's supported list.
- Endpoints and protocol overrides are committed atomically in a single save.

**Example** (GLM Coding Plan channel, three endpoints + override):

| endpoint | api_format | base_url |
|---|---|---|
| 1 | `openai/chat_completions` | `https://open.bigmodel.cn/api/coding/paas/v4` |
| 2 | `openai/responses` | `https://open.bigmodel.cn/api/v1` |
| 3 | `anthropic/messages` | inherits channel Base URL (`https://open.bigmodel.cn/api/anthropic`) |

| Client | Inbound protocol | Selected outbound | Converted? |
|---|---|---|---|
| Claude Code | `anthropic/messages` | `anthropic/messages` | No |
| Codex | `openai/responses` | `openai/responses` | No |
| OpenAI SDK | `openai/chat_completions` | `openai/chat_completions` | No |
| Codex (override limited to chat) | `openai/responses` | `openai/chat_completions` | Yes |

### Provider Family Routing

Custom chat endpoints select the channel family's native transformer instead of the generic OpenAI one (the generic transformer assumes `/v1` and produces wrong URLs for non-`/v1` upstreams):

| Channel types | Transformer | URL version |
|---|---|---|
| zhipu / zai (incl. `*_anthropic`) | zai | v4 |
| xiaomi (incl. `xiaomi_anthropic`) | zai | v1 |
| doubao / volcengine (incl. `*_anthropic`) | doubao | v3 |

Family transformers also apply provider dialect handling: strip `metadata`, clamp `user_id` to 6–128 characters, and convert `reasoning_effort` to `thinking.type`.

## Reasoning Effort

Unified effort values: `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`.

### Channel Mapping (ReasoningEffortMapping)

Configure `{from, to}` entries in the channel **Transform Options**; the first matching `from` wins. The mapping is applied once before outbound conversion and affects every client and every outbound protocol (chat / responses / messages), including the native effort marker in Anthropic metadata.

### Conversion Rules

- Anthropic messages: the client's native expression wins (`adaptive` / `budget_tokens` round-trip verbatim); explicit effort passes through unchanged (including `max` and `xhigh`); `minimal` is normalized to `low` (the only automatic conversion).
- OpenAI chat / responses: `reasoning_effort` / `reasoning.effort` pass through unchanged.
- If the upstream rejects a level, its original error is forwarded to the client; incompatible levels can be remapped via the channel mapping.

## Force 1-Hour Prompt Cache

Anthropic prompt caches expire after 5 minutes by default. In human-agent collaboration, gaps between requests often exceed 5 minutes while waiting for a reply or stepping away, and the whole prefix must then be written again. With **Force 1-Hour Prompt Cache** checked in the channel edit dialog, every existing cache breakpoint in Anthropic Messages requests sent to that channel (`tools`, `system`, message content including `tool_result` content, and the top-level `cache_control`) is set to `ttl: "1h"`, including breakpoints added by AxonHub and pass-through request bodies.

- Only the TTL of existing breakpoints changes; no breakpoints are added, and requests without prompt caching are unchanged.
- Only requests sent in Anthropic Messages format are affected; other outbound formats are not.
- No `anthropic-beta` header is added; add one with a header override if the upstream requires it.
- Whether the upstream actually caches for 1 hour is up to the upstream. Billing follows the 5-minute / 1-hour cache write split reported in the upstream response, not the request; responses reporting only a total are billed at the base cache write price. See [Cost Tracking](cost-tracking.md#cache-write-variants-anthropic) for 1-hour write pricing.
- In request logs, the model column shows a 1-hour cache icon after the outbound protocol and pass-through icons. It lights up when the request's billing includes 1-hour cache writes and stays gray otherwise, so it always matches billing and does not depend on this switch. In that case the Usage card in request details also shows a "1h cache" badge. Requests that only read an existing 1-hour cache without writing do not light it up.

## Channel request settings

Open **Request settings** from a channel row's action menu.

- **Stream first response timeout** waits for the first upstream stream event. It does not limit later event gaps or specifically measure the first visible token.
- **Non-stream response timeout** waits for the complete non-streaming response.
- **Do not reuse session channel** prevents session-based preference or ownership binding to this channel. Normal selection can still choose it.

Leave timeouts empty to inherit the system setting, enter `0` to remove that channel-specific timeout, or enter a positive integer in seconds.
The overall request deadline still applies. Every attempt uses the selected channel's values, including after failover.
Explicit channel timeouts work even when retries are disabled; inherited system values retain their existing retry-policy activation rule.
Once content has been sent to the client, a timeout does not replay the request through another channel.

The session switch covers both ordinary stickiness and health-gated ownership. It does not disable Responses `previous_response_id` context restoration,
exclude the channel, or guarantee a different channel on every request. It is off by default.

## Streaming Pass-Through and Retry

With retry enabled, stream metadata stays private until the first meaningful content event.
Pass-through buffers raw events during this pre-read phase without blocking the transformed stream.
Each attempt is limited to 4096 raw events and 64 MiB of event data, type, and ID bytes before
the consumer attaches, including events ignored by protocol conversion. Exceeding either limit
fails the request without retrying or exposing partial attempt data. After attachment, events
remain ordered and the bounded delivery channel applies backpressure.

This prevents long empty-thinking streams from deadlocking, but does not send early heartbeats
or shorten the wait for meaningful content. Client and proxy timeouts still apply to that wait.

## FAQ

### Q: Connection test failed?

- Check if the API Key is correct (no extra spaces when copying)
- Verify the Base URL is accessible
- Check if your provider account has sufficient credits

### Q: "Model not found" error?

- Confirm the model is in the Supported Models list
- Check if model mapping is configured correctly
- Verify the channel is enabled

### Q: How to set up multiple API Keys?

Enter all keys in the API Keys field, one per line. The system will automatically rotate them.

### Q: How to restore a disabled API Key?

Go to channel details, find the key in the **Disabled List**, and click **Restore**.

## Related Documentation

- [Model Management Guide](model-management.md) - Configure model-channel associations
- [API Key Profiles Guide](api-key-profiles.md) - Configure model mappings and permissions
- [Request Processing Guide](../getting-started/request-processing.md) - Understand the full request flow

## Default tags and stable rows

**Default provider**, next to **Default tags**, lets you search and select one provider group, with image groups selected separately. **Save and apply** filters the list immediately and returns to the first page. Reopening the channel page restores the provider, shows the provider tabs, and applies default tags as well. The preference is saved in the current browser only. Save **All** or **Clear (all)** to remove the default restriction. Cancelling, closing the dialog, or temporarily switching providers does not change the saved default. A provider with no matching channels shows an empty list instead of all channels.

Default tags stores zero, one or multiple tags in the current browser and applies them when opening the page. Empty means all tags; multiple tags match any. Temporary filters and reset do not overwrite the saved preference. Multiple tags resolve matching IDs from channel summaries, followed by server filtering, ordering and pagination. A single tag shows its override or inherited global value; multiple tags follow the no-single-tag display rules above. Channel and model rows change background color on hover without scaling.
