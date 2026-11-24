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

#### Client and upstream WebSockets

Codex can keep its connection to AxonHub open while a channel streams over HTTP. On a channel with WS enabled and an HTTP alternative, one incoming WS turn tries native WS when the selected endpoint, credential and model have no fresh result. Other matching turns can use HTTP during discovery and cooldown. A completed native response with meaningful output verifies the model.

Two AxonHub instances can be chained. The first gateway can connect to the second gateway’s `/v1/responses` over WS while the second streams over HTTP to its provider. A successful WS probe at the first gateway verifies the hop to the second. Check the execution at the second gateway for the final transport.

Open **Channels → Endpoints and protocols → Upstream endpoints** and expand **Verify model WS**. Opening the panel reads existing evidence. Clicking the verification button can send one billable model request using the saved configuration, even when the WS switch is off. Generic Responses probes request at most 64 output tokens. Native Codex rejects that parameter, so its probe asks for a short `OK` reply and the provider controls the output length. In the bulk health check, select WS verification to choose a model per channel and view the maximum request count. Fresh matching results can be reused.

A successful upgrade or an empty warmup only establishes connection-level evidence. A 1013 close for lack of an available account is a temporary model-stage failure. Native model evidence lasts six hours. The usual cooldown is ten minutes for a rejected upgrade and thirty seconds for transient failures, extended by upstream `Retry-After`. Each instance maintains its own bounded cache. The management view can restore recent records after a restart. Business requests use memory only.

Live native continuation requires the same upstream connection and identity. An HTTP bridge needs recoverable history. WS-only model and endpoint policies retain the native handshake timeout. Explicit probes and new turns with an HTTP alternative use the shorter handshake budget. Eligible handshake failures can use the configured HTTP endpoint before sending a generation frame. After an uncertain send, the gateway does not replay the turn.

For latency analysis, group non-empty `continuation_key` values within the same site, project and API key. Join each request ID to the structured request-delivery and upstream-transport logs. The timestamps distinguish completed intake, upload, HTTP response headers, meaningful model output, gateway event and terminal writes, and a committed budget settlement. Missing timestamps mean the corresponding event was unobserved. Gateway writes measure the server side of delivery.

Configure log collectors to use `axon_request_id` for the local database row ID. Older entries could contain two `request_id` keys in the same line. The numeric value was the database ID and the string was the trace identifier.

For Responses, `first_responses_event` observes the first decoded event the orchestrator reads from the upstream transport. WS control frames handled inside the transport and SSE comments fall outside this observation. `first_responses_delta` and `gateway_first_responses_delta_write` independently observe the first non-empty text, reasoning, tool argument/input or refusal delta read and successfully written. They store the event category, data byte count and timestamp. Streams returning only complete items can have meaningful model output with no delta observation. Measuring rendering requires a client-side clock.

`attempt_prepared_at` marks the prepared attempt. `upstream_dispatch_at` marks executor entry after admission. Together with completed intake they distinguish preparation from admission time. `gateway_first_content_write` records the first successfully written text, reasoning, tool or refusal content, including whole-item responses. Its event type can differ from the first delta. The same field also covers streaming OpenAI Chat, Anthropic Messages and Gemini Contents.

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

#### Continuations after client cancellation

The request list folds a verified client-cancellation continuation into its later request and shows a muted “Resumed N” label beside the ID, matching the retry-count style. Click it to page through the original canceled records. The representative keeps the later request's actual status, including failure. Original details, tokens and costs remain separate, and cost statistics retain the earlier records. Date, status and other list filters, pagination and status counts apply to the final representative; a continuation across midnight therefore belongs to the later request's date.

Only adjacent requests in the same project, API key, trace and explicitly supplied client session, thread and turn can match. Turn metadata must include `request_kind="turn"`. The preceding logical request must be canceled and its adopted execution must have a positive measured duration. Legacy records without a failure require a canceled execution. With the exact diagnostic `stage=delivery`, `type=gateway_error`, `code=stream_delivery_canceled`, the execution may be canceled or completed: upstream completion can coexist with client cancellation. Delivery timeout, write failure, unconfirmed delivery and actual upstream failure do not qualify through this rule.

