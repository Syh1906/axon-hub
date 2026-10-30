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
- With both fields empty, official OpenAI API and Codex channels automatically use `https://chatgpt.com/backend-api/codex/models`. No extra gateway catalog setting is needed. Third-party Codex channels use their `/models` endpoint; other Responses channels try their same-origin `/v1/models`, respecting existing `/v1` and `#` URL rules. If a third party only serves an ordinary OpenAI model list, it must provide a full catalog URL or JSON. AxonHub never invents capabilities or sends third-party credentials to OpenAI.

The catalog applies current project/key channel restrictions, allowed models, and aliases. Public slugs match callable model IDs. Multiple routes advertise common capabilities and the smallest context limit; conflicting instructions or tool dialects omit the model. Missing metadata is counted in `X-AxonHub-Catalog-Omitted`. If all visible models lack metadata, the endpoint returns `503 codex_catalog_unavailable`; no visible models returns an empty list. Catalog failures do not block inference.

Remote fetches have a 1 MiB limit and 10-second timeout. Cache entries are separated by channel, credentials, URL, and client version, respect `no-store`, `no-cache`, `max-age`, and `Age`, and expire within five minutes. Visibility is checked on every request; stale metadata is not served.

### WebSocket Capability and HTTP Fallback

The channel's **Supports WebSocket** checkbox declares capability without forcing client transport or changing the base URL. The client provider's `supports_websockets` setting and client version control whether it uses WS. HTTP clients always use upstream HTTP. WS clients use upstream WS only when the checkbox is enabled; otherwise AxonHub streams HTTP responses over the original client WS connection.

Before `response.create` is sent, connection failures and handshake statuses `404/405/426/501/502/503/504` (or a JSON `200`) can fall back to the same channel's HTTP endpoint. Authentication, rate-limit, and ordinary parameter errors do not trigger protocol fallback. Subsequent turns on that client connection retain HTTP for the downgraded upstream.

Continuation fallback restores prior user input and model output. Unrecoverable `previous_response_id` returns `previous_response_not_found` instead of sending incomplete history. Once a WS send begins, disconnects, timeouts, and decoding failures cannot automatically resend or switch channels. Request details show client transport, actual upstream transport, and the fallback reason.

Published legacy `transport=websocket` and WS URLs are read as capability declarations. New saves use `supportsWebSockets`; explicit false overrides legacy values. HTTP and WS share the Responses address, which should accept both POST and Upgrade requests.

### Related Documentation
- [Tracing Guide](tracing.md)
- [OpenAI API](../api-reference/openai-api.md)
- README sections on [Usage Guide](../../../README.en-US.md#usage-guide)
