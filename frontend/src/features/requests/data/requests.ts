import { COST_PRICING_EVIDENCE_FIELDS } from '@/components/cost-pricing-evidence';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useCursorList } from '@/hooks/use-cursor-list';
import { graphqlRequest } from '@/gql/graphql';
import { useTranslation } from 'react-i18next';
import { useSelectedProjectId } from '@/stores/projectStore';
import { useErrorHandler } from '@/hooks/use-error-handler';
import { usePermissions } from '@/hooks/usePermissions';
import { useRequestPermissions } from '../../../hooks/useRequestPermissions';
import {
  Request,
  RequestConnection,
  RequestExecutionConnection,
  requestConnectionSchema,
  requestExecutionConnectionSchema,
  requestSchema,
} from './schema';

// Requests expose their reporting timezone without granting system settings access.
export function useRequestLogTimezone() {
  const projectId = useSelectedProjectId();
  const { hasScope } = usePermissions();
  return useQuery({
    queryKey: ['request-log-timezone', projectId],
    enabled: hasScope('read_requests'),
    queryFn: async () => {
      const data = await graphqlRequest<{ requestLogTimezone: string }>(`query { requestLogTimezone }`, {}, projectId ? { 'X-Project-ID': projectId } : undefined);
      return data.requestLogTimezone;
    },
  });
}

