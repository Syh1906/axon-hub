# Model Management Guide

This guide explains how to manage AI models in AxonHub and use "Model Associations" for intelligent routing.

## Import a model card from the catalog

The right-side **Model card** header provides **Import model card from catalog**. Select a developer and catalog model, preview capabilities, modalities, limits and prices, then import. It replaces the whole right-side card: tool calling, vision, temperature, reasoning support/default, input/output modalities, pricing, context/output limits, knowledge cutoff and release/update dates. The left-side developer, model ID, matching rules, public IDs, name and group stay unchanged. Save the model to persist.

Missing catalog fields become empty or disabled. Models without catalog prices remain importable and clear current pricing. Prices include base/cache items and catalog volume tiers; old schedules are removed. Selecting a catalog model on the left still automatically fills empty pricing, while existing zero prices, items, tiers and schedules remain. Both entry points share catalog conversion without currency conversion.

## Core Concepts: Models and Channels

### Simple Analogy

Imagine you're sending a package:
- **Model** = The type of item you want to send (e.g., "document", "package")
- **Channel** = Different courier companies (e.g., "SF Express", "YTO")
- **Model Association** = Your rule: "Documents go via SF Express, packages via YTO; if SF fails, use YTO"

In AxonHub:
- **Model**: An abstract name you expose, like `gpt-4` or `claude-sonnet`
- **Channel**: An actual AI provider connection
- **Model Association**: Determines which channel and actual model to use when a client requests a model
- **Developer Rule**: A reusable channel rule configured once for a model developer and inherited by models from the same developer

### Request Flow

```
Client Request: "Please answer using gpt-4"
        ↓
System Lookup: What associations does gpt-4 have?
        ↓
Association Resolution: Priority 0 → OpenAI channel's gpt-4-turbo
                       Priority 1 → DeepSeek channel's deepseek-chat
        ↓
Load Balancing: Select best channel to execute request
```

## Where Model Association Fits in the Request Flow

