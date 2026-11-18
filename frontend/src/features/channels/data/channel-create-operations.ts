export const CREATE_CHANNEL_MUTATION = `
  mutation CreateChannel($input: CreateChannelInput!) {
    createChannel(input: $input) {
      id
      type
      createdAt
      updatedAt
      baseURL
      websiteURL
      name
      status
      policies {
        stream
        apiKeyAutoDisableMode
        apiKeyAutoDisableRules {
          statusCodes
          keywordPatterns
          times
          action
          disableDurationMinutes
          disableUntilCron
          disableUntilTimezone
        }
      }
      supportedModels
      autoSyncSupportedModels
      autoSyncModelPattern
      manualModels
      tags
      defaultTestModel
      settings {
        globalPriority
        tagPriorities { tag priority }
        modelPriceMultiplier
        healthGateFailureThreshold
        streamFirstEventTimeoutSeconds
        nonStreamResponseTimeoutSeconds
        disableSessionReuse
        allowImageGenerationTool
        responsesServiceTierOverride
        responsesTierSupport { fast ultrafast allowDowngrade }
        modelContexts { modelId defaultContextWindow maxContextWindow }
        upstreamAddresses { id name url priority enabled }
        addressFailover
        extraModelPrefix
        modelMappings {
          from
          to
        }
        autoTrimedModelPrefixes
        hideOriginalModels
        hideMappedModels
        lowercaseModelId
        quotaRoutingMode
        proxy {
          type
          url
          username
          password
          disableConnectionReuse
        }
        transformOptions {
          codexPageContextCompatibility
          forceArrayInstructions
          forceArrayInputs
          replaceDeveloperRoleWithSystem
          reasoningEffortMapping { from to }
        }
        passThroughUserAgent
        passThroughBody
        forceOneHourPromptCache
        retryableStatusCodes
        apiKeyStrategy
        apiKeyRoundRobinSwitchAfter
        retryableErrorPatterns {
          pattern
          regex
        }
        primaryApiFormat
        maxSingleChannelRetries
        protocolPolicy { allowedProtocols sameProtocolOnly }
            modelProtocols {
          model
          apiFormats
          enabled
        }
        providerQuota {
          commandCode {
            authCookie
          }
          ollama {
            authCookie
          }
        }
      }
      orderingWeight
      remark
      defaultEndpoints {
        apiFormat
        path
        baseURL
        transport
        supportsWebSockets
        catalogURL
        catalogJSON
      }
      endpoints {
        apiFormat
        path
        baseURL
        transport
        supportsWebSockets
        catalogURL
        catalogJSON
      }
    }
  }
`;