// Dynamic GraphQL query builder
function buildRequestsQuery(permissions: { canViewApiKeys: boolean; canViewChannels: boolean; canViewCallerUser: boolean }) {
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
                  }`
    : '';

  return `
    query GetRequests(
      $first: Int
      $after: Cursor
      $last: Int
      $before: Cursor
      $orderBy: RequestOrder
      $where: RequestWhereInput
      $filter: RequestLogFilterInput
    ) {
      requests: requestLogs(first: $first, after: $after, last: $last, before: $before, orderBy: $orderBy, where: $where, filter: $filter) {
        edges {
          node {
            id
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
            metricsLatencyMs
            metricsFirstTokenLatencyMs
            metricsReasoningDurationMs
            executions(first: 10, orderBy: { field: CREATED_AT, direction: DESC }) {
              edges {
                node {
                  id
                  createdAt
                  modelID
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
              pageInfo {
                hasNextPage
                hasPreviousPage
                startCursor
                endCursor
              }
              totalCount
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
                  costItems { ${COST_PRICING_EVIDENCE_FIELDS} image { width height tier source tierSource } imageRule unpriced itemCode promptWriteCacheVariantCode quantity subtotal tierBreakdown { upTo units subtotal } }
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
        totalCount
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

function buildRequestDetailQuery(permissions: { canViewApiKeys: boolean; canViewChannels: boolean; canViewCallerUser: boolean }) {
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
          source
          modelID
          stream
          clientIP
          clientStatusCode
          projectID
          dataStorageID
          contentSaved
          contentStorageKey
          requestHeaders
          requestBody
          responseBody
          responseChunks
          status
          format
          ${REQUEST_ROUTING_DECISION_SELECTION}
          metricsReasoningDurationMs
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
                  totalCost
                  costPriceMultiplier
                  costItems { ${COST_PRICING_EVIDENCE_FIELDS} image { width height tier source tierSource } imageRule unpriced itemCode promptWriteCacheVariantCode quantity subtotal tierBreakdown { upTo units subtotal } }
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
          source
          modelID
          stream
          clientIP
          clientStatusCode
          projectID
          dataStorageID
          contentSaved
          contentStorageKey
          status
          format
          ${REQUEST_ROUTING_DECISION_SELECTION}
          metricsReasoningDurationMs
        }
      }
    }
  `;
}

function buildRequestExecutionsQuery(permissions: { canViewChannels: boolean }) {
  const channelFields = permissions.canViewChannels
    ? `
              channel {
                  id
                  name
                  type
                  baseURL
                  websiteURL
                  tags
              }`
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
                projectID
                dataStorageID
                requestHeaders
                requestBody
                responseBody
                responseChunks
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

// Keep the Ent where input separate from log-specific filters in both list and drawer navigation.
function requestLogVariables(where: Record<string, any> = {}) {
  const { channelTagsIn, apiKeyGroupRefsIn, timeRange, ...entWhere } = where;
  return { where: entWhere, filter: { channelTagsIn, apiKeyGroupRefsIn, timeRange } };
}

// Query hooks
export function useInfiniteRequests(where: Record<string, any> | undefined, pageSize: number) {
  const permissions = useRequestPermissions();
  const projectId = useSelectedProjectId();
  return useCursorList({
    queryKey: ['requests', 'infinite', where, pageSize, permissions, projectId],
    refetchOnMount: 'always',
    fetchPage: async (after, signal) => {
      const data = await graphqlRequest<{ requests: RequestConnection }>(buildRequestsQuery(permissions), {
        first: pageSize,
        after,
        orderBy: { field: 'CREATED_AT', direction: 'DESC' },
        ...requestLogVariables({ ...where, ...(projectId && { projectID: projectId }) }),
      }, projectId ? { 'X-Project-ID': projectId } : undefined, { signal });
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
  options?: { projectId?: string | null; scopeToSelectedProject?: boolean; enabled?: boolean }
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
    queryFn: async () => {
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
          headers
        );
        return requestConnectionSchema.parse(data?.requests);
      } catch (error) {
        handleError(error, t('common.errors.internalServerError'));
        throw error;
      }
    },
    enabled,
    refetchOnWindowFocus: false,
  });
}

export function useRequest(
  id: string,
  options?: {
    projectId?: string | null;
    enabled?: boolean;
    disableAutoRefresh?: boolean;
  }
) {
  const { handleError } = useErrorHandler();
  const { t } = useTranslation();
  const permissions = useRequestPermissions();
  const selectedProjectId = useSelectedProjectId();
  const queryClient = useQueryClient();
  const projectId = options?.projectId !== undefined ? options.projectId : selectedProjectId;
  const enabled = options?.enabled ?? true;

  const queryKey = ['request', id, permissions, projectId] as const;

  return useQuery({
    queryKey,
    queryFn: async () => {
      try {
        const headers = projectId ? { 'X-Project-ID': projectId } : undefined;
        const previousRequest = queryClient.getQueryData<Request>(queryKey);
        const shouldUseLightweightPolling = previousRequest?.status === 'processing';

        const query = shouldUseLightweightPolling ? buildRequestDetailPollingQuery(permissions) : buildRequestDetailQuery(permissions);

        const data = await graphqlRequest<{ node: Request }>(query, { id }, headers);
        if (!data.node) {
          throw new Error('Request not found');
        }

        const parsedRequest = requestSchema.parse(data.node);

        if (!shouldUseLightweightPolling) {
          return parsedRequest;
        }

        if (parsedRequest.status !== 'processing') {
          const fullData = await graphqlRequest<{ node: Request }>(buildRequestDetailQuery(permissions), { id }, headers);
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
  options?: { projectId?: string | null }
) {
  const { handleError } = useErrorHandler();
  const { t } = useTranslation();
  const permissions = useRequestPermissions();
  const selectedProjectId = useSelectedProjectId();
  const projectId = options?.projectId !== undefined ? options.projectId : selectedProjectId;

  return useQuery({
    queryKey: ['request-executions', requestID, variables, permissions, projectId],
    queryFn: async () => {
      try {
        const query = buildRequestExecutionsQuery(permissions);
        const headers = projectId ? { 'X-Project-ID': projectId } : undefined;
        const finalVariables = {
          requestID,
          ...variables,
        };
        const data = await graphqlRequest<{ node: { executions: RequestExecutionConnection } }>(query, finalVariables, headers);
        return requestExecutionConnectionSchema.parse(data?.node?.executions);
      } catch (error) {
        handleError(error, t('common.errors.internalServerError'));
        throw error;
      }
    },
    enabled: !!requestID,
  });
}
