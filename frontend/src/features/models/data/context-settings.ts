import { z } from 'zod';

const contextWindow = z.number().int().positive().max(2147483647).nullable().optional();
export function createModelContextSettingsSchema(invalidRange = 'modelContext.invalidRange') {
  return z
    .object({
      defaultContextWindow: contextWindow,
      maxContextWindow: contextWindow,
    })
    .superRefine((value, ctx) => {
      if (value.defaultContextWindow != null && value.maxContextWindow != null && value.defaultContextWindow > value.maxContextWindow) {
        ctx.addIssue({ code: 'custom', message: invalidRange, path: ['defaultContextWindow'] });
      }
    });
}
export const modelContextSettingsSchema = createModelContextSettingsSchema();
export type ModelContextSettings = z.infer<typeof modelContextSettingsSchema>;
export const channelModelContextSchema = modelContextSettingsSchema.safeExtend({ modelId: z.string().min(1) });
export type ChannelModelContext = z.infer<typeof channelModelContextSchema>;
