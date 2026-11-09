import { z } from 'zod';
import { pageInfoSchema } from '@/gql/pagination';
import { apiKeySchema } from '@/features/apikeys/data/schema';
import { channelSchema } from '@/features/channels/data';
import { requestUsageSummarySchema, usageLogSchema } from './usage-logs-schema';

// Request Status
export const requestStatusSchema = z.enum(['pending', 'processing', 'completed', 'failed', 'canceled']);
export type RequestStatus = z.infer<typeof requestStatusSchema>;

// Request Source
export const requestSourceSchema = z.enum(['api', 'playground', 'test']);
export type RequestSource = z.infer<typeof requestSourceSchema>;

// Request Execution Status
export const requestExecutionStatusSchema = z.enum(['pending', 'processing', 'completed', 'failed', 'canceled']);
export type RequestExecutionStatus = z.infer<typeof requestExecutionStatusSchema>;

export function isRequestChannelSessionState(state: string | undefined): state is 'new' | 'reused' | 'returned' {
  return state === 'new' || state === 'reused' || state === 'returned';
}

export const executionSelectionSchema = z.object({
  channelName: z.string(),
  provider: z.string(),
  keyHint: z.string(),
  keyRemark: z.string(),
  sessionState: z.string(),
  clientTransport: z.string().optional(),
  upstreamTransport: z.string().optional(),
  transportFallbackReason: z.string().optional(),
  strategy: z.string(),
  priority: z.number(),
  candidates: z
    .array(
      z.object({
        channelID: z.number(),
        channelName: z.string(),
        provider: z.string(),
        priority: z.number(),
        models: z.array(z.string()),
        sessionReuse: z.boolean(),
      })
    )
    .optional(),
});
export const executionFailureSchema = z.object({
  source: z.string(),
  code: z.string(),
  type: z.string(),
  body: z.string().optional(),
  bodyTruncated: z.boolean().optional(),
});

// Request Execution
export const requestExecutionSchema = z.object({
  id: z.string(),
  createdAt: z.coerce.date(),
  updatedAt: z.coerce.date(),
  // userID: z.number(),
  // requestID: z.string(),
  // channelID: z.number(),
  channel: channelSchema.partial().nullable().optional(),
  // 1-based position of the API key used within the channel's credential list.
  // Null for single-key/OAuth channels and for executions recorded before the
  // field existed.
  channelAPIKeyIndex: z.number().nullable().optional(),
  modelID: z.string(),
  upstreamModelID: z.string().nullable().optional(),
  requestHeaders: z.any().nullable().optional(),
  responseHeaders: z.any().nullable().optional(),
  // 诊断与会话补查按需省略正文；Zod 4.6 的 any 不隐含字段可缺省。
  requestBody: z.any().optional(), // JSONRawMessage
  responseBody: z.any().nullable().optional(), // JSONRawMessage
  responseChunks: z.array(z.any()).nullable().optional(), // [JSONRawMessage!]
  errorMessage: z.string().nullable(),
  responseStatusCode: z.number().nullable().optional(),
  selection: executionSelectionSchema.nullable().optional(),
  costSnapshot: z
    .object({ applied: z.object({ value: z.number() }) })
    .nullable()
    .optional(),
  failure: executionFailureSchema.nullable().optional(),
  retryDecision: z.string().nullable().optional(),
  status: requestExecutionStatusSchema,
  format: z.string().optional(),
  reasoningEffort: z.string().nullable().optional(),
  channelAPIKeySuffix: z.string().nullable().optional(),
  metricsLatencyMs: z.number().nullable().optional(),
  metricsFirstTokenLatencyMs: z.number().nullable().optional(),
  metricsReasoningDurationMs: z.number().nullable().optional(),
  requestURL: z.string().nullable().optional(),
  passThroughApplied: z.boolean().optional(),
});
export type RequestExecution = z.infer<typeof requestExecutionSchema>;

export const routingDecisionComboSchema = z.object({
  channelID: z.number(),
  channelName: z.string(),
  actualModel: z.string(),
  state: z.string().nullable().optional(),
});

