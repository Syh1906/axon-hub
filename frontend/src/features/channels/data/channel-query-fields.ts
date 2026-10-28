export const CHANNEL_QUERY_FULL_NODE_SELECTION = `
          id
          createdAt
          updatedAt
          type
          baseURL
          name
          status
          policies {
            stream
            apiKeyAutoDisableRules { statusCodes keywordPatterns times action disableDurationMinutes disableUntilCron disableUntilTimezone }
          }
          credentials {
            apiKey
            apiKeys
            gcp {
              region
              projectID
              jsonData
            }
          }
          supportedModels
          autoSyncSupportedModels
          autoSyncModelPattern
          manualModels
          tags
          defaultTestModel
          settings {
            tagPriorities { tag priority }
            modelPriceMultiplier
            healthGateFailureThreshold
            streamFirstEventTimeoutSeconds
            nonStreamResponseTimeoutSeconds
            disableSessionReuse
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
            bodyOverrideOperations {
              op
              path
              from
              to
              value
              condition
              match {
                path
                eq
              }
              index
              splat
            }
            headerOverrideOperations {
              op
              path
              from
              to
              value
              condition
              match {
                path
                eq
              }
              index
              splat
            }
            proxy {
              type
              url
              username
              password
              disableConnectionReuse
            }
            transformOptions {
              forceArrayInstructions
              forceArrayInputs
              replaceDeveloperRoleWithSystem
              reasoningEffortMapping { from to }
            }
            passThroughUserAgent
            passThroughBody
            forceOneHourPromptCache
            rateLimit {
              rpm
              tpm
              maxConcurrent
              queueSize
              queueTimeoutMs
            }
            retryableStatusCodes
            retryableErrorPatterns {
              pattern
              regex
            }
            primaryApiFormat
            modelProtocols {
              model
              apiFormats
              enabled
            }
            providerQuota {
              commandCode {
                authCookie
              }
            }
          }
          orderingWeight
          errorMessage
          remark
          defaultEndpoints {
            apiFormat
            path
            baseURL
            transport
          }
          endpoints {
            apiFormat
            path
            baseURL
            transport
          }
          disabledAPIKeys {
            key
            disabledAt
            errorCode
            reason
            expiresAt
          }
          healthGate { disabled openCount unstableCount }
          liveLimiterStats {
            inFlight
            waiting
            capacity
            queueSize
          }
          providerQuotaStatus {
            status
            nextResetAt
            ready
            quotaData
            providerType
          }
`;

export const CHANNEL_QUERY_LIST_NODE_BASE_SELECTION = `
          id
          createdAt
          updatedAt
          type
          baseURL
          name
          status
          defaultTestModel
          errorMessage
          tags
          settings {
           tagPriorities { tag priority }
            modelPriceMultiplier
            healthGateFailureThreshold
            streamFirstEventTimeoutSeconds
            nonStreamResponseTimeoutSeconds
            disableSessionReuse
            upstreamAddresses { id name url priority enabled }
            addressFailover
            forceOneHourPromptCache
            primaryApiFormat
          }
          disabledAPIKeys {
            key
            disabledAt
            errorCode
            reason
            expiresAt
          }
          healthGate { disabled openCount unstableCount }
`;

export const CHANNEL_QUERY_SUPPORTED_MODELS_SELECTION = `
          supportedModels
`;

export const CHANNEL_QUERY_TAGS_SELECTION = `
          tags
`;

export const CHANNEL_QUERY_PROXY_SELECTION = `
          settings {
            tagPriorities { tag priority }
            modelPriceMultiplier
            healthGateFailureThreshold
            streamFirstEventTimeoutSeconds
            nonStreamResponseTimeoutSeconds
            disableSessionReuse
            upstreamAddresses { id name url priority enabled }
            addressFailover
            proxy {
              type
              url
              username
              password
              disableConnectionReuse
            }
          }
`;

export const CHANNEL_QUERY_ORDERING_WEIGHT_SELECTION = `
          orderingWeight
`;

export const CHANNEL_QUERY_HEALTH_SELECTION = `
          liveLimiterStats {
            inFlight
            waiting
            capacity
            queueSize
          }
`;

export const CHANNEL_QUERY_QUOTA_SELECTION = `
          providerQuotaStatus {
            status
            nextResetAt
            ready
            quotaData
            providerType
          }
`;
