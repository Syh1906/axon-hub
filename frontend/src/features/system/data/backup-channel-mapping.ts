import { z } from 'zod';

const referenceSchema = z.object({
  id: z.number().int().nonnegative(),
  name: z.string(),
  type: z.string().default(''),
  tags: z.array(z.string()).nullable().optional().transform((tags) => tags ?? []),
  has_config: z.boolean().default(false),
});

export type BackupChannelReference = z.infer<typeof referenceSchema>;
export type ChannelRestoreMapping = { sourceID: number; sourceName?: string; targetID: number };

export const backupChannelSourceKey = (source: BackupChannelReference) => source.id > 0 ? String(source.id) : `name:${source.name}`;
export const channelMappingSourceKey = (mapping: ChannelRestoreMapping) => mapping.sourceName === undefined ? String(mapping.sourceID) : `name:${mapping.sourceName}`;

// New backups place credential-free channel metadata on their first line.
// Do not read usage history just to populate a channel selector.
export async function readBackupChannelReferences(file: Blob): Promise<BackupChannelReference[]> {
  const reader = file.stream().getReader();
  const decoder = new TextDecoder();
  let firstLine = '';
  const manifestPrefix = '{"channel_references":';
  try {
    for (;;) {
      const { done, value } = await reader.read();
      firstLine += decoder.decode(value, { stream: !done });
      if (!manifestPrefix.startsWith(firstLine) && !firstLine.startsWith(manifestPrefix)) break;
      const end = firstLine.indexOf('\n');
      if (end >= 0) {
        const header = firstLine.slice(0, end);
        if (header.startsWith(manifestPrefix)) {
          const data = JSON.parse(`${header}}`);
          return z.array(referenceSchema).parse(data.channel_references ?? []);
        }
        break;
      }
      if (done) break;
    }
  } finally {
    await reader.cancel();
    reader.releaseLock();
  }
  // Published older backups have no manifest. Read them only when the user
  // explicitly opens channel mapping, retaining their existing JSON format.
  const data = JSON.parse(await file.text());
  const configs = z.array(referenceSchema).parse(data.channels ?? []);
  const configIDs = new Set(configs.map((channel) => channel.id));
  const refs = z.array(referenceSchema).parse(data.channel_references ?? data.channels ?? []).map((ref) => ({ ...ref, has_config: configIDs.has(ref.id) }));
  const sources = new Map(refs.map((ref) => [backupChannelSourceKey(ref), ref]));
  const knownNames = new Set(refs.map((ref) => ref.name));
  const prices = z.array(z.object({
    channel_id: z.number().int().nonnegative().default(0),
    channel_name: z.string(),
  })).parse(data.channel_model_prices ?? []);
  for (const price of prices) {
    // A full older backup already identifies this source through its channel.
    if (price.channel_id === 0 && knownNames.has(price.channel_name)) continue;
    const ref = { id: price.channel_id, name: price.channel_name, type: '', tags: [], has_config: false };
    const key = backupChannelSourceKey(ref);
    if (!sources.has(key)) sources.set(key, ref);
  }
  return [...sources.values()];
}
