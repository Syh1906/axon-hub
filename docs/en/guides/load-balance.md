# Adaptive Load Balancing Guide

AxonHub provides an intelligent adaptive load balancing system that automatically selects optimal AI channels based on multiple dimensions, ensuring high availability and optimal performance.

## 🎯 Core Features

### Intelligent Channel Selection
- **Priority Grouping** - Candidates are first grouped by effective channel priority (Lower value = Higher priority)
- **Session Consistency** - Requests from the same conversation are prioritized to route to previously successful channels
- **Health Awareness** - Automatically avoids channels with high error rates
- **Fair Distribution** - Uses request history among equal-priority, equal-weight candidates; different weights are ordered highest first
- **Latency Awareness** - Uses request-type-specific UX signals: streaming requests prioritize lower first-token latency and higher output throughput, while non-streaming requests prioritize lower end-to-end latency
- **Rate Limit Awareness** - Respects upstream RPM/TPM/concurrency limits, falls back to the default connection tracker when `MaxConcurrent` is not explicitly configured, and automatically cools down channels on 429 Retry-After

### Multi-Strategy Scoring System
Load balancing follows a hierarchical process: first by **effective priority** (key override, matching tag default, then association). Within a group, hard limits, exhausted quota, and failure backoff are respected before descending **channel weight**. Strategy scores only compare equal weights. Existing session reuse remains in effect. The scores below do not distribute traffic across different weight tiers.

| Level | Strategy | Score Range | Description |
|-------|----------|-------------|-------------|
| **1** | **Association Priority** | 0-N (Lower is better) | Hard grouping defined in model associations |
| **2** | **Trace Aware** | 0-1000 points | Same session priority, ensures conversation continuity |
| **3** | **Error Aware** | 0-200 points | Based on success rate and error history |
| **4** | **Weight Round Robin** | 10-150 points | Historical-load distribution within equal weights |
| **5** | **Latency Aware** | 0-80 points | Streaming requests use FTTL + TPS, non-streaming requests use end-to-end latency |
| **6** | **Rate Limit Aware** | -10000-100 points | Respects RPM/TPM/concurrency limits and 429 Retry-After |

## 🚀 Quick Start

### 1. Configure Multiple Channels
Add multiple channels for the same model in the management interface:

```yaml
# Channel A - Primary channel
name: "openai-primary"
type: "openai"
weight: 100  # High priority
base_url: "https://api.openai.com/v1"

# Channel B - Backup channel  
name: "openai-backup"
type: "openai"
weight: 50   # Medium priority
base_url: "https://api.openai.com/v1"

# Channel C - Third-party channel
name: "openai-third-party"
type: "openai"
weight: 30   # Low priority
base_url: "https://api.example.com/v1"
```

### 2. Enable Load Balancing
Load balancing is automatically enabled, no additional configuration needed. The system will:

- Automatically detect channel health status
- Sort channels based on strategy scores
- Intelligently select the optimal channel
- Automatically switch to the next channel on failure

### 3. Send Requests
Use standard OpenAI API format:

```python
from openai import OpenAI

client = OpenAI(
    api_key="your-axonhub-api-key",
    base_url="http://localhost:8090/v1"
)

# System will automatically select the optimal channel
response = client.chat.completions.create(
    model="gpt-4",
    messages=[{"role": "user", "content": "Hello!"}]
)
```

## Failure routing and feedback

With address failover enabled, connection failures, response timeouts, and 5xx statuses (including 524) can select the next enabled address within the existing retry budget. Raw HTTP errors and normalized protocol errors follow the same status check. The model, request body, and selected credential remain unchanged. Address exhaustion returns control to existing channel fallback. Statuses 400, 401, 403, and 429 do not trigger address failover; 429 also skips same-channel and mapped-model retries.

HTTP 413, or a 400 message containing both `image relay inline images exceed the configured` and `request limit`, indicates an upstream capacity rejection. It skips same-channel and mapped-model retries even if 400 is configured as retryable, while preserving existing cross-channel eligibility. Complete-history continuations can use eligible fallback channels; unresolved upstream IDs retain origin requirements. Without an eligible candidate, the request fails; history is not deleted and images are not compressed automatically. Other 400 errors are not treated as capacity failures. Provider image-relay size is not equivalent to the entire client JSON size.

