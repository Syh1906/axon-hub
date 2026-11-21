// 列表、详情导航与状态统计共用同一套日志筛选参数。
export function requestLogVariables(where: Record<string, any> = {}) {
  const { channelTagsIn, channelIDIn, apiKeyGroupRefsIn, timeRange, offset, ...entWhere } = where;
  return { where: entWhere, filter: { collapseContinuations: !entWhere.clientSessionID, channelTagsIn, channelIDsIn: channelIDIn, apiKeyGroupRefsIn, timeRange, offset } };
}

export function requestLogOrder(where?: Record<string, any>) {
  return { field: 'CREATED_AT' as const, direction: where?.clientSessionID ? ('ASC' as const) : ('DESC' as const) };
}

export function requestStatusCountVariables(where: Record<string, any> = {}, projectId?: string | null) {
  const { statusIn: _statusIn, offset: _offset, ...filters } = where;
  return requestLogVariables({ ...filters, ...(projectId && { projectID: projectId }) });
}