The next request must start within 10 seconds after the adopted execution's estimated end. Missing markers, overlapping work, an intervening normal request or missing timing leave requests separate. The estimate uses execution creation time plus total latency, so an extremely fast continuation may conservatively remain separate. Cancellation diagnostics, upstream execution statuses and original costs remain intact; folding changes only the display relationship.

The `v1.0.0-beta12-fork.3` migration backfills the internal normalized identity from saved client headers in batches. It is idempotent and preserves current statuses and timestamps; it does not restore deleted headers or invent usage. Ordinary GraphQL `requestLogs` calls keep the original per-request semantics. The UI opts in through `filter.collapseContinuations=true` and loads history on demand through `requestContinuations`, under the same authorization checks.

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
- With both fields empty, official OpenAI API and Codex channels automatically use `https://chatgpt.com/backend-api/codex/models`. No extra gateway catalog setting is needed. Third-party Codex channels use their `/models` endpoint; other Responses channels try their same-origin `/v1/models`, respecting existing `/v1` and `#` URL rules. If a third party only serves an ordinary OpenAI model list, its routes can share complete metadata from associated routes for the same actual model. A trusted catalog URL or definition is needed when neither channel sources nor an official release snapshot describes the exact model. Third-party credentials are sent only to the configured catalog service.

The catalog applies current project/key channel restrictions, allowed models, and aliases. Public slugs match callable model IDs. Reasoning choices follow the model-profile rules below. Other capabilities and context bounds follow the selected catalog source and the routes visible to the key. Instructions and client tool policies use the selected source. Missing metadata is counted in `X-AxonHub-Catalog-Omitted`. If all visible models lack metadata, the endpoint returns `503 codex_catalog_unavailable`; no visible models returns an empty list. Catalog failures do not block inference.

Remote fetches have a 1 MiB limit and a three-second timeout, leaving transport and decoding time within the Codex client's five-second deadline. Cache entries are separated by channel, credentials, URL, and client version, respect `no-store`, `no-cache`, `max-age`, and `Age`, and expire within five minutes. Visibility is checked on every request; stale metadata is not served.

### Automatic discovery and optional overrides

By default, model configuration, associations and key permissions produce the catalog without a JSON import. Each request recomputes visible models, aliases and routes and fetches upstream metadata for its `client_version`. Source cache controls are honored with a maximum five-minute TTL. `no-store` and `no-cache` disable caching. Clients see changes when they fetch a fresh catalog.

Automatic discovery first excludes profiles whose tool protocol cannot be served by any caller-visible route. For example, a richer profile requiring Responses tools cannot displace a usable profile when the key only has Chat Completions routes. Each concrete public model then selects a complete source independently. An actual-model match to the public target wins. Among profiles for the same actual model, prefer more distinct non-`none` reasoning effort IDs; equal counts retain the stable association-priority, channel-ID and actual-model-ID order. Do not combine efforts from different actual models or infer choices from model names. Instructions, client tool policies and multi-agent parameters remain a complete source profile. Routes missing a catalog first look up the **same actual model** in the complete catalogs of associated channels, even if a source channel is not configured to route that model. If no exact entry exists, the model page's explicit association allows the route to inherit the selected public-model profile, including opaque upstream aliases such as `codex-auto-review`. This does not grant inference access to the source channel.

Reasoning choices, their default and support for dynamic effort updates belong to the selected model profile, rather than the intersection of route snapshots. If a complete source declares `low/medium/high/xhigh/max/ultra` and another only declares `none`, retain the complete profile's choices and default. Manual overrides use the administrator's saved model profile without selecting a remote replacement. For official and channel-sourced profiles, explicit `limits` on caller-visible routes intersect for choices not explicitly set on the model page; sparse `capabilities` snapshots no longer substitute for administrator limits. All sources follow the model capability precedence rules below. Client-tool compatibility filtering does not remove their source metadata; actual upstream aliases still reuse their own context limits.