When the provider supplies no error `code`, diagnostics add `upstream_request_too_large` (capacity rejection), `upstream_timeout` (504/524), `upstream_first_event_timeout` (first streaming event), or `upstream_response_timeout` (non-streaming response). Existing provider error fields are preserved. Exhaustion of the gateway's overall request deadline returns HTTP 504 with `request_deadline_exceeded` when `Process` returns an error before response delivery begins; client cancellation is not classified as an upstream timeout. Once streaming output is committed, its HTTP status cannot be rewritten: existing stream termination handling remains, and its timeout codes are not unified by this change. A gateway-generated 504 is not recorded as an actual upstream HTTP status. Existing error visibility policies still apply.

This does not add retries, change timeout durations, buffer entire outputs for retry, or replay committed streaming content. Requests still fail when backup paths are unavailable or providers keep rejecting their size.

## 📊 Load Balancing Strategy Details

### Model Association Priority
- **Purpose**: High-level traffic control and hard grouping.
- **Mechanism**: Candidates use the effective priority: key override, matching tag default, then model-association `priority`.
- **Rule**: Lower values have higher priority. All candidates in priority group `N` will be exhausted before any candidate in group `N+1` is considered.
- **Use Case**: Primary/Secondary channel separation, A/B testing (by setting the same priority and weight).

### Trace Aware Strategy
- **Purpose**: Maintain channel consistency for multi-turn conversations
- **Mechanism**: If request contains trace ID, prioritize previously successful channel
- **Advantage**: Avoids initialization delays from channel switching
- **Scoring**: Matching channel gets 1000 points, otherwise 0 points

### Delayed concurrency for Codex title requests

HTTP streaming requests marked `X-Codex-Turn-Metadata.thread_source=thread_title` may start a fallback attempt after 10 seconds without deliverable content when retries are enabled and multiple candidates exist. The delay launches a backup without canceling the original. The first attempt to produce actual output, refusal or tool output wins; the other is canceled. Startup and reasoning-only events do not select a winner and remain buffered for delivery with the winning output. A failed backup leaves the original running. Single-channel requests, disabled retries and unresolved upstream response IDs retain ordinary sequential execution and timeouts.

The strategy retains all ordinary candidates, model mappings, same-channel retries and backup-address rules; actual model names need not contain Luna. Ordinary chat, memory tasks, non-streaming and downstream WebSocket requests do not enable this concurrency. It does not extend the Codex App's title deadline.

At most two upstream attempts are live per title request. The fallback lane uses only remaining candidates and existing retry allowances; the last attempt is not interrupted by this delay. A failed original attempt retains the configured retry backoff. Health, permission, concurrency, rate and budget admission apply to every attempt. Insufficient budget for an optional backup rejects only that backup, leaving the admitted original running. An uncertain execution or other non-replayable error prevents new attempts while allowing already-dispatched work to finish. Body copying and fallback initialization are deferred until needed, so fast requests do not call an extra provider. Attempts share one logical request but retain independent execution, usage evidence and budget settlement; slow requests can incur additional provider charges. Losing cancellation is recorded as `hedge_lost` after stream/accounting finalization, without marking the logical request client-canceled or erasing actual channel failures from health statistics. Only the winner may publish the logical result, session ownership and history preference.

The logical request stores its adopted execution ID and channel together. Admin displays and request-level statistics read that execution; a later-created failed or canceled backup cannot replace the winner. A recorded client delivery status code takes precedence; otherwise the request row uses the adopted execution's response code. If the original succeeds with 200 and a later backup fails with 500, the row shows 200 while the routing details retain both actual outcomes. With live preview enabled, attempts have separate previews and the logical request attaches only the winning content after adoption.

### Responses encrypted continuation

Encrypted history is identified by the actual response ID or a SHA-256 fingerprint, within the same project and caller API key. Durable origin records verify continuation identity. Complete-history routing preferences are stored separately and scoped by the public model, rather than inferred from the latest trace/thread request.

