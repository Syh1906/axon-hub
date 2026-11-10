import { z } from 'zod';
import { pageInfoSchema } from '@/gql/pagination';

const threadSchema = z
  .object({
    id: z.string(),
    threadID: z.string().nullable().optional(),
  })
  .nullable()
  .optional();

export const traceRequestsSummarySchema = z
  .object({
    totalCount: z.number().nullable().optional(),
  })
  .nullable()
  .optional();

export const traceStatusSchema = z.enum(['active', 'archived', 'retained']);
export type TraceStatus = z.infer<typeof traceStatusSchema>;

export const traceSchema = z.object({
  id: z.string(),
  traceID: z.string(),
  createdAt: z.coerce.date(),
  updatedAt: z.coerce.date(),
  lastRequestAt: z.coerce.date(),
  thread: threadSchema,
  requests: traceRequestsSummarySchema,
  firstUserQuery: z.string().nullable().optional(),
  firstText: z.string().nullable().optional(),
  status: traceStatusSchema.optional().default('active'),
});

export type Trace = z.infer<typeof traceSchema>;

export const traceConnectionSchema = z.object({
  edges: z.array(
    z.object({
      node: traceSchema,
      cursor: z.string(),
    })
  ),
  pageInfo: pageInfoSchema,
  totalCount: z.number(),
});

export type TraceConnection = z.infer<typeof traceConnectionSchema>;

const spanSystemInstructionSchema = z
  .object({
    instruction: z.string().nullable().optional(),
  })
  .nullable()
  .optional();

const spanUserQuerySchema = z
  .object({
    text: z.string().nullable().optional(),
  })
  .nullable()
  .optional();

const spanUserImageURLSchema = z
  .object({
    url: z.string().nullable().optional(),
  })
  .nullable()
  .optional();

const spanUserVideoURLSchema = z
  .object({
    url: z.string().nullable().optional(),
  })
  .nullable()
  .optional();

const spanUserInputAudioSchema = z
  .object({
    format: z.string().nullable().optional(),
    data: z.string().nullable().optional(),
  })
  .nullable()
  .optional();

const spanTextSchema = z
  .object({
    text: z.string().nullable().optional(),
  })
  .nullable()
  .optional();

const spanImageURLSchema = z
  .object({
    url: z.string().nullable().optional(),
  })
  .nullable()
  .optional();

const spanVideoURLSchema = z
  .object({
    url: z.string().nullable().optional(),
  })
  .nullable()
  .optional();

const spanAudioSchema = z
  .object({
    id: z.string().nullable().optional(),
    format: z.string().nullable().optional(),
    data: z.string().nullable().optional(),
    transcript: z.string().nullable().optional(),
  })
  .nullable()
  .optional();

const spanThinkingSchema = z
  .object({
    thinking: z.string().nullable().optional(),
  })
  .nullable()
  .optional();

const spanToolUseSchema = z
  .object({
    id: z.string().nullable().optional(),
    type: z.string().nullable().optional(),
    name: z.string(),
    arguments: z.string().nullable().optional(),
  })
  .nullable()
  .optional();

const spanToolResultSchema = z
  .object({
    toolCallID: z.string().nullable().optional(),
    isError: z.boolean().nullable().optional(),
    text: z.string().nullable().optional(),
  })
  .nullable()
  .optional();

const spanCompactionSchema = z
  .object({
    summary: z.string().nullable().optional(),
  })
  .nullable()
  .optional();

const spanValueSchema = z
  .object({
    systemInstruction: spanSystemInstructionSchema,
    userQuery: spanUserQuerySchema,
    userImageUrl: spanUserImageURLSchema,
    userVideoUrl: spanUserVideoURLSchema,
    userInputAudio: spanUserInputAudioSchema,
    text: spanTextSchema,
    thinking: spanThinkingSchema,
    imageUrl: spanImageURLSchema,
    videoUrl: spanVideoURLSchema,
    audio: spanAudioSchema,
    toolUse: spanToolUseSchema,
    toolResult: spanToolResultSchema,
    compaction: spanCompactionSchema,
  })
  .nullable()
  .optional();

