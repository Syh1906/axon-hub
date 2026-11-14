# Request Processing Guide

This guide explains the main steps a request goes through in AxonHub. Its goal is to give you a simple and accurate mental model.

## One-Sentence Summary

AxonHub converts the request into a unified format, selects a channel based on configuration, and converts the result back into the format the client expects.

## Core Concept: Three Layers of Model Settings

AxonHub has three places that affect model names:

| Layer | Configuration Location | Purpose | Simple Analogy |
|-------|------------------------|---------|----------------|
| Layer 1 | API Key Profile | Rewrite the client's requested model name | "What to ask for" |
| Layer 2 | Model Association | Choose which channel handles the request | "Where to go" |
| Layer 3 | Channel Configuration | Rewrite the model name sent upstream | "What to call it" |

**Processing Order**: API Key Profile renames → Model Association selects channel → Channel renames → Send upstream

## End-to-End Flow

```mermaid
flowchart TD
    A[Client Request] --> B[1. Protocol Transform]
    B --> C[2. Permission Check]
    C --> D[3. Model Mapping]
    D --> E[4. Channel Selection]
    E --> F[5. Content Processing]
    F --> G[6. Load Balancing]
    G --> H[7. Request Rewrite]
    H --> I[8. Call Upstream]
    I --> J[9. Retry on Failure]
    J --> K[10. Response Transform]
    K --> L[Return to Client]
```

## Stage Overview

### 1. Protocol Transform

AxonHub supports multiple API formats, such as OpenAI, Anthropic, and Gemini. It first converts them into one internal format.

### 2. Permission Check

The system checks the basic limits of the API Key and the active Profile, such as:
- whether the API Key is valid
- whether quota is exceeded
- whether the requested model is allowed

If this step fails, the request stops here.

### 3. Model Mapping

This is the first layer of model-name rewriting, controlled by the API Key Profile.

Example:

```
Client request: gpt-4
    ↓
API Key Profile: gpt-4 → claude-3-opus
```

### 4. Channel Selection

The system uses Model Association to decide which channels can handle the request.

This step usually considers:
- model-level association rules
- developer rules from the same model developer, unless the model has **Do not inherit developer settings** enabled
- API Key Profile channel restrictions
- whether the channel is enabled

Developer rules select only a channel or channel tags. The concrete upstream model is matched from the current request's model ID, so one developer rule can serve multiple models from the same developer.

### 5. Content Processing

Before sending the request upstream, AxonHub may also apply:
- prompt injection
- prompt protection

### 6. Load Balancing

If there are multiple candidate channels, AxonHub decides which one to try first.

Common factors include:
- channel weight
- historical state
- current load

### 7. Request Rewrite

At the channel layer, AxonHub can further rewrite the upstream model name and request parameters.

### 8. Call Upstream

The system sends the request to the selected AI provider.

AxonHub-owned native HTTP clients isolate connection pools by upstream origin while continuing to reuse healthy connections. If a connection has been obtained but the request upload times out, subsequent requests use a new pool for that origin. The old pool stops accepting new requests and is reclaimed after its existing responses finish. User cancellation, connection establishment failures, parameter errors, and response timeouts after upload do not trigger this recovery.

Each origin starts with one pool lane. The backend automatically detects concurrent upload pressure without per-channel or per-machine configuration. When at least two unfinished uploads share an HTTP/2 connection, a known body of at least 64 KiB has spent at least two seconds uploading, and full body size divided by elapsed time is no greater than 256 KiB/s, a subsequent real request may try one additional lane. Equivalent evidence observed when an upload completes is retained for one minute, but is reused only when the next burst already has an identifiable, connected HTTP/2 upload. Connection establishment, model waiting after upload, early response headers, and user cancellation are not slow-upload evidence. Requests prefer existing lanes with fewer pending uploads, then fewer active responses, rotating ties.

Expansion adds one lane at a time, at least ten seconds apart, up to eight lanes. Within one minute, the new lane must complete two HTTP/2 uploads at least as large as the slow sample, receive non-error response headers, and take at least 25% less upload time per byte in each case before further expansion is allowed. Missing comparable evidence, insufficient improvement, or an error response pauses expansion for one minute. If another admitting lane remains, the trial lane stops accepting new work and drains its responses before reclamation. A trial already idle when rejected is reclaimed immediately, even if the idle timeout is zero. Each idle lane expires independently using the existing idle timeout. This is a bounded transport optimization, not a diagnosis of network failure or a speed guarantee. It sends no synthetic probes and does not relocate dispatched requests. Explicit per-host connection caps and disabled connection reuse suppress automatic expansion.

Each lane retains at most one current generation and one draining generation. After its last response ends, the old generation closes its owned connections; another recovery for that lane cannot create a third generation until those connections have actually closed. Before another recovery is allowed, the replacement must upload a body at least as large as the failed request and receive response headers, in either order; a small successful request does not validate large uploads. Externally injected HTTP clients, custom TLS dialers, SDK-owned connections, and native WebSockets are not taken over. State is local to the process and isolated by HTTP client and origin, with no persistent cache or database migration and no changes to channel, model, or provider selection.

Transport timing logs include the pool generation, connection identity, reuse status, and actual HTTP protocol. Recovery, expansion actions, comparable-upload verification, and completed uploads taking at least two seconds are logged at info level. `pool_replaced` means future requests use a new generation; `upload_verified` confirms a comparable or larger upload, not successful completion of the model request. `pool_count` is the lane count at allocation time, potentially including draining lanes, not a TCP connection count. `pool_action` values `expanded`, `expansion_verified`, and `expansion_unhelpful`/`expansion_unverified` identify trial capacity, improved uploads, and insufficient improvement/evidence. `baseline_reopened` identifies restored baseline capacity while a rejected trial still drains. Diagnostics contain neither payloads nor credentials.

### 9. Retry on Failure

If the upstream call fails, AxonHub may retry or switch to the next candidate channel.

Pool scaling and recovery are independent of request retries: adding or replacing a pool does not resubmit the original request, increase retry budgets, or change timeout settings. Existing restrictions on replaying requests with an unknown execution outcome remain in effect.

### 10. Response Transform

The system converts the upstream result back into the format the client expects and records the result.

Streaming requests record upstream execution and client delivery separately. For example, if the provider completes a response but the client connection is canceled before the gateway sends the final completion event, the execution remains completed while the request is canceled. Diagnostics identify the response delivery stage and cancellation cause. Write failures and deadlines remain failed; missing delivery acknowledgement without an explicit cancellation does not establish success or cancellation. A disconnect after the final event was successfully sent does not undo completion.

HTTP 200 only indicates that the streaming response started, not that the client received the complete result. Interrupted delivery neither removes recorded upstream usage nor automatically resubmits generation. New delivery diagnostics do not backfill historical requests; an upstream completion alone cannot establish successful delivery for an older record lacking evidence.

## Which Document to Read Next

| If your question is | Read this |
|---------------------|-----------|
| How do I rename the requested model? | API Key Profile |
| How do I choose which channel handles the request? | Model Association |
| How do I change the upstream model name or request parameters? | Channel Management |

## Related Documentation

- [Channel Management Guide](../guides/channel-management.md)
- [Model Management Guide](../guides/model-management.md)
- [API Key Profile Guide](../guides/api-key-profiles.md)
- [Load Balancing Guide](../guides/load-balance.md)