**Complete-history preferences expire after one idle hour.** Successful responses bind the actual winning channel to the latest input/output history; a lookup hit renews the hour. Streaming preferences are written after successful terminal delivery and stream closure; reading an upstream completion alone is not delivery confirmation. Non-streaming preferences require successful processing and a request that has not been canceled. Failed, canceled and partial responses do not establish new preferences. Expired or missing preferences are never restored from durable origin records. A miss resumes ordinary session binding and then current priority/availability selection, restricted to compatible Responses routes. Ordinary bindings retain their existing 30-minute lifetime; the health-gated strategy's independent ownership policy is unchanged. Memory-mode restarts clear preferences, while Redis retains unexpired entries. Two-level configurations use Redis as the sole authority for this preference, preventing stale cache promotion.

**Complete history can switch providers.** When the client supplies all history or AxonHub fully expands `previous_response_id`, an unexpired eligible channel preference is used if session reuse is enabled. Disabled, deleted, unauthorized, model-ineligible, health-gated or cooling sources can yield to eligible Responses channels. Unavailable credentials or endpoints can use the current valid configuration. History and encrypted content remain intact; Chat Completions routes that cannot carry it are excluded. The actual upstream decides whether it accepts the ciphertext; arbitrary provider compatibility is not guaranteed.

The next turn prefers the latest source in its actual history: look up the original response ID first when present, otherwise the last encrypted input item. After A switches to B, mixed A/B history is not rejected locally for conflicting origins. An available original endpoint is tried first. With address failover enabled, remaining addresses follow configured priority, including backups ranked before the origin. Permission, health, budget and retry limits still apply.

**An unresolved upstream response ID retains strict origin requirements.** If full history was not restored and `previous_response_id` remains, a known channel, credential, endpoint and account must match. A different provider's unknown ID cannot stand in for complete history. Conflicts or unavailable known origins return the corresponding `409 continuation_error`: `continuation_origin_conflict`, `continuation_channel_disabled`, `continuation_channel_missing`, `continuation_candidate_unavailable`, `continuation_upstream_changed`, or `continuation_credential_unavailable`. Actual history restoration errors keep their existing behavior. Imported IDs without local origin evidence retain existing routing; provenance is not invented.

The independent `responses_origins` table records the channel, irreversible credential reference, and endpoint/account digest without additional ciphertext or credential bodies. Completed encrypted items, compaction results, interrupted snapshots, `response.done`, and successful responses without optional `status` attempt origin registration. Echoed old inputs retain their origin. Origin lookup or write failures produce diagnostics but do not block complete-history delivery, storage or routing, and do not regenerate successful output. Metadata is independent of debug-body and session-cache expiry and currently has no automatic expiry. It cannot replace missing history content.

Legacy evidence requires an exact response ID for a successful execution. A unique masked suffix can recover its credential; ambiguity under strict ID dependence returns `continuation_credential_ambiguous`. Without credential evidence, the first attempt uses existing key selection and retries retain that chosen key. `.syh.15` added the origin table and request failure field; this routing update adds no migration or old-ciphertext backfill.

Automatic retries require no delivered meaningful output, a safely replayable send outcome and remaining budget. A preferred-source attempt does not rotate mapped models to hide ciphertext errors. Eligible fallback channels still follow existing error retry rules, without modifying or dropping history. Delivered output or an uncertain execution outcome prevents replay; unfinished tool arguments remain unfinished. Requests are registered before restoration and routing, so failures appear in diagnostics without inventing upstream executions.

During Responses stream conversion, tool-input deltas are forwarded incrementally. When the upstream explicitly marks an individual tool as `completed`, the gateway forwards any missing input suffix and its completion promptly. A later failure in another tool does not revert the completed tool. Starting another interleaved tool leaves earlier tools open. Without an explicit per-tool completion signal, synthesized completion waits until the response finishes successfully, so clients that wait for those events may execute tools later in these requests. An abnormal terminal event already sent to the client suppresses an additional failure event. Failed, incomplete, and cancelled outcomes are preserved with or without usage information.

