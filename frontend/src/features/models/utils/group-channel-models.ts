type PreviewModel = { requestModel: string; actualModel?: string };

export function previewModelKey(model: PreviewModel) {
  return JSON.stringify([model.requestModel, model.actualModel ?? model.requestModel]);
}

// A pass-through rule and a mapped alias can have the same requested ID but
// different outbound models, even on the same channel.
export function groupChannelModels<C extends { id: string | number }, M extends PreviewModel>(
  connections: { channel: C; models: M[] }[]
) {
  const grouped = new Map<C['id'], { channel: C; models: Map<string, M> }>();
  for (const connection of connections) {
    let group = grouped.get(connection.channel.id);
    if (!group) {
      group = { channel: connection.channel, models: new Map() };
      grouped.set(connection.channel.id, group);
    }
    for (const model of connection.models) {
      const key = previewModelKey(model);
      if (!group.models.has(key)) group.models.set(key, model);
    }
  }
  return [...grouped.values()].map(({ channel, models }) => ({ channel, models: [...models.values()] }));
}
