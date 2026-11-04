# Codex Integration Guide

---

## Overview
AxonHub can act as a drop-in replacement for OpenAI endpoints, letting Codex connect through your own infrastructure. This guide explains how to configure Codex and how to combine it with AxonHub model profiles for flexible routing.

### Key Points
- AxonHub performs AI protocol/format transformation. You can configure multiple upstream channels (providers) and expose a single OpenAI-compatible interface for Codex.
- You can aggregate Codex requests from the same conversation by enabling `server.trace.codex_trace_enabled` (uses `Session_id`) or adding extra headers via `server.trace.extra_trace_headers`.

### Prerequisites
- AxonHub instance reachable from your development machine.
- Valid AxonHub API key with project access.
- Access to Codex (OpenAI compatible) application.
- Optional: one or more model profiles configured in the AxonHub console.

### Configure Codex
1. Edit `${HOME}/.codex/config.toml` and register AxonHub as a provider:
   ```toml
   model = "gpt-5"
   model_provider = "axonhub-responses"

   [model_providers.axonhub-responses]
   name = "AxonHub Responses"
   base_url = "http://127.0.0.1:8090/v1"
   env_key = "AXONHUB_API_KEY"
   wire_api = "responses"
   model_catalog_url = "http://127.0.0.1:8090/codex/models"
   supports_websockets = true
   query_params = {}

   [features]
   api_key_model_discovery = true
   ```
2. Export the API key for Codex to read:
   ```bash
   export AXONHUB_API_KEY="<your-axonhub-api-key>"
   ```
3. Restart Codex to apply the configuration.

#### Catalog URL and response format

Place `base_url` and `model_catalog_url` in the selected provider table. The former routes inference, normally ends in `/v1`, and uses `wire_api = "responses"`; the latter must target `/codex/models`, not `/v1/models`. For a static token, replace the example's `env_key` with `experimental_bearer_token = "<AxonHub API Key>"`. Never commit a real key in configuration or documentation.

| Purpose | Request | Successful response root |
| --- | --- | --- |
| OpenAI model names | `GET /v1/models` | `{"object":"list","data":[{"id":"model-id"}]}` |
| Full Codex catalog | `GET /codex/models?client_version=0.166.0` | Root `models` array; each entry uses `slug` and includes complete instructions, capabilities and tool metadata |
| Inference | `POST /v1/responses` | Responses protocol; separate from catalog discovery |

