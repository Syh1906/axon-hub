import { useQuery, useQueryClient } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { useTranslation } from 'react-i18next';
import { useSelectedProjectId } from '@/stores/projectStore';
import { useCursorList } from '@/hooks/use-cursor-list';
import { useErrorHandler } from '@/hooks/use-error-handler';
import { usePermissions } from '@/hooks/usePermissions';
import { COST_PRICING_EVIDENCE_FIELDS } from '@/components/cost-pricing-evidence';
import { useRequestPermissions } from '../../../hooks/useRequestPermissions';
import { shareRequestList } from './request-list-cache';
import { requestLogVariables } from './request-log-variables';
import { Request, RequestConnection, RequestExecutionConnection, requestConnectionSchema, requestExecutionConnectionSchema, requestSchema } from './schema';

// Requests expose their reporting timezone without granting system settings access.
export function useRequestLogTimezone() {
  const projectId = useSelectedProjectId();
  const { hasScope } = usePermissions();
  return useQuery({
    queryKey: ['request-log-timezone', projectId],
    enabled: hasScope('read_requests'),
    queryFn: async ({ signal }) => {
      const data = await graphqlRequest<{ requestLogTimezone: string }>(
        `query { requestLogTimezone }`,
        {},
        projectId ? { 'X-Project-ID': projectId } : undefined, { signal }
      );
      return data.requestLogTimezone;
    },
  });
}