Model Association is the **middle** step in a three-layer pipeline. For the full picture, see [Request Processing Guide](../getting-started/request-processing.md#core-concept-three-layers-of-model-settings).

In short: **API Key Profile renames → Model Association selects channel → Channel renames → Send upstream**

## Developer Rule Inheritance

Rule-based models inherit developer associations using the concrete request ID after API Key mapping, rather than the matching expression.

Built-in developer IDs are case-insensitive: typing `OpenAI` or `OPENAI`, or selecting OpenAI from the catalog, stores `openai`. Existing casing variants share one developer group and inherit the same rules. Custom developer names retain their original spelling.

If multiple models from the same developer should use the same channel rules, configure **Developer Rules** on the developer group in the model list. A developer rule only selects a channel or channel tags. It does not lock in a specific upstream model; at routing time, each concrete model uses its own model ID when searching those channels.

By default, a model inherits developer rules from the same developer and merges them with rules configured directly on the model:

- Smaller `priority` values run first.
- When priorities tie, model-level rules take precedence over inherited developer rules.
- Enable **Do not inherit developer settings** in a model's association dialog when that model should use only its own rules.

## Model Association Types

Model associations are routing rules. The following types can be combined:

### Request model on a selected channel

The **Use request model on channel** association sends the model ID after API Key mapping unchanged to the selected channel.
The ID does not need to be listed in that channel's model catalog. Existing channel permissions, status, protocol capabilities, and association conditions still apply.
Combine this with fixed-model associations when one channel accepts `gpt-6.1-luna` while another requires `gpt-5.6-luna`.
The association preview accepts a test request ID without contacting the upstream.

### 1. Specific Channel, Specific Model (Most Precise)

**Purpose**: Precise control over which model version goes through which channel

**Configuration in Admin UI:**
- Association Type: "Specific Channel Model"
- Priority: 0
- Channel: OpenAI (ID: 1)
- Model: gpt-4-turbo

**Scenario**: "When user wants gpt-4, prioritize using OpenAI channel's gpt-4-turbo"

### 2. Specific Channel, Regex Match (More Flexible)

**Purpose**: Match a batch of models in a specific channel

**Configuration in Admin UI:**
- Association Type: "Channel Regex Match"
- Priority: 1
- Channel: DeepSeek (ID: 2)
- Pattern: `gpt-4.*`

**Scenario**: "All models starting with gpt-4 in the DeepSeek channel are acceptable"

**Common Patterns:**
- `gpt-4.*` — Matches `gpt-4`, `gpt-4-turbo`, `gpt-4-vision`
- `claude-3-.*-sonnet` — Matches `claude-3-5-sonnet`, `claude-3-opus-sonnet`
- `.*` — Matches all models

### 3. All Channels, Regex Match (Most Flexible)

**Purpose**: Find matching models across all enabled channels

**Configuration in Admin UI:**
- Association Type: "Global Regex Match"
- Priority: 2
- Pattern: `gpt-4.*`
- Exclude Channels with Tags: `test`

**Scenario**: "Any gpt-4 series model from any channel, but exclude test channels"

### 4. All Channels, Specific Model

**Purpose**: Don't specify channel; any channel supporting this model can be used

**Configuration in Admin UI:**
- Association Type: "Global Model Match"
- Priority: 3
- Model: gpt-4

**Scenario**: "Any channel supporting gpt-4 can be a backup"

### 5. Tagged Channels, Specific Model

**Purpose**: Select based on channel tags (e.g., only production environment channels)

**Configuration in Admin UI:**
- Association Type: "Tagged Channel Model"
- Priority: 4
- Channel Tags: production, high-performance
- Model: gpt-4

**Scenario**: "Only look for gpt-4 in channels tagged as production or high-performance"

### 6. Tagged Channels, Regex Match

**Purpose**: Tag + regex combination

**Configuration in Admin UI:**
- Association Type: "Tagged Channel Regex"
- Priority: 5
- Channel Tags: openai, azure
- Pattern: `gpt-4.*`

**Scenario**: "Find gpt-4 series models in OpenAI or Azure channels"

## Priority Settings

An active API Key profile can override priorities within its already permitted channels. See [Channel priorities and templates](api-key-profiles.md#channel-priorities-and-templates).

**Smaller priority value = Higher priority**

Recommended settings:
- **Primary channels**: Priority 0-10
- **Backup channels**: Priority 10-50
- **Emergency channels**: Priority 50-100

Example configuration in Admin UI:
- Association 1: Priority 0 (Highest priority: Primary)
  - Type: Specific Channel Model
  - Channel: OpenAI
  - Model: gpt-4o
- Association 2: Priority 10 (Lower priority: Backup)
  - Type: Global Model Match
  - Model: gpt-4

## Entry matching and public model IDs

Use the existing add/edit model dialog to select **exact**, **prefix**, **suffix**, or **regex** matching, then configure associations.
Prefixes and suffixes are literal strings. Regex matches the full ID; for example, `^gpt-.*-luna$` accepts `gpt-6-luna` and `gpt-6.1-luna`.
The test model ID uses the same backend matcher as routing and reports match, no match, or invalid expression without saving or calling a provider.
Testing is optional. **Create** saves the model and returns to the list by default. Choose **Create and associate** from the adjacent creation-options menu to continue into the association dialog. Enable the model when configuration is ready.

Enabled exact entries take precedence. Otherwise, the first matching enabled rule wins by ascending match priority, then ascending configuration ID.
Disabled and archived configurations do not participate. If the selected configuration has no usable association, the request fails instead of trying lower-priority rules.

**Public model IDs** is a separate list of zero, one, or multiple concrete IDs. Every ID must match the configuration's rule.
An empty list hides the configuration from model discovery and detail endpoints while matching requests can still call it.
OpenAI, Anthropic, and Gemini lists share these IDs; expressions and unbounded possible matches are never enumerated.
API Key aliases are listed only when mapped to a published ID, while model allowlists still check the original client ID and channel restrictions still apply.
All published IDs share the configuration's capability card; use separate configurations when capabilities differ.

Existing exact models continue publishing their original ID after upgrade until explicitly cleared and saved.
Discovery and routing resolve overlapping rules identically, so a lower-priority configuration cannot publish an ID hidden by the winning configuration.

## System Settings

Formal requests require an enabled registered model after API-key mapping. Only its effective
developer/model associations select channels; missing, disabled, archived or unassociated models
cannot fall back to channels. Public model APIs expose only enabled registered targets and valid
aliases. Channel connectivity tests remain an authorized administrative operation.

| Setting | Default | Description |
|---------|---------|-------------|
| Hide Unroutable Models in Lists | Disabled | When enabled, public model-list APIs hide configured models that the current API key cannot structurally route to a capable channel. Requests for those models still return 422. The admin models table is unchanged. |

Enable Hide Unroutable Models in Lists to additionally suppress registered models without a
structurally usable association for the current API key. Request-time conditions can still reject a
listed model. The former channel fallback, channel-union listing and channel blacklist settings
have been removed; old stored values cannot restore bypass behavior.

## FAQ

### Q: Why does the request say "Model not found"?

Check in order:
1. Is the resolved target model created and enabled (not archived), including API-key mapping targets?
2. Are model associations configured with correct channels?
3. Are channels enabled?
4. Do channels support the models specified in associations?
5. If the model should inherit developer rules, is **Do not inherit developer settings** disabled?

### Q: How to verify associations are working?

1. Send a test request
2. Check the Trace in the console to see which channel the request actually went through
3. Check logs for candidate selection records

### Q: Will too many associations affect performance?

Generally no significant impact, but recommended:
- No more than 10 association rules per model
- Avoid overly complex regex patterns

### Q: Will the same (channel, model) combination be duplicated?

No, the system automatically deduplicates.

## Best Practices

1. **Naming Convention**: Use standardized model names like `gpt-4`, `claude-3-opus`
2. **Priority Planning**: Primary 0-10, backup 10-50, emergency 50-100
3. **Use Tags**: Tag channels (e.g., production, test) for batch management
4. **Test Before Enabling**: Verify request routing in the console after configuration
5. **Regular Review**: Clean up unused models and association rules

## Related Documentation

- [Channel Management Guide](channel-management.md) - Configure AI provider channels
- [API Key Profiles Guide](api-key-profiles.md) - Configure model mappings
- [Load Balancing Guide](load-balance.md) - Learn about channel selection and failover
- [Request Processing Guide](../getting-started/request-processing.md) - Complete request flow explanation

## Bulk associations

The model ID cell displays the stored Exact, Prefix, Suffix or Regex match type, without inferring it from the expression. Bulk priorities share the existing editor range of integers from 0 to 10. Preview entries for the same channel are merged, listing each model once.

Select models in the list and choose Bulk associate. All seven single-model types are available: channel request passthrough, channel exact/regex, channel-tag exact/regex, and global exact/regex. Channel and tag types accept multiple targets; global types need no target selection. Fixed models can be selected from the target model list or entered manually; exact models default to their own model ID. Regex previews use the backend matcher. Request passthrough saves a dynamic rule rather than freezing a request or preview model name. Preview shows additions and duplicates. Unmatched entries are excluded from saving. Saves run per model with individual results; failed entries can be retried without resubmitting successful ones. Each save rereads the model and appends rules while preserving other settings. The batch is not a cross-model transaction.

Within one model association dialog, Add in bulk searches channels, tags and models and selects filtered results or same-name models across channels. Existing enabled, unconditional rules with the same target are skipped. Disabled or conditional rules remain and do not prevent a new unconditional rule. Additions first enter the edit draft and take effect after Save in the original dialog. Cancelling selection preserves the original draft. The former ten-rule UI limit is removed; long lists scroll inside the dialog.
