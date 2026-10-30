import { z } from 'zod';
import { pageInfoSchema } from '@/gql/pagination';
import { apiKeySchema } from '@/features/apikeys/data/schema';
import { channelSchema } from '@/features/channels/data';
import { usageLogSchema } from './usage-logs-schema';

// Request Status
export const requestStatusSchema = z.enum(['pending', 'processing', 'completed', 'failed', 'canceled']);
export type RequestStatus = z.infer<typeof requestStatusSchema>;

// Request Source
export const requestSourceSchema = z.enum(['api', 'playground', 'test']);
export type RequestSource = z.infer<typeof requestSourceSchema>;

// Request Execution Status
export const requestExecutionStatusSchema = z.enum(['pending', 'processing', 'completed', 'failed', 'canceled']);
export type RequestExecutionStatus = z.infer<typeof requestExecutionStatusSchema>;

export const executionSelectionSchema = z.object({
  channelName: z.string(),
  provider: z.string(),
  keyHint: z.string(),
  keyRemark: z.string(),
  sessionState: z.string(),
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
  modelID: z.string(),
  requestHeaders: z.any().nullable().optional(),
  requestBody: z.any(), // JSONRawMessage
  responseBody: z.any().nullable(), // JSONRawMessage
  responseChunks: z.array(z.any()).nullable(), // [JSONRawMessage!]
  errorMessage: z.string().nullable(),
  responseStatusCode: z.number().nullable().optional(),
  selection: executionSelectionSchema.nullable().optional(),
  costSnapshot: z.object({ applied: z.object({ value: z.number() }) }).nullable().optional(),
  failure: executionFailureSchema.nullable().optional(),
  retryDecision: z.string().nullable().optional(),
  status: requestExecutionStatusSchema,
  format: z.string().optional(),
  reasoningEffort: z.string().nullable().optional(),
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
  requestBody: z.any().nullable().optional(), // JSONRawMessage
  responseBody: z.any().nullable().optional(), // JSONRawMessage
  responseChunks: z.array(z.any()).nullable().optional(), // [JSONRawMessage!]
  routingDecision: requestRoutingDecisionSchema.nullable().optional(),
  status: requestStatusSchema,
  clientStatusCode: z.number().nullable().optional(),
  format: z.string().optional(),
  clientIP: z.string().nullable().optional(),
  stream: z.boolean().nullable(),
  metricsLatencyMs: z.number().nullable().optional(),
  metricsFirstTokenLatencyMs: z.number().nullable().optional(),
  metricsReasoningDurationMs: z.number().nullable().optional(),
  executions: z
    .object({
      edges: z.array(
        z.object({
          node: requestExecutionSchema.partial().nullable().optional(),
          cursor: z.string(),
        })
      ),
      pageInfo: pageInfoSchema,
      totalCount: z.number(),
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
  totalCount: z.number(),
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
