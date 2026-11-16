import { useQuery } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import type { FilterCondition } from '@/features/models/data/schema';

export type RouteSummary = { modelID: string; name: string; native: number; converted: number; conditional: number; excluded: number };
export type ProtocolRoute = {
  channelID: number;
  channelName: string;
  actualModel: string;
  apiFormat: string;
  state: string;
  reason: string;
  condition?: FilterCondition | null;
};
export type RouteEvidence = { channelID: number; actualModel: string; apiFormat: string; lastSuccess: string };
type RoutePage = { totalCount: number; items: RouteSummary[] };
type RouteDetail = { routes: ProtocolRoute[]; evidence: RouteEvidence[]; evidenceReadable: boolean };

export function useProtocolRoutes(protocol: string, search: string, page: number) {
  return useQuery({
    queryKey: ['protocol-routes', protocol, search, page],
    queryFn: async ({ signal }) => {
      const result = await graphqlRequest<{ protocolRoutes: RoutePage }>(
        `query ProtocolRoutes($protocol: String!, $search: String!, $offset: Int!) {
        protocolRoutes(protocol: $protocol, search: $search, offset: $offset, limit: 20) {
          totalCount items { modelID name native converted conditional excluded }
        }
      }`,
        { protocol, search, offset: page * 20 },
        undefined,
        { signal }
      );
      return result.protocolRoutes;
    },
    staleTime: 30_000,
    gcTime: 60_000,
    refetchOnWindowFocus: false,
    refetchOnMount: 'always',
  });
}

export function useProtocolRouteDetail(protocol: string, modelID: string) {
  return useQuery({
    queryKey: ['protocol-route-detail', protocol, modelID],
    queryFn: async ({ signal }) => {
      const result = await graphqlRequest<{ protocolRouteDetail: RouteDetail }>(
        `query ProtocolRouteDetail($protocol: String!, $modelID: String!) {
        protocolRouteDetail(protocol: $protocol, modelID: $modelID) {
          routes {
            channelID channelName actualModel apiFormat state reason
            condition {
              type logic field operator value
              conditions {
                type logic field operator value
                conditions {
                  type logic field operator value
                  conditions { type logic field operator value }
                }
              }
            }
          }
          evidence { channelID actualModel apiFormat lastSuccess } evidenceReadable
        }
      }`,
        { protocol, modelID },
        undefined,
        { signal }
      );
      return result.protocolRouteDetail;
    },
    staleTime: 30_000,
    gcTime: 60_000,
    refetchOnWindowFocus: false,
    refetchOnMount: 'always',
  });
}

export function routeEvidence(route: ProtocolRoute, evidence: RouteEvidence[]): string | undefined {
  return evidence.find((e) => e.channelID === route.channelID && e.actualModel === route.actualModel && e.apiFormat === route.apiFormat)
    ?.lastSuccess;
}