Earlier local simulated requests with Codex `0.166.0-alpha.6` verified that Ultra sends the selected profile's `multi_agent_reasoning_effort`. When the model page leaves the relevant efforts unspecified, route `limits` that remove the underlying effort also remove Ultra. When the model page explicitly sets efforts, its field priority applies before the catalog checks whether Ultra's underlying effort is available. The catalog publishes Ultra only when the model profile declares it with a known mapping. Automatic and manual catalogs share these rules. Inference uses the selected channel's parameter settings. Recheck the Ultra mapping when upgrading the client.

Profiles declaring `freeform` patch tools or `code_mode_only` use visible Responses routes, including per-model protocol overrides. Inference requests containing Responses custom tools or their call history also exclude incompatible endpoints, avoiding silent tool removal. Ordinary requests retain existing protocol selection.

Automatic results are not stored in `settings.codexCatalog`. New associations, key permissions and client versions do not require reimport. A valid manual override takes precedence; clear it to restore automatic discovery. In automatic mode, a model is omitted only when neither its associated channels nor official release snapshots provide a usable complete profile. If every model is unavailable the endpoint returns 503. Third-party models can continue using channel metadata, inherited overrides or fixed complete definitions.

### Choose a baseline catalog

Open **Integrations → Codex compatibility → Import model catalog**. Previewing leaves the current source active. Applying a preview selects exactly one baseline for this instance.

| Source | How updates work |
| --- | --- |
| Official repository (default) | Scheduled and manual checks update the official baseline as before. |
| Catalog URL | Use a public HTTPS link that returns JSON directly, such as a GitHub Raw URL. Save it as a subscription to check this URL on the configured schedule. Open Import to preview and apply changes. |
| Local snapshot | Run the supplied command on the Codex machine and paste its output, or select a JSON file. An import without a URL subscription also creates a snapshot. Remote checks pause until a remote source is selected. |

Provide a JSON object with a nonempty `models` array of complete Codex profiles. The catalog limit is 1 MiB. The local command checks `CODEX_HOME` first and then the current user's `.codex` directory. It copies or prints the existing cache without refreshing it. The import stores the model list and optional `client_version`; it does not need account credentials. Imports honor each model's `minimal_client_version`, using the cache's client version when the model omits it. Select **Official repository**, preview and apply to return to the default source.

When an import or subscription changes a model’s effective minimum client version, the preview shows its old and new minimum. Older clients can still use compatible bundled or previously archived releases. Replicas sharing a database use the same saved source. A check started before a source switch cannot overwrite it.