### Error Aware Strategy
- **Purpose**: Avoid unhealthy channels
- **Base Score**: 200 points for healthy channels
- **Scoring Factors**:
  - Consecutive failures: -30 points per failure, decaying over the cooldown window
  - Recent failure (within 5 min): up to -40 points (time-decaying)
- **Recovery**: Failed channels automatically recover priority over time as the time-decay penalty decreases.

### Weight Round Robin Strategy
- **Purpose**: Historical-load distribution within equal weights.
- **Algorithm**: Normalizes historical request counts by channel weight. This score only compares equal weights; different weight tiers are no longer distributed proportionally.
- **Scoring**: `150 * exp(-normalized_request_count / 150)`
- **Range**: 10-150 points

### Latency Aware Strategy
- **Purpose**: Prioritize channels using latency signals that match actual user experience
- **Mechanism**:
  - **Streaming requests**: score channels by EWMA first-token latency (FTTL/TTFT) and EWMA output throughput (tokens per second)
  - **Non-streaming requests**: score channels by EWMA end-to-end latency
- **Scoring**:
  - **Streaming**: `80 * (0.7 * first_token_component + 0.3 * throughput_component)`
  - **Non-streaming**: `80 * (1 - latency_ewma / 3000)`, clamped to [0, 80]
- **EWMA Alpha**: 0.3 (recent requests have higher influence)
- **Neutral Score**: Channels without latency data receive a neutral score of 40 points
- **Range**: 0-80 points

### Rate Limit Aware Strategy
- **Purpose**: Respect upstream provider rate limits and prevent 429 errors
- **Mechanism**: Tracks per-channel RPM (Requests Per Minute), TPM (Tokens Per Minute), and concurrent request counts using a 1-minute sliding window
- **Scoring**:
  - Full score (100 points) when neither explicit rate limits nor default connection saturation signals apply
  - Score decreases linearly as usage approaches the limit: `100 * (1 - max_usage_ratio)`
  - **-10000 points** (ranked last as fallback) when any limit is exhausted
- **429 Retry-After**: When a channel returns HTTP 429 with a `Retry-After` header, the channel enters a cooldown period and receives -10000 points until the cooldown expires
- **Configuration**: Set RPM, TPM, and Max Concurrent limits per channel in the management interface under **Rate Limit** settings
- **Concurrency Fallback**: If `MaxConcurrent` is not configured but the default connection tracker has a per-channel capacity, adaptive balancing still penalizes channels with many in-flight requests and treats fully saturated channels as exhausted fallback candidates

### Health-Gated Strategy (health-gated)
- **Purpose**: Remove persistently failing "channel × upstream model" combinations from routing, keep one-off failures scoped to the current request, and show exactly which combination is broken in the admin UI.
- **How to enable**: Set the load balancer strategy to "Health-gated" in the system retry policy, an API key profile, or model settings. The other strategies are unchanged.
- **Ordering**: New sessions and sessions whose ownership has expired first follow model association priority (lower first). Within each group, hard-unavailable candidates are deferred, then channel weight is ordered highest first. Historical load, latency, and quota scores break ties only among equal weights. An available new weight-15 channel therefore precedes a weight-10 channel in the same association priority group even without latency samples.
- **Granularity**: Tracked per channel and the actual upstream model after model mapping; one broken model does not affect other models on the same channel.
- **States**:
  - **Healthy / Unstable**: Routed normally; shown as unstable when a failure or a post-first-token stream break happened within the last W minutes. Unstable is display-only and not penalized.
  - **Open**: After N consecutive counted failures the combination is skipped during routing, including sticky session channels (the sticky record itself is kept).
  - **Probing**: When the open period expires, only new sessions without a sticky channel are admitted, one request at a time; M consecutive successes recover it, a failure reopens it with a doubled duration (capped).
