import { priceFromCatalog } from './pricing';
import { deriveReasoningEfforts } from './reasoning-efforts';
import { resolveVision, type ProviderModel } from './providers.schema';
import type { ModelCard } from './schema';

export function modelCardFormValues(card?: ModelCard | null): ModelCard {
  return {
    ...card,
    reasoning: {
      supported: card?.reasoning?.supported ?? false,
      supportDeclared: card?.reasoning?.supportDeclared ?? false,
      default: card?.reasoning?.default ?? false,
    },
    modalities: {
      input: card?.modalities?.input ?? [],
      output: card?.modalities?.output ?? [],
    },
    limit: {
      context: card?.limit?.context ?? 0,
      input: card?.limit?.input ?? null,
      output: card?.limit?.output ?? 0,
    },
    price: {
      ...card?.price,
      items: card?.price?.items ?? [],
      schedule: card?.price?.schedule ?? null,
      // Field-array reset requires an explicit empty array to remove the previous model's tiers.
      volumeTiers: card?.price?.volumeTiers ?? [],
    },
  };
}

export function modelCardFromCatalog(model: ProviderModel, developer: string): ModelCard {
  const inputLimit = model.limit?.input;
  return {
    anthropicCapabilities: model.anthropic_capabilities,
    reasoning: {
      supported: model.reasoning?.supported ?? false,
      supportDeclared: model.reasoning?.supported != null,
      default: model.reasoning?.default ?? false,
    },
    reasoningEfforts: deriveReasoningEfforts(model.reasoning_options),
    toolCall: model.tool_call ?? false,
    temperature: model.temperature ?? false,
    vision: resolveVision(model),
    modalities: { input: model.modalities?.input ?? [], output: model.modalities?.output ?? [] },
    price: priceFromCatalog(model, developer),
    limit: {
      context: model.limit?.context ?? 0,
      input: inputLimit != null && inputLimit > 0 ? inputLimit : null,
      output: model.limit?.output ?? 0,
    },
    knowledge: model.knowledge,
    releaseDate: model.release_date,
    lastUpdated: model.last_updated,
  };
}