export const spanSchema = z.object({
  id: z.string(),
  type: z.string(),
  startTime: z.coerce.date().nullable().optional(),
  endTime: z.coerce.date().nullable().optional(),
  value: spanValueSchema,
});

export type Span = z.infer<typeof spanSchema>;

const requestMetadataSchema = z
  .object({
    itemCount: z.number().nullable().optional(),
    inputTokens: z.number().nullable().optional(),
    outputTokens: z.number().nullable().optional(),
    totalTokens: z.number().nullable().optional(),
    cachedTokens: z.number().nullable().optional(),
  })
  .nullable()
  .optional();

export type RequestMetadata = z.infer<typeof requestMetadataSchema>;

const segmentNodeSchema = z.object({
  id: z.any(),
  parentId: z.any().nullable().optional(),
  model: z.string(),
  duration: z.number(),
  startTime: z.coerce.date(),
  endTime: z.coerce.date(),
  metadata: requestMetadataSchema,
  requestSpans: z.array(spanSchema).nullable().optional().default([]),
  responseSpans: z.array(spanSchema).nullable().optional().default([]),
  children: z.array(z.unknown()).nullable().optional().default([]),
});

type SegmentNode = z.infer<typeof segmentNodeSchema>;

type SegmentParseTask = {
  input: unknown;
  target: SegmentNode[];
  index: number;
  parent?: SegmentParseTask;
};

export const segmentSchema: z.ZodType<any> = z.unknown().transform((input, ctx) => {
  const roots: SegmentNode[] = [];
  const pending: SegmentParseTask[] = [{ input, target: roots, index: 0 }];

  // 长会话可能形成很深的单链，逐节点校验，避免递归耗尽调用栈。
  while (pending.length > 0) {
    const task = pending.pop()!;
    const result = segmentNodeSchema.safeParse(task.input);
    if (!result.success) {
      // 仅失败时回溯父节点，成功路径不复制每层的完整错误路径。
      const indexes: number[] = [];
      for (let current: SegmentParseTask | undefined = task; current?.parent; current = current.parent) {
        indexes.push(current.index);
      }
      const path = indexes.reverse().flatMap((index) => ['children', index]);
      for (const issue of result.error.issues) {
        ctx.addIssue({ ...issue, path: [...path, ...issue.path] });
      }
      return z.NEVER;
    }

    const node = result.data;
    task.target[task.index] = node;
    if (node.children) {
      const children = node.children;
      const parsedChildren: SegmentNode[] = [];
      node.children = parsedChildren;
      for (let index = children.length - 1; index >= 0; index--) {
        pending.push({ input: children[index], target: parsedChildren, index, parent: task });
      }
    }
  }

  return roots[0];
});

export type Segment = z.infer<typeof segmentSchema>;

export const usageMetadataSchema = z
  .object({
    totalInputTokens: z.number(),
    totalOutputTokens: z.number(),
    totalTokens: z.number(),
    totalCost: z.coerce.number(),
    totalCachedTokens: z.number().nullable().optional(),
    totalCachedWriteTokens: z.number().nullable().optional(),
  })
  .nullable()
  .optional();

export type UsageMetadata = z.infer<typeof usageMetadataSchema>;

export const traceDetailSchema = z.object({
  id: z.string(),
  traceID: z.string(),
  createdAt: z.coerce.date(),
  updatedAt: z.coerce.date(),
  thread: threadSchema,
  requests: traceRequestsSummarySchema,
  rawRootSegment: z.any().nullable().optional(),
  usageMetadata: usageMetadataSchema,
  status: traceStatusSchema.optional().default('active'),
});

export type TraceDetail = z.infer<typeof traceDetailSchema>;

// Helper function to parse rawRootSegment JSON string into Segment object
export function parseRawRootSegment(rawRootSegment: any | null | undefined): Segment | null {
  if (!rawRootSegment) {
    return null;
  }
  if (typeof rawRootSegment === 'string') {
    try {
      const parsed = JSON.parse(rawRootSegment);
      return segmentSchema.parse(parsed);
    } catch (error) {
      return null;
    }
  }

  if (typeof rawRootSegment === 'object') {
    return segmentSchema.parse(rawRootSegment);
  }

  throw new Error('Invalid rawRootSegment type');
}
