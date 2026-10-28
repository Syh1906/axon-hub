// A channel can occur once per association in the preview response.
export function groupChannelModels<C extends { id: string | number }, M extends { requestModel: string }>(
  connections: { channel: C; models: M[] }[]
) {
  const grouped = new Map<C['id'], { channel: C; models: M[] }>();
  for (const connection of connections) {
    const existing = grouped.get(connection.channel.id);
    if (!existing)
      grouped.set(connection.channel.id, {
        channel: connection.channel,
        models: [...new Map(connection.models.map((model) => [model.requestModel, model])).values()],
      });
    else {
      const names = new Set(existing.models.map((model) => model.requestModel));
      for (const model of connection.models)
        if (!names.has(model.requestModel)) {
          existing.models.push(model);
          names.add(model.requestModel);
        }
    }
  }
  return [...grouped.values()];
}