export const DUPLICATE_CHANNEL_MUTATION = `
  mutation DuplicateChannel($sourceID: ID!, $input: CreateChannelInput!) {
    duplicateChannel(sourceID: $sourceID, input: $input) {
      id
      type
      createdAt
      updatedAt
      baseURL
      websiteURL
      name
      status
      policies {
        stream
        apiKeyAutoDisableMode
        apiKeyAutoDisableRules {
          statusCodes
          keywordPatterns
          times
          action
          disableDurationMinutes
          disableUntilCron
          disableUntilTimezone
        }
      }
      supportedModels
      autoSyncSupportedModels
      autoSyncModelPattern
      manualModels
      tags
      defaultTestModel
      settings {
        globalPriority
        tagPriorities { tag priority }
        modelPriceMultiplier
        healthGateFailureThreshold
        streamFirstEventTimeoutSeconds
        nonStreamResponseTimeoutSeconds
        disableSessionReuse
        allowImageGenerationTool
        responsesServiceTierOverride
        responsesTierSupport { fast ultrafast allowDowngrade }
        modelContexts { modelId defaultContextWindow maxContextWindow }
        upstreamAddresses { id name url priority enabled }
        addressFailover
        extraModelPrefix
        modelMappings {
          from
          to
        }
        autoTrimedModelPrefixes
        hideOriginalModels
        hideMappedModels
        lowercaseModelId
        quotaRoutingMode
        proxy {
          type
          url
          username
          password
          disableConnectionReuse
        }
        transformOptions {
          codexPageContextCompatibility
          forceArrayInstructions
          forceArrayInputs
          replaceDeveloperRoleWithSystem
          reasoningEffortMapping { from to }
        }
        passThroughUserAgent
        passThroughBody
        forceOneHourPromptCache
        retryableStatusCodes
        apiKeyStrategy
        apiKeyRoundRobinSwitchAfter
        retryableErrorPatterns {
          pattern
          regex
        }
        primaryApiFormat
        maxSingleChannelRetries
        protocolPolicy { allowedProtocols sameProtocolOnly }
            modelProtocols {
          model
          apiFormats
          enabled
        }
        providerQuota {
          commandCode {
            authCookie
          }
          ollama {
            authCookie
          }
        }
      }
      orderingWeight
      remark
      defaultEndpoints {
        apiFormat
        path
        baseURL
        transport
        supportsWebSockets
        catalogURL
        catalogJSON
      }
      endpoints {
        apiFormat
        path
        baseURL
        transport
        supportsWebSockets
        catalogURL
        catalogJSON
      }
    }
  }
`;

export const BULK_CREATE_CHANNELS_MUTATION = `
  mutation BulkCreateChannels($input: BulkCreateChannelsInput!) {
    bulkCreateChannels(input: $input) {
      id
      type
      createdAt
      updatedAt
      baseURL
      websiteURL
      name
      status
      policies {
        stream
        apiKeyAutoDisableMode
        apiKeyAutoDisableRules {
          statusCodes
          keywordPatterns
          times
          action
          disableDurationMinutes
          disableUntilCron
          disableUntilTimezone
        }
      }
      supportedModels
      autoSyncSupportedModels
      autoSyncModelPattern
      manualModels
      tags
      defaultTestModel
      settings {
        globalPriority
        tagPriorities { tag priority }
        modelPriceMultiplier
        healthGateFailureThreshold
        streamFirstEventTimeoutSeconds
        nonStreamResponseTimeoutSeconds
        disableSessionReuse
        allowImageGenerationTool
        responsesServiceTierOverride
        responsesTierSupport { fast ultrafast allowDowngrade }
        modelContexts { modelId defaultContextWindow maxContextWindow }
        upstreamAddresses { id name url priority enabled }
        addressFailover
        extraModelPrefix
        modelMappings {
          from
          to
        }
        autoTrimedModelPrefixes
        hideOriginalModels
        hideMappedModels
        lowercaseModelId
        quotaRoutingMode
        proxy {
          type
          url
          username
          password
          disableConnectionReuse
        }
        transformOptions {
          codexPageContextCompatibility
          forceArrayInstructions
          forceArrayInputs
          replaceDeveloperRoleWithSystem
          reasoningEffortMapping { from to }
        }
        passThroughUserAgent
        passThroughBody
        forceOneHourPromptCache
        retryableStatusCodes
        apiKeyStrategy
        apiKeyRoundRobinSwitchAfter
        retryableErrorPatterns {
          pattern
          regex
        }
        primaryApiFormat
        maxSingleChannelRetries
        protocolPolicy { allowedProtocols sameProtocolOnly }
            modelProtocols {
          model
          apiFormats
          enabled
        }
        providerQuota {
          commandCode {
            authCookie
          }
          ollama {
            authCookie
          }
        }
      }
      orderingWeight
      remark
      defaultEndpoints {
        apiFormat
        path
        baseURL
        transport
        supportsWebSockets
        catalogURL
        catalogJSON
      }
      endpoints {
        apiFormat
        path
        baseURL
        transport
        supportsWebSockets
        catalogURL
        catalogJSON
      }
    }
  }
`;