// Dynamic GraphQL query builder
function buildRequestsQuery(
  permissions: { canViewApiKeys: boolean; canViewChannels: boolean; canViewCallerUser: boolean },
  mode: 'list' | 'continuations' = 'list'
) {
  const apiKeyFields = permissions.canViewApiKeys
    ? `
          apiKey {
            groupName
            groupRef
            id
            name${
              permissions.canViewCallerUser
                ? `
            user {
              firstName
              lastName
            }`
                : ''
            }
          }`
    : '';

  const requestChannelFields = permissions.canViewChannels
    ? `
                channel {
                  id
                  name
                  type
                  tags
                }`
    : '';

  const executionChannelFields = permissions.canViewChannels
    ? `
                  channel {
                    id
                    name
                  type
                  tags
                  }
                  channelAPIKeyIndex`
    : '';

  return `
    ${mode === 'continuations' ? `query GetRequestContinuations($requestID: ID!, $first: Int, $after: Cursor) {
      requests: requestContinuations(requestID: $requestID, first: $first, after: $after) {` : `query GetRequests(
      $first: Int
      $after: Cursor
      $last: Int
      $before: Cursor
      $orderBy: RequestOrder
      $where: RequestWhereInput
      $filter: RequestLogFilterInput
    ) {
      requests: requestLogs(first: $first, after: $after, last: $last, before: $before, orderBy: $orderBy, where: $where, filter: $filter) {`}
        edges {
          node {
            id
            ${mode === 'list' ? 'continuationCount' : ''}
            createdAt
            updatedAt${apiKeyFields}${requestChannelFields}
            source
            modelID
            format
            reasoningEffort
            stream
            status
            clientIP
            clientStatusCode
            failure { stage code type message }
            outputTokensPerSecond
            userAgent
            metricsLatencyMs
            metricsFirstTokenLatencyMs
            metricsReasoningDurationMs
            ${permissions.canViewChannels ? `sessionExecution {
              id
              channel { id name type tags }
              selection { channelName provider keyHint keyRemark sessionState clientTransport upstreamTransport transportFallbackReason strategy priority }
              costSnapshot { applied { value } }
            }` : ''}
            executions(first: 10, orderBy: { field: CREATED_AT, direction: DESC }) {
              totalCount
              edges {
                node {
                  id
                  createdAt
                  modelID
                  upstreamModelID
                  format
                  status
                  reasoningEffort
                  responseStatusCode
                  errorMessage
                  retryDecision
                  costSnapshot { applied { value } }
                  failure { source code type }
                  ${permissions.canViewChannels ? 'selection { channelName provider keyHint keyRemark sessionState clientTransport upstreamTransport transportFallbackReason strategy priority }' : ''}
                  passThroughApplied${executionChannelFields}
                }
                cursor
              }
            }
            oneHourPromptCacheUsageLogs: usageLogs(first: 1, where: { promptWriteCachedTokens1hGT: 0 }) {
              totalCount
            }
            usageLogs(first: 1) {
              edges {
                node {
                  id
                  promptTokens
                  completionTokens
                  completionReasoningTokens
                  totalTokens
                  promptCachedTokens
                  promptWriteCachedTokens
                  promptWriteCachedTokens5m
                  promptWriteCachedTokens1h
                  totalCost
                  costPriceMultiplier
                  costItems { ${COST_PRICING_EVIDENCE_FIELDS} image { width height tier source tierSource requestedWidth requestedHeight inspectionError } imageRule unpriced unpricedReason itemCode promptWriteCacheVariantCode quantity subtotal tierBreakdown { upTo units subtotal } }
                  costPriceReferenceID
                }
              }
            }
          }
          cursor
        }
        pageInfo {
          hasNextPage
          hasPreviousPage
          startCursor
          endCursor
        }
      }
    }
  `;
}

const REQUEST_ROUTING_DECISION_SELECTION = `
          routingDecision {
            owner { channelID channelName actualModel state }
            skipped { channelID channelName actualModel state }
            temporaryFailover
            lastResort
            consecutiveFailovers
            migration { fromChannelID fromChannelName toChannelID toChannelName reason }
          }
`;

function buildRequestDetailQuery(
  permissions: { canViewApiKeys: boolean; canViewChannels: boolean; canViewCallerUser: boolean },
  payload: 'all' | 'request' | 'response' = 'all'
) {
  const apiKeyFields = permissions.canViewApiKeys
    ? `
          apiKey {
            groupName
            groupRef
            id
            name${
              permissions.canViewCallerUser
                ? `
            user {
              firstName
              lastName
            }`
                : ''
            }
        }`
    : '';

  const requestChannelFields = permissions.canViewChannels
    ? `
          channel {
            id
            name
                  type
                  tags
          }`
    : '';

  return `
    query GetRequestDetail($id: ID!) {
      node(id: $id) {
        ... on Request {
          id
          createdAt
          updatedAt${apiKeyFields}${requestChannelFields}
          ${permissions.canViewChannels ? 'sessionExecution { id }' : ''}
          source
          modelID
          stream
          clientIP
          clientStatusCode
          failure { stage code type message }
          projectID
          dataStorageID
          contentSaved
          contentStorageKey
          ${payload !== 'response' ? 'requestHeaders requestBody' : ''}
          ${payload !== 'request' ? 'responseHeaders responseBody responseChunks' : ''}
          status
          format
          ${REQUEST_ROUTING_DECISION_SELECTION}
          metricsReasoningDurationMs
          outputTokensPerSecond
          usageLogs(first: 1) {
            edges {
              node {
                  id
                  promptTokens
                  completionTokens
                  completionReasoningTokens
                  totalTokens
                  promptCachedTokens
                  promptWriteCachedTokens
                  promptWriteCachedTokens5m
                  promptWriteCachedTokens1h
                  totalCost
                  costPriceMultiplier
                  costItems { ${COST_PRICING_EVIDENCE_FIELDS} image { width height tier source tierSource requestedWidth requestedHeight inspectionError } imageRule unpriced unpricedReason itemCode promptWriteCacheVariantCode quantity subtotal tierBreakdown { upTo units subtotal } }
                  costPriceReferenceID
                }
            }
          }
        }
      }
    }
  `;
}

function buildRequestDetailPollingQuery(permissions: { canViewApiKeys: boolean; canViewChannels: boolean; canViewCallerUser: boolean }) {
  const apiKeyFields = permissions.canViewApiKeys
    ? `
          apiKey {
            groupName
            groupRef
            id
            name${
              permissions.canViewCallerUser
                ? `
            user {
              firstName
              lastName
            }`
                : ''
            }
        }`
    : '';

  const requestChannelFields = permissions.canViewChannels
    ? `
          channel {
            id
            name
                  type
                  tags
          }`
    : '';

  return `
    query GetRequestDetailPolling($id: ID!) {
      node(id: $id) {
        ... on Request {
          id
          createdAt
          updatedAt${apiKeyFields}${requestChannelFields}
          ${permissions.canViewChannels ? 'sessionExecution { id }' : ''}
          source
          modelID
          stream
          clientIP
          clientStatusCode
          failure { stage code type message }
          projectID
          dataStorageID
          contentSaved
          contentStorageKey
          responseHeaders
          status
          format
          ${REQUEST_ROUTING_DECISION_SELECTION}
          metricsReasoningDurationMs
          outputTokensPerSecond
        }
      }
    }
  `;
}

function buildRequestExecutionsQuery(permissions: { canViewChannels: boolean }, includePayload: boolean) {
  const channelFields = permissions.canViewChannels
    ? `
              channel {
                  id
                  name
                  type
                  baseURL
                  websiteURL
                  tags
              }
              channelAPIKeySuffix
              channelAPIKeyIndex`
    : '';

  return `
    query GetRequestExecutions(
      $requestID: ID!
      $first: Int
      $after: Cursor
      $orderBy: RequestExecutionOrder
      $where: RequestExecutionWhereInput
    ) {
      node(id: $requestID) {
        ... on Request {
          executions(first: $first, after: $after, orderBy: $orderBy, where: $where) {
            edges {
              node {
                id
                createdAt
                updatedAt
                requestID${channelFields}
                modelID
                upstreamModelID
                projectID
                dataStorageID
                ${includePayload ? 'requestHeaders responseHeaders requestBody responseBody responseChunks' : ''}
                errorMessage
                responseStatusCode
                failure { source code type body bodyTruncated }
                retryDecision
                ${permissions.canViewChannels ? 'selection { channelName provider keyHint keyRemark sessionState clientTransport upstreamTransport transportFallbackReason strategy priority candidates { channelID channelName provider priority models sessionReuse } }' : ''}
                status
                format
                reasoningEffort
                stream
                requestURL
                passThroughApplied
                metricsLatencyMs
                metricsFirstTokenLatencyMs
                metricsReasoningDurationMs
              }
              cursor
            }
            pageInfo {
              hasNextPage
              hasPreviousPage
              startCursor
              endCursor
            }
            totalCount
          }
        }
      }
    }
  `;
}

// Query hooks
export function useRequestContinuations(requestID: string, count: number, after: string | null) {
  const permissions = useRequestPermissions();
  const projectId = useSelectedProjectId();
  return useQuery({
    queryKey: ['request-continuations', requestID, count, after, permissions, projectId],
    queryFn: async ({ signal }) => {
      const data = await graphqlRequest<{ requests: RequestConnection }>(
        buildRequestsQuery(permissions, 'continuations'),
        { requestID, first: 20, after },
        projectId ? { 'X-Project-ID': projectId } : undefined,
        { signal }
      );
      return requestConnectionSchema.parse(data.requests);
    },
    structuralSharing: shareRequestList,
    staleTime: 60_000,
    gcTime: 60_000,
    refetchOnWindowFocus: false,
    retry: false,
  });
}

export function useInfiniteRequests(where: Record<string, any> | undefined, pageSize: number, enabled = true) {
  const permissions = useRequestPermissions();
  const projectId = useSelectedProjectId();
  return useCursorList({
    enabled,
    structuralSharing: shareRequestList,
    gcTime: 60_000,
    maxPages: 8,
    queryKey: ['requests', 'infinite', where, pageSize, permissions, projectId],
    fetchPage: async (after, signal) => {
      const data = await graphqlRequest<{ requests: RequestConnection }>(
        buildRequestsQuery(permissions),
        {
          first: pageSize,
          after,
          orderBy: { field: 'CREATED_AT', direction: 'DESC' },
          ...requestLogVariables({ ...where, ...(projectId && { projectID: projectId }) }),
        },
        projectId ? { 'X-Project-ID': projectId } : undefined,
        { signal }
      );
      return requestConnectionSchema.parse(data.requests);
    },
    fetchPreviousPage: async (before, signal) => {
      const data = await graphqlRequest<{ requests: RequestConnection }>(
        buildRequestsQuery(permissions),
        {
          last: pageSize,
          before,
          orderBy: { field: 'CREATED_AT', direction: 'DESC' },
          ...requestLogVariables({ ...where, ...(projectId && { projectID: projectId }) }),
        },
        projectId ? { 'X-Project-ID': projectId } : undefined,
        { signal }
      );
      return requestConnectionSchema.parse(data.requests);
    },
  });
}

export function useRequests(
  variables?: {
    first?: number;
    after?: string;
    last?: number;
    before?: string;
    orderBy?: { field: 'CREATED_AT'; direction: 'ASC' | 'DESC' };
    where?: {
      status?: string;
      source?: string;
      channelID?: string;
      channelIDIn?: string[];
      statusIn?: string[];
      sourceIn?: string[];
      projectID?: string;
      [key: string]: any;
    };
  },
  options?: { projectId?: string | null; scopeToSelectedProject?: boolean; enabled?: boolean; keepPreviousPage?: boolean }
) {
  const { handleError } = useErrorHandler();
  const { t } = useTranslation();
  const permissions = useRequestPermissions();
  const selectedProjectId = useSelectedProjectId();
  const scopeToSelectedProject = options?.scopeToSelectedProject ?? true;
  const projectId = options?.projectId !== undefined ? options.projectId : selectedProjectId;
  const enabled = options?.enabled ?? true;

  return useQuery({
    queryKey: ['requests', variables, permissions, projectId, scopeToSelectedProject],
    queryFn: async ({ signal }) => {
      try {
        const query = buildRequestsQuery(permissions);
        const headers = projectId ? { 'X-Project-ID': projectId } : undefined;

        // Add project filter if project scoping is enabled
        const finalVariables = {
          ...variables,
          where: {
            ...variables?.where,
            ...(scopeToSelectedProject && projectId && { projectID: projectId }),
          },
        };

        const data = await graphqlRequest<{ requests: RequestConnection }>(
          query,
          { ...finalVariables, ...requestLogVariables(finalVariables.where) },
          headers,
          { signal }
        );
        return requestConnectionSchema.parse(data?.requests);
      } catch (error) {
        handleError(error, t('common.errors.internalServerError'));
        throw error;
      }
    },
    enabled,
    placeholderData: (previous, previousQuery) => {
      if (!options?.keepPreviousPage || !previousQuery) return undefined;
      const [, oldVariables, oldPermissions, oldProject, oldScope] = previousQuery.queryKey;
      const withoutOffset = (value: typeof variables) => ({ ...value, where: { ...value?.where, offset: undefined } });
      return oldProject === projectId &&
        oldScope === scopeToSelectedProject &&
        JSON.stringify(oldPermissions) === JSON.stringify(permissions) &&
        JSON.stringify(withoutOffset(oldVariables as typeof variables)) === JSON.stringify(withoutOffset(variables))
        ? previous
        : undefined;
    },
    structuralSharing: shareRequestList,
    gcTime: 60_000,
    refetchOnWindowFocus: false,
  });
}

export function useRequest(
  id: string,
  options?: {
    projectId?: string | null;
    enabled?: boolean;
    disableAutoRefresh?: boolean;
    includePayload?: boolean;
    payload?: 'all' | 'request' | 'response';
  }
) {
  const { handleError } = useErrorHandler();
  const { t } = useTranslation();
  const permissions = useRequestPermissions();
  const selectedProjectId = useSelectedProjectId();
  const queryClient = useQueryClient();
  const projectId = options?.projectId !== undefined ? options.projectId : selectedProjectId;
  const enabled = options?.enabled ?? true;

  const includePayload = options?.includePayload ?? true;
  const payload = options?.payload ?? 'all';
  const queryKey = ['request', id, permissions, projectId, includePayload, includePayload ? payload : null] as const;

  return useQuery({
    queryKey,
    gcTime: 0,
    queryFn: async ({ signal }) => {
      try {
        const headers = projectId ? { 'X-Project-ID': projectId } : undefined;
        const previousRequest = queryClient.getQueryData<Request>(queryKey);
        const shouldUseLightweightPolling = includePayload && previousRequest?.status === 'processing';

        const query =
          !includePayload || shouldUseLightweightPolling
            ? buildRequestDetailPollingQuery(permissions)
            : buildRequestDetailQuery(permissions, payload);

        const data = await graphqlRequest<{ node: Request }>(query, { id }, headers, { signal });
        if (!data.node) {
          throw new Error('Request not found');
        }

        const parsedRequest = requestSchema.parse(data.node);

        if (!shouldUseLightweightPolling) {
          return parsedRequest;
        }

        if (parsedRequest.status !== 'processing') {
          const fullData = await graphqlRequest<{ node: Request }>(buildRequestDetailQuery(permissions, payload), { id }, headers, { signal });
          if (!fullData.node) {
            throw new Error('Request not found');
          }
          return requestSchema.parse(fullData.node);
        }

        return requestSchema.parse({
          ...previousRequest,
          ...parsedRequest,
          requestHeaders: previousRequest?.requestHeaders,
          requestBody: previousRequest?.requestBody,
          responseBody: previousRequest?.responseBody,
          responseChunks: previousRequest?.responseChunks,
          routingDecision: parsedRequest.routingDecision ?? previousRequest?.routingDecision,
          usageLogs: previousRequest?.usageLogs,
        });
      } catch (error) {
        if (signal.aborted) throw error;
        handleError(error, t('common.errors.internalServerError'));
        throw error;
      }
    },
    enabled: enabled && !!id,
    refetchInterval: (query) => {
      if (options?.disableAutoRefresh) {
        return false;
      }

      return query.state.data?.status === 'processing' ? 2000 : false;
    },
  });
}

/**
 * Imperative (non-hook) fetch of a page of requests for drawer navigation.
 * direction 'older' fetches the page after endCursor (older in DESC order).
 * direction 'newer' fetches the page before startCursor (newer in DESC order).
 */
export async function fetchAdjacentRequestPage(params: {
  cursor: string;
  direction: 'older' | 'newer';
  pageSize: number;
  where?: Record<string, any>;
  permissions: { canViewApiKeys: boolean; canViewChannels: boolean; canViewCallerUser: boolean };
  projectId?: string | null;
}): Promise<{ requests: Request[]; pageInfo: RequestConnection['pageInfo'] }> {
  const query = buildRequestsQuery(params.permissions);
  const variables =
    params.direction === 'older' ? { first: params.pageSize, after: params.cursor } : { last: params.pageSize, before: params.cursor };

  const where: Record<string, any> = { ...params.where };
  if (params.projectId) where.projectID = params.projectId;

  const headers = params.projectId ? { 'X-Project-ID': params.projectId } : undefined;
  const data = await graphqlRequest<{ requests: RequestConnection }>(
    query,
    { ...variables, ...requestLogVariables(where), orderBy: { field: 'CREATED_AT', direction: 'DESC' } },
    headers
  );
  const result = requestConnectionSchema.parse(data?.requests);
  return { requests: result.edges.map((e) => e.node), pageInfo: result.pageInfo };
}

export function useRequestExecutions(
  requestID: string,
  variables?: {
    first?: number;
    after?: string;
    orderBy?: { field: 'CREATED_AT'; direction: 'ASC' | 'DESC' };
    where?: Record<string, any>;
  },
  options?: { projectId?: string | null; includePayload?: boolean; enabled?: boolean }
) {
  const { handleError } = useErrorHandler();
  const { t } = useTranslation();
  const permissions = useRequestPermissions();
  const selectedProjectId = useSelectedProjectId();
  const projectId = options?.projectId !== undefined ? options.projectId : selectedProjectId;

  const includePayload = options?.includePayload ?? true;
  return useQuery({
    queryKey: ['request-executions', requestID, variables, permissions, projectId, includePayload],
    gcTime: 0,
    queryFn: async ({ signal }) => {
      try {
        const query = buildRequestExecutionsQuery(permissions, includePayload);
        const headers = projectId ? { 'X-Project-ID': projectId } : undefined;
        const finalVariables = {
          requestID,
          ...variables,
        };
        const data = await graphqlRequest<{ node: { executions: RequestExecutionConnection } }>(query, finalVariables, headers, { signal });
        return requestExecutionConnectionSchema.parse(data?.node?.executions);
      } catch (error) {
        if (signal.aborted) throw error;
        handleError(error, t('common.errors.internalServerError'));
        throw error;
      }
    },
    enabled: !!requestID && (options?.enabled ?? true),
  });
}