- **What counts**: 5xx, network errors, timeouts, empty responses, stream breaks before the first token, and 401/403 count as failures; 429, request errors such as 400/404/422, client cancellation, and local rate-limit/queue rejections are not counted.
- **When everything is open**: The earliest-expiring combination is tried once; a counted failure returns a generic 503 without channel names, while request errors (e.g. 400) are returned as-is.
- **Defaults**: N=5, first open period 5 minutes, cap 60 minutes, M=2, W=5 minutes, K=2. A channel can override N; 0 disables circuit breaking for that channel.
- **Session ownership** (when stickiness is "prefer previous channel"): ownership is established only after success, with the thread authoritative when present. During the protection window the owner is retained to avoid switching on one-off failures; an open owner or K consecutive successful failovers moves ownership. The window defaults to 5 minutes, or 1 hour when the final outbound Anthropic request declares a valid 1-hour cache breakpoint. This follows the requested cache lifetime, not observed provider cache hits. Only success on the owner or a committed migration refreshes the window; temporary failover does not renew the old owner's protection. Expired sessions route using current configuration and cannot revive ownership from legacy sticky records. Adding a channel does not force migration while the protection window is valid.
- **Admin UI**: The channel list shows open/unstable model counts with an "abnormal only" filter; the health detail view lists each model's state, last error, and remaining time, with manual reset. The request detail "Routing decision" card shows the session owner, skipped combinations, whether this was a temporary failover or a last resort, and whether the session moved and why.
- **Notifications**: Webhooks can subscribe to `channel.health_gate_opened` (first open) and `channel.health_gate_recovered` (probe recovery); failed probes during an open period do not notify again.
- **Cancellation**: Configuration reads and channel observation lock waits honor the request deadline, including admin health queries. Cancellation does not apply fallback configuration or count as an upstream health failure. Attempt finalization and session/routing persistence each have a separate 10-second cleanup budget; session lock waits share that budget. A timed-out finalization releases only its own probe slot without marking recovery.
- **Limitations**: State lives in process memory only; it resets on restart and is not shared across instances.
- **Ownership storage**: With two-level caching configured, health-gated ownership reads and writes Redis directly so delayed local-cache promotion cannot restore an old owner. Without Redis it uses memory. Existing bindings are reevaluated once after upgrading.

## 🔧 Advanced Configuration

### Enable Debug Mode
View detailed load balancing decision process by setting the environment variable.

```bash
# Set environment variable
export AXONHUB_DEBUG_LOAD_BALANCER_ENABLED=true
```

### View Decision Logs
```bash
# View load balancing decisions
tail -f axonhub.log | grep "Load balancing decision"

# View specific channel scoring
tail -f axonhub.log | grep "Channel load balancing details"

# Use jq to format JSON logs
tail -f axonhub.log | jq 'select(.msg | contains("Load balancing"))'
```

## 📈 Monitoring and Troubleshooting

### Key Metrics
- **Channel switching frequency** - Should be relatively low under normal conditions
- **Error rate distribution** - High error rate on a channel may indicate configuration issues
- **Response time** - Load balancing should optimize overall response time

### Common Issues

**Q: Why do requests always route to the same channel?**
A: Check if session consistency is enabled. Requests with the same trace ID will prioritize the same channel.

**Q: What to do if channels don't switch?**
A: Check Error Aware strategy scoring. The channel may still be healthy or needs time to recover.

**Q: How to verify load balancing is working?**
A: Enable debug mode and view channel scoring and sorting in logs.

## 🎛️ Best Practices

### 1. Channel Configuration
- Set different weight values to reflect priorities
- Configure multiple different provider channels for higher availability
- Regularly check channel health status

### 2. Monitoring Setup
- Monitor error rates and response times for each channel
- Set alerts when a channel continuously fails
- Regularly analyze load balancing decision logs

### 3. Performance Optimization
- Adjust channel priorities based on cost considerations
- Use session consistency to improve user experience

## 🔗 Related Documentation

- [Request Processing Guide](../getting-started/request-processing.md)
- [OpenAI API](../api-reference/openai-api.md)
- [Anthropic API](../api-reference/anthropic-api.md)
- [Gemini API](../api-reference/gemini-api.md)
- [Channel Management Guide](channel-management.md)
- [Tracing and Debugging](tracing.md)
