import type { ChannelEndpoint, ChannelSettings, DetectedChannelEndpoint } from './schema';

export const IMAGE_TOOL = 'responses/image_generation';
export const WEB_SEARCH = 'responses/web_search';

export type ProbeSettings = Pick<
  ChannelSettings,
  'adaptedCapabilities' | 'disabledEndpoints' | 'allowImageGenerationTool' | 'allowWebSearchTool'
>;
export interface ProbeEdits {
  settings: ProbeSettings;
  additions: string[];
}

export function adaptResults(edits: ProbeEdits, results: DetectedChannelEndpoint[], configured: readonly string[]): ProbeEdits {
  const settings = { ...edits.settings };
  const adapted = new Set(settings.adaptedCapabilities);
  for (const result of results) {
    if (!result.supported || adapted.has(result.apiFormat)) continue;
    adapted.add(result.apiFormat);
    if (![IMAGE_TOOL, WEB_SEARCH].includes(result.apiFormat) && !configured.includes(result.apiFormat)) {
      settings.disabledEndpoints = [...new Set([...(settings.disabledEndpoints ?? []), result.apiFormat])];
    }
    if (result.apiFormat === IMAGE_TOOL) settings.allowImageGenerationTool ??= false;
    if (result.apiFormat === WEB_SEARCH) settings.allowWebSearchTool ??= false;
  }
  return { ...edits, settings: { ...settings, adaptedCapabilities: [...adapted] } };
}

export function capabilityEnabled(format: string, edits: ProbeEdits, configured: readonly string[]) {
  if (format === IMAGE_TOOL) return edits.settings.allowImageGenerationTool === true;
  if (format === WEB_SEARCH) return edits.settings.allowWebSearchTool === true;
  return !edits.settings.disabledEndpoints?.includes(format) && (configured.includes(format) || edits.additions.includes(format));
}

export function toggleCapability(format: string, edits: ProbeEdits, configured: readonly string[]): ProbeEdits {
  const enabled = !capabilityEnabled(format, edits, configured);
  if (format === IMAGE_TOOL || format === WEB_SEARCH) {
    const key = format === IMAGE_TOOL ? 'allowImageGenerationTool' : 'allowWebSearchTool';
    return { ...edits, settings: { ...edits.settings, [key]: enabled } };
  }
  const disabled = new Set(edits.settings.disabledEndpoints);
  const additions = new Set(edits.additions);
  if (enabled) {
    disabled.delete(format);
    if (!configured.includes(format)) additions.add(format);
  } else {
    additions.delete(format);
    disabled.add(format);
  }
  return { additions: [...additions], settings: { ...edits.settings, disabledEndpoints: [...disabled] } };
}

export function appendProbeEndpoints(endpoints: ChannelEndpoint[], additions: readonly string[]) {
  return [
    ...endpoints,
    ...additions
      // These formats are probed only when the channel already provides the
      // endpoint. Their enabled state needs no additional custom transformer.
      .filter(format => !['openai/video','openai/completions','openai/responses_compact','modelscope/image_generation','seedance/video','ollama/chat'].includes(format))
      .filter((format) => !endpoints.some((ep) => ep.apiFormat === format))
      .map((apiFormat) => {
        if (apiFormat === 'openai/responses-ws') {
          const http = endpoints.find((ep) => ep.apiFormat === 'openai/responses');
          return { apiFormat, path: http?.path, baseURL: http?.baseURL };
        }
        return { apiFormat };
      }),
  ];
}
