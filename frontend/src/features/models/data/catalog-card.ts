import { priceFromCatalog } from './pricing';
import { resolveVision, type ProviderModel } from './providers.schema';
import type { ModelCard } from './schema';

export function modelCardFormValues(card?: ModelCard | null): ModelCard {
  return {
    ...card,
    reasoning: {
      supported: card?.reasoning?.supported ?? false,
      default: card?.reasoning?.default ?? false,
    },
    modalities: {
      input: card?.modalities?.input ?? [],
      output: card?.modalities?.output ?? [],
    },
    limit: {
      context: card?.limit?.context ?? 0,
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
  return modelCardFormValues({
    reasoning: { supported: model.reasoning?.supported ?? false, default: model.reasoning?.default ?? false },
    toolCall: model.tool_call ?? false,
    temperature: model.temperature ?? false,
    vision: resolveVision(model),
    modalities: { input: model.modalities?.input ?? [], output: model.modalities?.output ?? [] },
    price: priceFromCatalog(model, developer),
    limit: { context: model.limit?.context ?? 0, output: model.limit?.output ?? 0 },
    knowledge: model.knowledge,
    releaseDate: model.release_date,
    lastUpdated: model.last_updated,
  });
}