The catalog URL is prefilled with [the catalog maintained in this repository](https://raw.githubusercontent.com/Syh1906/axon-hub/refs/heads/release/v1.0.x/catalogs/codex/models.json). You may replace it or use **Restore repository URL** to reset the input. New installations still use the official repository until you preview and apply another source.

**Model capability precedence.** The official repository, official release snapshots, catalog URLs and local JSON all resolve fields in the same order. Explicit outer model-card and context settings take precedence over handwritten Codex overrides. Unset fields inherit the baseline selected for the client version. The same order applies when a channel supplies a model missing from the official catalog. A dedicated maximum takes precedence over the context limit on the card. Blank form fields inherit. Codex accepts text, image and audio inputs. A card listing text, image and PDF therefore publishes text and image. Fixed profiles retain their saved baseline and apply current card and context settings when served. New previews include `catalogDefaults` for each model to identify edited capability fields. Use per-model `overrides` to force a value identical to its original catalog value. Older fixed definitions use their saved model values as the baseline.

When neither the outer form nor the handwritten override sets a field, official and channel-sourced profiles use existing route capability merging. Profiles imported from a URL or JSON keep their declared capabilities unless every compatible route visible to the key explicitly rules one out. For example, forcing Standard on every route removes Fast. If one route inherits the model declaration, Fast remains available. If every route has a maximum context limit, the largest available limit bounds the model window. Key permissions, tool protocol admission, Node REPL disablement and mandatory review still apply. Inference follows the selected channel's actual settings.

The preview compares baseline profiles. It does not publish model IDs, edit channel permissions or overwrite saved model definitions. A fixed model definition continues to use its saved profile. An inherited model follows the selected baseline with its field overrides, context settings and visible route restrictions. An explicit release pin still selects that release. Changes become visible when the client next requests its catalog.

### Official release snapshots and rollback

For a synchronized official-source entry, the model page sets the default and maximum context independently. When the model page and handwritten override leave the windows unset, an explicit maximum on a visible route's actual model (including a channel alias) caps the inherited values. For example, a route limited to 128,000 cannot advertise an upstream maximum of 872,000. Admin previews and automatic catalogs select profiles using the same tool protocol requirements. A saved definition is also checked against the caller's current routes before publication: a chat-only route cannot advertise Responses-only tools.

The last check records the most recent attempt, including unchanged or failed checks, and survives a restart. Neither case rewrites the catalog body. When the panel reads a later successful result, it clears the earlier check error.

Under **Integrations → Codex compatibility → Official model catalog sync**, Axon anonymously checks the [public Codex model file](https://github.com/openai/codex/blob/main/codex-rs/models-manager/models.json). Checks default to every 15 minutes; choose 5, 15, 30 or 60 minutes, or turn them off. **Check now** works independently of the schedule. Only a fully validated file replaces the saved copy. The panel shows upstream context, available reasoning levels and the default level; it does not represent an account-specific catalog or prove what a provider accepts.

Automatic discovery prefers an exact public model entry if it declares a compatible minimum client version and its tool protocol works on a visible route. Default and maximum context independently use the value on the model page, inheriting each blank field from upstream without writing to the model. Manual definitions, pins and caller visibility still apply. Changes appear on the next client fetch; the public source reports `official_public` and the content digest.

Available since `v1.0.0-beta10-fork.7.syh.19`. Existing automatic catalog configurations need no reimport: the rules below apply after upgrading. Complete third-party definitions remain supported.

AxonHub bundles the complete `models.json` from OpenAI Codex release tags 0.166.0 and 0.167.0. Original bytes, source URLs and SHA-256 digests are in `internal/server/biz/codexdata/`. No OpenAI credential or metadata-bearing inference channel is required. These are release snapshots, not the authenticated service's live catalog or a grant of model access.

Lookup uses the model page's exact **public model ID**, after resolving key aliases. An inherited definition can explicitly select a `sourceModelId`. No model names are guessed from prefixes, regexes or similarity, and no inference routes are added. Existing equally complete same-model channel metadata retains precedence. Official metadata fills missing profiles, different-model alias profiles, or a smaller set of non-`none` reasoning choices. Client tool protocols must still be compatible with a caller-visible route.

On the first request for a new client version, fetch `https://raw.githubusercontent.com/openai/codex/rust-v<version>/codex-rs/models-manager/models.json`, never `main`. Official and channel fetches run independently within the three-second catalog deadline. Public downloads have a 1.5-second timeout and a 1 MiB limit, send no channel credentials or caller authentication headers, and reject redirects. Validate the complete catalog before archiving immutable original bytes and their digest by version in the existing systems table. Archives are revalidated after restart. Failed updates never replace valid versions and are not retried for one hour.

If the exact release is unavailable, use the newest valid archive or bundled release no newer than the client. The `axonhub_catalog` entry records the official snapshot and requested client versions. If none applies, channel metadata can still be used; missing metadata only omits affected models and never blocks inference. Test each supported client's parsing: version ordering alone is not a compatibility guarantee.

Set these options in the server configuration and restart after changes:

```yaml
codex_catalog:
  disabled: false
  offline: false
  version: ""
```

`offline: true` uses bundled and archived snapshots without public downloads. `disabled: true` disables official snapshots. `version: "0.166.0"` pins the official source; an unavailable or too-new pin does not float to another official version, while channel metadata can still be used. Environment equivalents are `AXONHUB_CODEX_CATALOG_DISABLED`, `AXONHUB_CODEX_CATALOG_OFFLINE` and `AXONHUB_CODEX_CATALOG_VERSION`. Offline, disabled and server-pinned modes do not poll the public repository.

### Keep custom changes while following source updates

Use the existing **Codex manual override** JSON editor to save:

```json
{
  "mode": "inherit",
  "sourceModelId": "gpt-6.1-terra",
  "overrides": {
    "description": "My Terra configuration",
    "default_reasoning_level": "high"
  }
}
```

`sourceModelId` defaults to the public target ID. `overrides` follows JSON Merge Patch: objects merge recursively, arrays replace, and null removes fields. Other fields follow the source. `slug` cannot be overridden; the gateway assigns the callable name. The picker uses `display_name` from the model definition or its overrides. A missing or blank name falls back to the callable name. The gateway clears `upgrade` and removes `prefer_websockets` so the client controls transport. For every baseline, explicit outer model fields take precedence over `overrides`, as described under [Choose a baseline catalog](#choose-a-baseline-catalog). Tool compatibility uses the patched declarations. The final profile must pass catalog parsing and tool compatibility checks; a bad override affects only its model and cannot mutate the base snapshot. For modern instructions, patch `model_messages.instructions_template`, not only legacy `base_instructions`.

Add `"snapshotVersion": "0.166.0"` to pin this model to an official release for rollback. Its base model must exist in that version; failure does not silently select another model or a channel definition. Remove the pin to follow the client version again. Existing complete `clientVersion/model/models/routes` definitions remain fixed and are never automatically converted. **Preview available catalog metadata** returns a complete definition with official provenance and version when applicable.

Models absent from the official catalog, such as DeepSeek, continue using channel metadata or administrator-confirmed complete definitions. They can also use `mode: inherit` with an explicit third-party `sourceModelId` and custom differences. Official GPT instructions are never fabricated for unknown third-party models.

Official references: [Release snapshots and client schema](https://learn.chatgpt.com/docs/enterprise/roll-out-a-gateway#match-the-clients-schema), [Model catalog reference](https://learn.chatgpt.com/docs/enterprise/roll-out-a-gateway#model-catalog-reference).

The following workflow is optional.

### Configure catalog context windows

Set the default and maximum supported context windows in **Models → Edit → Model catalog context**, in tokens. Settings apply to every public ID under the model rule. The dedicated maximum takes precedence over the card’s context limit. The two window settings can be set independently.

Use **Channels → More → Model context** to configure windows for an actual upstream model. Channel settings and source metadata supply fields left unset by the outer form and handwritten override. Setting only the model maximum retains an available inherited default. The final default cannot exceed the maximum.

The Codex catalog uses only channels the key can access that also admit Responses clients. Models disappear when every associated channel denies this ingress. The base catalog supplies the candidate speed tiers, labels and order. For Fast or Ultrafast without a handwritten model override, the key must have a Responses route that can deliver the tier without a downgrade. An explicit model tier override changes the client catalog declaration. Inference still uses the selected channel’s actual tier configuration. An explicit channel tier setting takes precedence over upstream tier metadata. When a channel inherits its tier configuration, an imported profile supplies the model declaration. Official and channel-sourced profiles use route declarations and request-body overrides. For example, forcing `service_tier=default` prevents the channel from qualifying for Fast or Ultrafast. Saved route limits still apply. For official and channel-sourced profiles, omitted search, experimental tool and input modality declarations inherit the model profile. An explicit restriction narrows the result. Fixed definitions from those sources honor the saved capabilities of each channel and actual model before applying its explicit route limits. A saved route can enable search even if the model baseline disables it. Node REPL auto-review stays required if the base model or a visible route requires it.

For official and channel-sourced profiles, the gateway resolves each available route before merging their common capabilities for the requesting API key. In manual catalogs, missing context metadata on one route does not erase window limits or compaction thresholds declared by other visible routes. The model and channel context settings apply to automatic catalogs and existing manual definitions, changing only `context_window` and `max_context_window`. Clients see updates when they next fetch the catalog.

A channel model may serve several public models or aliases, so inherited limits depend on the model rule matched by each request. The channel dialog displays explicit channel overrides and labels blank fields as inheriting from the request-matched model. It does not infer a unique effective value from the channel model ID.

Published values do not increase the provider's actual capacity. Unconfigured fields are not replaced by hardcoded numbers; for example, `872000` is not a built-in default. Context settings cannot supply missing model instructions or tool metadata.

### Manual multi-channel model definitions

To pin or customize metadata, open **Models → Edit → Codex manual override (optional)**, enter the actual client request version used for import, and preview available catalog metadata. Import selects a complete client profile for each concrete public ID, keeping its instructions and tool settings separate from other IDs published by the same regex. It does not save model or channel configuration; newly fetched official snapshots are still archived as described above.

Review the profiles in `models`, the differences in `sourceDifferences`, and any route overrides you want to keep. Different source instructions, patch tools, web-search types, tool modes or multi-agent modes do not prevent previewing. Confirm, apply, then save. The catalog does not change channel capabilities: for example, `freeform` generates Responses custom tools, and the inference path still checks which routes can carry them.

Import does not pre-intersect source capabilities. `models[public ID].model` keeps that ID's instructions and inheritance baseline; each catalog-backed route stores its own `capabilities`. Only permitted routes constrain the result, and key aliases select profiles using their mapped target IDs. Edit route `limits` for manual reductions. `truncation_policy` clips local tool output and stays with the selected client profile; bytes and tokens are not interchangeable upstream context limits.

`settings.codexCatalog` stores the import request version in `clientVersion` and a `models` map keyed by public target ID. Each profile has `sourceChannelId`, `sourceModelId` and a complete `model`. `sourceDifferences` records source identities and differing field names without copying instruction text. Root `model` and provenance retain the legacy baseline; published definitions remain readable without forced migration. The optional `routes` array stores overrides. Each entry has integer `channelId`, actual upstream `modelId`, optional source `capabilities`, and narrowing `limits`, for example:

```json
{"channelId": 12, "modelId": "actual-model-id", "limits": {"context_window": 16000}}
```

Limits support context bounds, reasoning/service tiers, input modalities, experimental tools and API/verbosity/parallel-tool booleans. Official and channel-sourced profiles intersect route limits for fields without explicit model settings. For fields without an explicit model-page override, imported profiles keep each value allowed by at least one available route. Unknown limit fields are rejected; unknown model metadata is preserved. Reasoning and service tiers match by identity rather than description text.

An enabled model visible to the current project and key uses its saved definition directly. When you add a channel or change an upstream model mapping, any association without a matching route override inherits the model definition. It does not need reimport, separate confirmation, or a live channel catalog. Only visible associations contribute limits. The import version records the source; update the definition after a client upgrade if there is a real format difference. Clearing the definition restores automatic discovery.

The read-only admin GraphQL query `previewModelCodexCatalog(id, clientVersion)` requires channel-write permission. Save/clear through `updateModel.settings.codexCatalog`. Source request failures and invalid definitions retain warnings. Channels without a Responses catalog are not probed; valid ordinary model lists indicate absent metadata. Models omitted solely for absent metadata produce debug logs. `X-AxonHub-Catalog-Omitted` remains included in success and failure responses. Remote refresh has concurrency 4 and a total three-second budget. Inline JSON is read first, followed by explicit catalog URLs, Codex channels and official catalogs, then ordinary compatible endpoints. Valid OpenAI `data` lists are remembered as lacking complete metadata under the same identity, version and upstream cache rules. Authentication, connection and malformed-response errors are reported immediately. Known fields and complete model-message structures are validated before saving and after merging; malformed remote entries are isolated. Equivalent shell aliases and documented capability defaults are normalized. If no permitted model has a usable definition, the endpoint still returns an explicit 503 rather than inventing instructions.

### WebSocket Capability and HTTP Fallback

The channel's **Supports WebSocket** checkbox declares capability without forcing client transport or changing the base URL. The client provider's `supports_websockets` setting and client version control whether it uses WS. For ordinary Responses requests, an HTTP client can use an upstream WebSocket. The channel’s [upstream transport setting](channel-management.md) controls selection. Automatic can reuse a verified route. Prefer WebSocket can try an unverified route and allows eligible HTTP fallback. WebSocket only requires WS even when the client uses HTTP.

Before `response.create` is sent, connection failures and handshake statuses `404/405/426/501/502/503/504` (or a JSON `200`) can fall back to the same channel's HTTP endpoint. A handshake 400 with an empty body or plain `Bad Request` can also fall back when a dual-transport channel has a configured HTTP endpoint. Authentication, rate-limit, and ordinary parameter errors do not trigger protocol fallback. Subsequent independent turns use fresh evidence for the selected endpoint, credential and model.

Safe fallback reuses the same request and execution record, retaining the fallback reason in the channel process. A successful HTTP result does not create a separate failed request. A `1013: no available account` disconnect after sending leaves the request outcome unknown and is not a pre-send fallback. This close message does not establish that the channel lacks WS support, so it does not permanently disable the channel's WS setting. A new request initiated by the client retains its own record.

Continuation fallback restores prior user input and model output. Unrecoverable `previous_response_id` returns `previous_response_not_found` instead of sending incomplete history. Once a WS send begins, disconnects, timeouts, and decoding failures cannot automatically resend or switch channels. Request details show client transport, actual upstream transport, and the fallback reason.

Codex may send `response.interrupt` when new input or a subagent result arrives during generation. AxonHub acknowledges this control event on the client WS connection and stops the corresponding upstream call, including HTTP-only upstreams. By default it waits for the current output item to finish; explicit `discard_partial_items: true` permits dropping unfinished items. The gateway emits a terminal response with `incomplete_details.reason: "interrupted"` and retains completed reasoning items and tool calls for continuation without replaying the interrupted request. Interrupts affect only the specified response on the same connection; late interrupts racing with normal completion are also acknowledged. Disk retention continues to respect both body-storage switches; disabling storage leaves only the existing bounded in-memory cache.

Normal native WS continuations prefer the original upstream socket, preserving `previous_response_id` and incremental input. The gateway also retains bounded recovery history as immutable per-turn segments: each turn's new input and complete output are retained once, sharing preceding segments. Healthy continuations do not flatten history. The existing 64 MiB retention budget is shared across lanes on each client connection and includes JSON, segment bookkeeping, and item indexes; existing output-capture limits still apply. Exceeding the budget disables complete local recovery without blocking incremental continuation on a valid original socket and response ID.

With a Responses HTTP or WS upstream and complete history, intentional interruptions register completed output items as recoverable history for the current lane. Chat Completions, Anthropic, and other converted upstreams are not certified as complete Responses recovery history and do not gain response-ID continuation through this change. When the next turn references that ID, or the gateway knows before sending that the original socket expired or was evicted, complete retained history can be expanded into a new response with the old ID removed. Following turns return to incremental sends. Recovery preserves current parameters and input after plugin/body overrides, does not replay the interrupted generation, and does not require disk body storage. Page notifications retain their original representation under the channel-neutral contract below and are adapted before sending to the final selected channel. A valid credential or final account/address change on the same permitted channel releases the old strict origin binding only after complete history is expanded and the old ID removed; missing history still fails. Incomplete history returns `previous_response_not_found`; the client must clear the old ID and send full context. A disconnection discovered only during this turn's write or read still has a potentially ambiguous execution outcome and is never automatically replayed.

This in-memory recovery is restricted to the same client connection and `stream_id`. Cross-connection recovery, cross-lane forks, and `response.steer` are outside this capability. Closing the client connection releases its history and upstream connections. Origin and connection availability are checked again before dispatch, and execution details record the actual restored request body.

Published legacy `transport=websocket` and WS URLs are read as capability declarations. Editors recognize both the published `supportsWebSockets` flag and persisted standalone `openai/responses-ws` endpoints. Unchecking also removes the standalone WS endpoint while preserving ordinary Responses configuration. HTTP and WS share the Responses address, which should accept both POST and Upgrade requests.

Upstream Responses error events may omit an HTTP status. When a request fails before output starts, AxonHub's HTTP error response uses 502 while retaining the upstream error code and message; explicit upstream error statuses remain unchanged. This conversion only determines the client response and does not alter the recorded upstream status or retry decisions. Streams that have already started continue to report failure through error events.

An explicit `upstream_stream_read_error` without an available HTTP error status can enter the existing retry flow. Output must not have started, replay must remain safe, and retry budget must remain. Complete-history continuations prefer their original source and can use eligible fallback channels or addresses. Unresolved upstream response IDs retain origin requirements. Delivered output or an uncertain send outcome prevents replay.

When an upstream 429 includes `Retry-After`, AxonHub records a channel cooldown using the existing parsing rules and checks it before each send. Requests blocked by the cooldown do not reach that channel or consume RPM. Ordinary requests and complete-history continuations can use existing eligible candidates; unresolved upstream response IDs cannot switch known sources. With no available candidate, the client receives 429 and the remaining `Retry-After` and decides when to retry. A 429 without that header does not imply a cooldown. Initial rate limits and provider stream interruptions can still occur. This local refusal does not extend the cooldown, charge actual usage or trigger credential disabling, and retains the gateway's cooldown message. Actual upstream 429 errors continue to use configured hidden or custom messages.

### Cross-Thread Delivery and Tool Outputs

Codex can represent a task delivered by another thread as a standalone tool output with `id`, `namespace`, `name`, and `output`. If the receiving thread did not make the corresponding tool call, the output has no `call_id`. The output item's own `id` must not substitute for a call ID.

Responses-to-Responses conversion preserves the original tool-output `type`, `id`, `namespace`, name, and any actual `call_id`. Later turns replay the delivery as tool output. A `custom_tool_call_output` also retains its type when its matching call exists only in the response referenced by `previous_response_id`.

This does not require enabling WebSocket on the client. Direct HTTPS and safe WSS-to-HTTPS fallback before sending share these conversions. The upstream must still support standalone tool outputs. For a missing-`call_id` error, first compare these fields in the inbound and outbound requests, then check upstream support. Switching transport does not establish field compatibility, and ordinary HTTP parameter errors do not trigger protocol fallback.

### Per-channel Page context compatibility

Codex Desktop may attach standalone `agent.codex_apps_open_page` and `agent.codex_apps_selected_page_part_N_of_M` outputs when submitting a message. Even without an open Page, it sends `page_id: null` to clear earlier Page state. These notifications do not answer a model-issued tool call and have no `call_id`, so a new chat can still receive HTTP 400 from a provider that requires one.

For a Responses channel with this confirmed incompatibility, enable **Channels → Transform Options → Codex Page context compatibility**. It defaults to off and is stored as `settings.transformOptions.codexPageContextCompatibility`. Matching standalone Page outputs are represented as user-role text explicitly marked as untrusted external data, retaining the original notification fields, content, ordering, fragments, and null state. They are not user instructions, are never promoted to system/developer messages, and receive no fabricated call IDs.

Ordinary tool results, cross-thread deliveries, and unrecognized inputs retain their existing behavior. Adaptation runs immediately before dispatch for this channel, covering body pass-through, reconstructed requests, and restored continuation history. A fallback channel with the option off still receives the original input. Execution details record the adapted body actually sent. Turning the option off restores the original forwarding behavior without a client transport change or database migration.

### Complete Continuation History and Mode Billing

Restoration follows `previous_response_id` through every ancestor until a complete starting request or cached ancestor is available. Each lookup retains the current key and project scope. The 1 MiB limit controls cache admission, not usable history size: larger records can be used directly for the current request. Missing, cyclic, or malformed ancestors never produce partial input.

Native HTTP may retain the ID for upstream restoration. Transports requiring complete local input, including Codex HTTP and WS-to-HTTP bridging, reject missing history. Storage failures preserve their real cause and stop ineffective retries; cancellation stops immediately. Database failures are not mislabeled as missing history.

Only historical input and output are restored. The current model, `service_tier`, and other parameters remain current; previous usage and billing tiers are not inherited. See [Cost Tracking](cost-tracking.md#fast--ultrafast-and-long-context-costs) for pricing and markers.

### Related Documentation
- [Tracing Guide](tracing.md)
- [OpenAI API](../api-reference/openai-api.md)
- README sections on [Usage Guide](../../../README.en-US.md#usage-guide)


## beta12 compatibility endpoints

When Codex compatibility is enabled in system settings, use `/codex/responses`, `/codex/models`,
and `/codex/api/codex/accounts/check`. Existing `/v1/responses` and model catalog configurations remain available.
The compatibility model list uses this fork's aggregated catalog and the caller's model permissions.
Catalog metadata never grants inference access. Image generation and editing use `/codex/images/generations`
and `/codex/images/edits`; the selected inference channel must support the corresponding native image endpoint.