export const routingDecisionMigrationSchema = z.object({
  fromChannelID: z.number(),
  fromChannelName: z.string(),
  toChannelID: z.number(),
  toChannelName: z.string(),
  reason: z.string(),
});

export const requestRoutingDecisionSchema = z.object({
  owner: routingDecisionComboSchema.nullable(),
  skipped: z.array(routingDecisionComboSchema),
  temporaryFailover: z.boolean(),
  lastResort: z.boolean(),
  consecutiveFailovers: z.number(),
  migration: routingDecisionMigrationSchema.nullable(),
});

// Request
export const requestSchema = z.object({
  continuationCount: z.number().int().nonnegative().optional(),
  outputTokensPerSecond: z.number().nullable().optional(),
  usageSummary: requestUsageSummarySchema.nullable().optional(),
  id: z.string(),
  createdAt: z.coerce.date(),
  updatedAt: z.coerce.date(),
  // apiKeyID: z.string().optional().nullable(),
  apiKey: apiKeySchema.partial().nullable().optional(),
  // channelID: z.string().optional().nullable(),
  channel: channelSchema.partial().nullable().optional(),
  source: requestSourceSchema,
  modelID: z.string(),
  reasoningEffort: z.string().nullable().optional(),
  contentSaved: z.boolean().optional(),
  contentStorageKey: z.string().nullable().optional(),
  requestHeaders: z.any().nullable().optional(),
  responseHeaders: z.any().nullable().optional(),
  requestBody: z.any().nullable().optional(), // JSONRawMessage
  responseBody: z.any().nullable().optional(), // JSONRawMessage
  responseChunks: z.array(z.any()).nullable().optional(), // [JSONRawMessage!]
  routingDecision: requestRoutingDecisionSchema.nullable().optional(),
  status: requestStatusSchema,
  clientStatusCode: z.number().nullable().optional(),
  failure: z.object({ stage: z.string(), code: z.string(), type: z.string(), message: z.string() }).nullable().optional(),
  format: z.string().optional(),
  clientIP: z.string().nullable().optional(),
  userAgent: z.string().nullable().optional(),
  stream: z.boolean().nullable(),
  metricsLatencyMs: z.number().nullable().optional(),
  metricsFirstTokenLatencyMs: z.number().nullable().optional(),
  metricsReasoningDurationMs: z.number().nullable().optional(),
  sessionExecution: requestExecutionSchema.pick({ id: true, channel: true, selection: true, costSnapshot: true }).nullable().optional(),
  resultExecution: requestExecutionSchema.pick({ id: true, responseStatusCode: true }).nullable().optional(),
  executions: z
    .object({
      edges: z.array(
        z.object({
          node: requestExecutionSchema.partial().nullable().optional(),
          cursor: z.string(),
        })
      ),
      // The request list only needs execution summary rows; nested pagination metadata is not requested.
      pageInfo: pageInfoSchema.optional(),
      totalCount: z.number().optional(),
    })
    .optional(),
  oneHourPromptCacheUsageLogs: z.object({ totalCount: z.number() }).optional(),
  usageLogs: z
    .object({
      edges: z
        .array(
          z.object({
            node: usageLogSchema.partial().nullable().optional(),
            cursor: z.string().optional(),
          })
        )
        .optional(),
      pageInfo: pageInfoSchema.optional(),
    })
    .optional()
    .nullable(),
});

export type Request = z.infer<typeof requestSchema>;

// Request Connection (for pagination)
export const requestConnectionSchema = z.object({
  edges: z.array(
    z.object({
      node: requestSchema,
      cursor: z.string(),
    })
  ),
  pageInfo: pageInfoSchema,
  totalCount: z.number().optional(),
});
export type RequestConnection = z.infer<typeof requestConnectionSchema>;

// Request Execution Connection (for pagination)
export const requestExecutionConnectionSchema = z.object({
  edges: z.array(
    z.object({
      node: requestExecutionSchema,
      cursor: z.string(),
    })
  ),
  pageInfo: pageInfoSchema,
  totalCount: z.number(),
});
export type RequestExecutionConnection = z.infer<typeof requestExecutionConnectionSchema>;
