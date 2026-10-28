# API Key Profile Guide

This guide explains how to configure API Key Profiles for model mapping, access control, and profile switching.

## What is an API Key Profile?

An **API Key Profile** lets you:
- **Map models**: Rewrite the model name from the client request
- **Restrict channels**: Limit the API Key to specific channels
- **Restrict models**: Limit the API Key to specific models
- **Switch profiles**: Create multiple profiles for one API Key and choose which one is active

In simple terms, an API Key Profile decides at the **request entry point** what model the request should be treated as first.

## Where API Key Profile Fits in the Request Flow

API Key Profile model mapping is the **first** step in a three-layer pipeline. For the full picture, see [Request Processing Guide](../getting-started/request-processing.md#core-concept-three-layers-of-model-settings).

In short: **API Key Profile renames → Model Association selects channel → Channel renames → Send upstream**

## Common Use Cases

### Use Case 1: Client tools with fixed model names

Many AI tools use fixed model names internally. If you want them to use other models, use API Key Profile model mapping.

```json
{
  "modelMappings": [
    {"from": "claude-sonnet-4-5", "to": "anthropic/claude-3.5-sonnet"}
  ]
}
```

### Use Case 2: Unify model names across clients

```json
{
  "modelMappings": [
    {"from": "gpt4", "to": "gpt-4o"},
    {"from": "gpt-4-turbo", "to": "gpt-4o"}
  ]
}
```

### Use Case 3: Restrict a profile to part of the system

```json
{
  "channelTags": ["production"],
  "modelIDs": ["gpt-4o", "claude-3-sonnet"]
}
```

## Configuration Steps

### Step 1: Open the profile UI

1. Log in to the AxonHub management interface
2. Go to **API Keys**
3. Find the API Key you want to configure
4. Open the **Actions** menu
5. Select **Profiles** or **Configure**

### Step 2: Create a profile

1. Click **Add Profile**
2. Enter a profile name
3. Configure model mappings, channel restrictions, or model restrictions

### Step 3: Configure model mappings

Each mapping has:
- **From**: the model name in the client request
- **To**: the model name that AxonHub should use

Supported matching methods:

#### Exact match

```json
{"from": "gpt-4", "to": "claude-3-opus"}
```

#### Regex match

```json
{"from": "gpt-.*", "to": "claude-3-sonnet"}
```

### Step 4: Set the active profile

1. Select a profile in **Active Profile**
2. Click **Save**
3. The change takes effect immediately

## Channel priorities and templates

See [tag priorities](channel-management.md#tag-priorities-and-quick-list-settings) for key/project tag inheritance.

Expand the active profile in API Key profile management and add entries under **Channel priorities**.
Smaller numbers are tried first. Unset channels inherit matching tag defaults, then model-association priority; **Restore inheritance** removes the override without removing the channel.

For two keys allowing the same channels A and B, set A=10/B=20 on one and A=20/B=10 on the other.
Channel and tag restrictions define the permitted set. Priorities only reorder that set and never broaden access or change global channel weight.
Equal priorities use descending channel weight, then the configured load balancer for equal weights. Session stickiness may keep an existing conversation on its prior channel;
enable **Do not reuse session channel** in that channel's request settings when it should not receive this preference.

Priorities participate in saving, template import/export, and linked-template synchronization together with the full profile.
Publishing a template updates linked profiles; directly editing and saving a key's profile detaches it. Templates synchronize the entire profile, not only priorities.

## Rule Matching Order

Model mappings are evaluated in order. **The first matching rule is applied.**

Put more specific rules first and more general rules later.

## FAQ

### Q: Why is model mapping not working?

Check:
1. Is the correct active profile selected?
2. Does the model name match?
3. Is the regex pattern correct?

### Q: What are the requirements for profile names?

- Must be unique within one API Key
- Cannot be empty
- Should be meaningful

### Q: How many profiles should I create?

There is no hard limit, but keep the set small and easy to manage.

## Best Practices

1. **Use descriptive names** like `production` or `openrouter-mapping`
2. **Put specific rules first**
3. **Test before enabling**
4. **Prefer channel tags** over hardcoded channel IDs

## Related Documentation

- [Model Management Guide](model-management.md) - Configure model association
- [Channel Management Guide](channel-management.md) - Configure upstream channels
- [Load Balancing Guide](load-balance.md) - Understand channel selection and failover
- [Request Processing Guide](../getting-started/request-processing.md) - See the full request flow

## Usage shortcut

Enabled user and personal keys have a Usage button beside the masked key. It opens the existing self-service usage page for that key, clearing the previous key query cache first. The key passes through tab-scoped session storage, never the URL. Existing key type, status, project status and IP checks remain in force.