Catalog requests use `Authorization: Bearer <AxonHub API Key>`. The client appends `client_version`: the tested bundled `0.166.0-alpha.5.1` executable requests `0.166.0`. Use the actual three-part request version when importing the matching format; once saved, it records import provenance. An effective `model_catalog_json` takes precedence and prevents remote fetching; see the [official remote catalog guidance](https://learn.chatgpt.com/docs/enterprise/roll-out-a-gateway#serve-a-remote-catalog).

A 200 response from the ordinary list does not establish Codex catalog availability. Check the instance's key for a catalog 401, and model definitions, sources and import errors for a 503 with omitted models. Models without a manual override generate their catalogs automatically. Acceptance requires an authenticated catalog request and a real client's model list.

#### Trace aggregation by conversation (important)
Enable the built-in Codex trace extraction to reuse the `Session_id` header as the trace ID:

```yaml
server:
  trace:
    codex_trace_enabled: true
```

If Codex sends a different stable conversation identifier header (for example `Conversation_id`), you can configure AxonHub to use it as a fallback trace header in `config.yml`:

```yaml
server:
  trace:
    extra_trace_headers:
      - Conversation_id
```

**Note**: Enabling this also ensures that requests from the same trace are prioritized to be sent to the same upstream channel, significantly improving provider-side cache hit rates (e.g., Anthropic Prompt Caching).

#### Testing
- Send a sample prompt; AxonHub's request logs should show a `/v1/responses` call.
- Enable tracing in AxonHub to inspect prompts, responses, and latency.

### Working with Model Profiles
AxonHub model profiles remap incoming model names to provider-specific equivalents:
- Create a profile in the AxonHub console and add mapping rules (exact name or regex).
- Assign the profile to your API key.
- Switch active profiles to alter Codex behavior without changing tool settings.

<table>
  <tr align="center">
    <td align="center">
      <a href="../../screenshots/axonhub-profiles.png">
        <img src="../../screenshots/axonhub-profiles.png" alt="Model Profiles" width="250"/>
      </a>
      <br/>
      Model Profiles
    </td>
  </tr>
</table>

#### Example
- Request `gpt-4` → mapped to `deepseek-reasoner` for getting more accurate responses.
- Request `gpt-3.5-turbo` → mapped to `deepseek-chat` for reducing costs.

### Troubleshooting
- **Codex reports authentication errors**: ensure `AXONHUB_API_KEY` is exported in the same shell session that launches Codex.
- **Unexpected model responses**: review active profile mappings in the AxonHub console; disable or adjust rules if necessary.

---

## Provider Quota Tracking

AxonHub automatically tracks quota usage for Codex provider channels, displaying the current status with battery icons in the interface.

### How It Works

- **Automatic Polling**: AxonHub periodically polls your Codex account to check quota status
- **Storage**: Quota data is stored in the database and updated based on the configured check interval
- **Visual Indicators**: Battery icons show your remaining quota at a glance.

### Quota Windows

Codex uses multiple quota windows:
- **Primary window**: Main usage limit with a configurable duration (e.g., 5 hours, 1 day)
- **Secondary window**: Optional secondary usage limit with its own duration and reset schedule

The system shows both window percentages including the primary window duration and reset time.

### Configuration

Adjust the quota check interval in `config.yml`:

```yaml
provider_quota:
  check_interval: "5m"           # Check every 5 minutes (default)
```

Or via environment variable:

```bash
export AXONHUB_PROVIDER_QUOTA_CHECK_INTERVAL="30m"
```

Supported intervals: `1m`, `2m`, `3m`, `4m`, `5m`, `6m`, `10m`, `12m`, `15m`, `20m`, `30m`, `1h`, `2h`, etc.

**Recommendations:**
- **Development**: Use shorter intervals (e.g., `5m`) for quick feedback
- **Production**: Use `5m` (default) for timely quota detection; increase to `10m` or `20m` to reduce API calls

### Refreshing Quota Data

You can manually trigger a quota refresh by clicking the refresh icon in the quota status popover.

### Viewing Quota Status

1. Look for the battery icon next to the settings gear in the header
2. Click the battery icon to view detailed quota information including:
   - Primary window usage percentage and duration
   - Primary window reset time
   - Plan type (if available)
   - Secondary window usage (if configured)

### Codex Model Catalog

`GET /codex/models?client_version=0.159.2` authenticates with an AxonHub API key and returns Codex's `{"models": [...]}` structure. The client supplies a three-part numeric version. `/v1/models` retains its OpenAI `data` format and is not a Codex catalog.

Configure **Endpoints and Paths → Responses** on each channel:

- **Custom Codex catalog JSON** supplies complete metadata and takes precedence over the remote URL. Each `slug` identifies the actual upstream model. Include model instructions, tool capabilities, and reasoning levels, not just model names.
- **Codex model catalog URL** supplies the complete trusted upstream URL. AxonHub sends channel credentials and `client_version`; redirects are rejected.
- With both fields empty, official OpenAI API and Codex channels automatically use `https://chatgpt.com/backend-api/codex/models`. No extra gateway catalog setting is needed. Third-party Codex channels use their `/models` endpoint; other Responses channels try their same-origin `/v1/models`, respecting existing `/v1` and `#` URL rules. If a third party only serves an ordinary OpenAI model list, its routes can share complete metadata from associated routes for the same actual model. A trusted catalog URL or definition is needed when all such sources are missing. Third-party credentials are sent only to the configured catalog service.

The catalog applies current project/key channel restrictions, allowed models, and aliases. Public slugs match callable model IDs. Visible compatible routes advertise common capabilities and the smallest context limit. Instructions and client tool policies use the stable source selection below. Missing metadata is counted in `X-AxonHub-Catalog-Omitted`. If all visible models lack metadata, the endpoint returns `503 codex_catalog_unavailable`; no visible models returns an empty list. Catalog failures do not block inference.

Remote fetches have a 1 MiB limit and a three-second timeout, leaving transport and decoding time within the Codex client's five-second deadline. Cache entries are separated by channel, credentials, URL, and client version, respect `no-store`, `no-cache`, `max-age`, and `Age`, and expire within five minutes. Visibility is checked on every request; stale metadata is not served.

### Automatic discovery and optional overrides

By default, model configuration, associations and key permissions produce the catalog without a JSON import. Each request recomputes visible models, aliases and routes and fetches upstream metadata for its `client_version`. Source cache controls are honored with a maximum five-minute TTL. `no-store` and `no-cache` disable caching. Clients see changes when they fetch a fresh catalog.

Each concrete public model selects a complete source independently. An actual-model match to the public target wins, followed by association priority, channel ID and actual-model ID. Instructions and client tool policies remain a complete source profile. Routes missing a catalog first look up the **same actual model** in the complete catalogs of associated channels, even if a source channel is not configured to route that model. If no exact entry exists, the model page's explicit association allows the route to inherit the selected public-model profile, including opaque upstream aliases such as `codex-auto-review`. This does not grant inference access to the source channel. Known capabilities on caller-visible routes still constrain the result. Inheritance relies on the administrator's configured model associations, and provider behavior requires verification.

Profiles declaring `freeform` patch tools or `code_mode_only` use visible Responses routes, including per-model protocol overrides. Inference requests containing Responses custom tools or their call history also exclude incompatible endpoints, avoiding silent tool removal. Ordinary requests retain existing protocol selection.

Automatic results are not stored in `settings.codexCatalog`. New associations, key permissions and client versions do not require reimport. A valid manual override takes precedence; clear it to restore automatic discovery. In automatic mode, a public model is omitted if none of its associated channels supplies a complete source profile. If every model is unavailable the endpoint returns 503. Automatic discovery requires an available source after its cache expires.

The following workflow is optional.

### Configure catalog context windows

Set the default and maximum supported context windows in **Models → Edit → Model catalog context**, in tokens. Settings apply to every public ID under the model rule. These are independent of the model card's descriptive limits and do not require a complete manual Codex definition.

Use **Channels → More → Model context** to override each field for a specific channel model. Channel values take precedence; blank fields dynamically inherit the model rule matched by the request. Clear an override or choose **Restore inheritance** to follow subsequent model changes. With neither layer configured, preserve upstream catalog behavior. A maximum-only configuration uses the effective maximum as its default; an upstream maximum also supplies a missing upstream default. The default must not exceed the maximum. An inherited default above a channel maximum is reduced to that maximum.

The gateway resolves each available route before merging their common capabilities for the requesting API key. In manual catalogs, missing context metadata on one route does not erase window limits or compaction thresholds declared by other visible routes. The model and channel context settings apply to automatic catalogs and existing manual definitions, changing only `context_window` and `max_context_window`. Clients see updates when they next fetch the catalog.

A channel model may serve several public models or aliases, so inherited limits depend on the model rule matched by each request. The channel dialog displays explicit channel overrides and labels blank fields as inheriting from the request-matched model. It does not infer a unique effective value from the channel model ID.

Published values do not increase the provider's actual capacity. Unconfigured fields are not replaced by hardcoded numbers; for example, `872000` is not a built-in default. Context settings cannot supply missing model instructions or tool metadata.

### Manual multi-channel model definitions

To pin or customize metadata, open **Models → Edit → Codex manual override (optional)**, enter the actual client request version used for import, and import from associated channels. Import selects a complete client profile for each concrete public ID, keeping its instructions and tool settings separate from other IDs published by the same regex. It does not persist data.

Review the profiles in `models`, the differences in `sourceDifferences`, and any route overrides you want to keep. Different source instructions, patch tools, web-search types, tool modes or multi-agent modes do not prevent previewing. Confirm, apply, then save. The catalog does not change channel capabilities: for example, `freeform` generates Responses custom tools, and the inference path still checks which routes can carry them.

Import does not pre-intersect source capabilities. `models[public ID].model` keeps that ID's instructions and inheritance baseline; each catalog-backed route stores its own `capabilities`. Only permitted routes constrain the result, and key aliases select profiles using their mapped target IDs. Edit route `limits` for manual reductions. `truncation_policy` clips local tool output and stays with the selected client profile; bytes and tokens are not interchangeable upstream context limits.

`settings.codexCatalog` stores the import request version in `clientVersion` and a `models` map keyed by public target ID. Each profile has `sourceChannelId`, `sourceModelId` and a complete `model`. `sourceDifferences` records source identities and differing field names without copying instruction text. Root `model` and provenance retain the legacy baseline; published definitions remain readable without forced migration. The optional `routes` array stores overrides. Each entry has integer `channelId`, actual upstream `modelId`, optional source `capabilities`, and narrowing `limits`, for example:

```json
{"channelId": 12, "modelId": "actual-model-id", "limits": {"context_window": 16000}}
```

Limits support context bounds, reasoning/service tiers, input modalities, experimental tools and API/verbosity/parallel-tool booleans. Arrays intersect, numeric bounds take the minimum, and booleans require common support. Unknown limit fields are rejected; unknown model metadata is preserved. Reasoning and service tiers match by identity rather than description text.

An enabled model visible to the current project and key uses its saved definition directly. When you add a channel or change an upstream model mapping, any association without a matching route override inherits the model definition. It does not need reimport, separate confirmation, or a live channel catalog. Only visible associations contribute limits. The import version records the source; update the definition after a client upgrade if there is a real format difference. Clearing the definition restores automatic discovery.

The read-only admin GraphQL query `previewModelCodexCatalog(id, clientVersion)` requires channel-write permission. Save/clear through `updateModel.settings.codexCatalog`. Source request failures and invalid definitions retain warnings. Channels without a Responses catalog are not probed; valid ordinary model lists indicate absent metadata. Models omitted solely for absent metadata produce debug logs. `X-AxonHub-Catalog-Omitted` remains included in success and failure responses. Remote refresh has concurrency 4 and a total three-second budget. Inline JSON is read first, followed by explicit catalog URLs, Codex channels and official catalogs, then ordinary compatible endpoints. Valid OpenAI `data` lists are remembered as lacking complete metadata under the same identity, version and upstream cache rules. Authentication, connection and malformed-response errors are reported immediately. Known fields and complete model-message structures are validated before saving and after merging; malformed remote entries are isolated. Equivalent shell aliases and documented capability defaults are normalized. If no permitted model has a usable definition, the endpoint still returns an explicit 503 rather than inventing instructions.

### WebSocket Capability and HTTP Fallback

The channel's **Supports WebSocket** checkbox declares capability without forcing client transport or changing the base URL. The client provider's `supports_websockets` setting and client version control whether it uses WS. HTTP clients always use upstream HTTP. WS clients use upstream WS only when the checkbox is enabled; otherwise AxonHub streams HTTP responses over the original client WS connection.

Before `response.create` is sent, connection failures and handshake statuses `404/405/426/501/502/503/504` (or a JSON `200`) can fall back to the same channel's HTTP endpoint. Authentication, rate-limit, and ordinary parameter errors do not trigger protocol fallback. Subsequent turns on that client connection retain HTTP for the downgraded upstream.

Continuation fallback restores prior user input and model output. Unrecoverable `previous_response_id` returns `previous_response_not_found` instead of sending incomplete history. Once a WS send begins, disconnects, timeouts, and decoding failures cannot automatically resend or switch channels. Request details show client transport, actual upstream transport, and the fallback reason.

Codex may send `response.interrupt` when new input or a subagent result arrives during generation. AxonHub acknowledges this control event on the client WS connection and stops the corresponding upstream call, including HTTP-only upstreams. By default it waits for the current output item to finish; explicit `discard_partial_items: true` permits dropping unfinished items. The gateway emits a terminal response with `incomplete_details.reason: "interrupted"` and retains completed reasoning items and tool calls for continuation without replaying the interrupted request. Interrupts affect only the specified response on the same connection; late interrupts racing with normal completion are also acknowledged. Disk retention continues to respect both body-storage switches; disabling storage leaves only the existing bounded in-memory cache.

Published legacy `transport=websocket` and WS URLs are read as capability declarations. New saves use `supportsWebSockets`; explicit false overrides legacy values. HTTP and WS share the Responses address, which should accept both POST and Upgrade requests.

Upstream Responses error events may omit an HTTP status. When a request fails before output starts, AxonHub's HTTP error response uses 502 while retaining the upstream error code and message; explicit upstream error statuses remain unchanged. This conversion only determines the client response and does not alter the recorded upstream status or retry decisions. Streams that have already started continue to report failure through error events.

An explicit `upstream_stream_read_error` without an available HTTP error status can enter the existing retry flow. Output must not have started, replay must remain safe, and retry budget must remain. Complete-history continuations prefer their original source and can use eligible fallback channels or addresses. Unresolved upstream response IDs retain origin requirements. Delivered output or an uncertain send outcome prevents replay.

When an upstream 429 includes `Retry-After`, AxonHub records a channel cooldown using the existing parsing rules and checks it before each send. Requests blocked by the cooldown do not reach that channel or consume RPM. Ordinary requests and complete-history continuations can use existing eligible candidates; unresolved upstream response IDs cannot switch known sources. With no available candidate, the client receives 429 and the remaining `Retry-After` and decides when to retry. A 429 without that header does not imply a cooldown. Initial rate limits and provider stream interruptions can still occur. This local refusal does not extend the cooldown, charge actual usage or trigger credential disabling, and retains the gateway's cooldown message. Actual upstream 429 errors continue to use configured hidden or custom messages.

### Cross-Thread Delivery and Tool Outputs

Codex can represent a task delivered by another thread as a standalone tool output with `id`, `namespace`, `name`, and `output`. If the receiving thread did not make the corresponding tool call, the output has no `call_id`. The output item's own `id` must not substitute for a call ID.

Responses-to-Responses conversion preserves the original tool-output `type`, `id`, `namespace`, name, and any actual `call_id`. Later turns replay the delivery as tool output. A `custom_tool_call_output` also retains its type when its matching call exists only in the response referenced by `previous_response_id`.

This does not require enabling WebSocket on the client. Direct HTTPS and safe WSS-to-HTTPS fallback before sending share these conversions. The upstream must still support standalone tool outputs. For a missing-`call_id` error, first compare these fields in the inbound and outbound requests, then check upstream support. Switching transport does not establish field compatibility, and ordinary HTTP parameter errors do not trigger protocol fallback.

### Complete Continuation History and Mode Billing

Restoration follows `previous_response_id` through every ancestor until a complete starting request or cached ancestor is available. Each lookup retains the current key and project scope. The 1 MiB limit controls cache admission, not usable history size: larger records can be used directly for the current request. Missing, cyclic, or malformed ancestors never produce partial input.

Native HTTP may retain the ID for upstream restoration. Transports requiring complete local input, including Codex HTTP and WS-to-HTTP bridging, reject missing history. Storage failures preserve their real cause and stop ineffective retries; cancellation stops immediately. Database failures are not mislabeled as missing history.

Only historical input and output are restored. The current model, `service_tier`, and other parameters remain current; previous usage and billing tiers are not inherited. See [Cost Tracking](cost-tracking.md#fast--ultrafast-and-long-context-costs) for pricing and markers.

### Related Documentation
- [Tracing Guide](tracing.md)
- [OpenAI API](../api-reference/openai-api.md)
- README sections on [Usage Guide](../../../README.en-US.md#usage-guide)
