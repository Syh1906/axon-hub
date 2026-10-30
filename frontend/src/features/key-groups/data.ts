import { useQuery, useQueryClient } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { useSelectedProjectId } from '@/stores/projectStore';
import { buildGUID } from '@/lib/utils';
import { usePermissions } from '@/hooks/usePermissions';
import { budgetFields, type BudgetView, type BudgetRule } from '@/features/budgets/data';

export type BudgetPatch = { rules?: BudgetRule[]; timezone?: string; setExpiry?: boolean; expiresAt?: string | null; revision?: number };
export type KeyInput = { name: string; type: string; scopes?: string[]; allowedIPs?: string[]; templateID?: number; budget?: BudgetPatch };
export type ManagementRow = {
  rowID: string;
  action: string;
  targetID?: number;
  groupRef?: number;
  name?: string;
  note?: string;
  enabled?: boolean;
  revision?: number;
  updatedAt?: string;
  budget?: BudgetPatch;
  keys?: KeyInput[];
  templateID?: number;
  allowedIPs?: string[];
  setAllowedIPs?: boolean;
};
export type Result = { rowID: string; success: boolean; error: string; groupID: number; keyIDs: number[] };
export type Group = {
  id: number;
  name: string;
  note: string;
  enabled: boolean;
  revision: number;
  keyCount: number;
  createdAt: string;
  budget: BudgetView;
};
export type Member = {
  id: string;
  name: string;
  type: string;
  status: string;
  updatedAt: string;
  groupRef: number | null;
  allowedIps: string[];
  profiles: { activeProfile: string } | null;
};
export const memberFields = 'id name type status updatedAt groupRef allowedIps profiles { activeProfile }';
const resultFields = 'rowID success error groupID keyIDs';
export function useManagementAPI() {
  const project = useSelectedProjectId();
  const cache = useQueryClient();
  const request = <T>(query: string, variables?: Record<string, unknown>) => {
    if (!project) throw new Error('Project required');
    return graphqlRequest<T>(query, variables, { 'X-Project-ID': project });
  };
  return {
    budget: async (ownerID: number) =>
      (
        await request<{ apiKeyBudget: BudgetView }>(
          `query ManagementBudget($ownerID:Int!){apiKeyBudget(kind:"key",ownerID:$ownerID){${budgetFields}}}`,
          { ownerID }
        )
      ).apiKeyBudget,
    preview: async (rows: ManagementRow[]) =>
      (
        await request<{ previewKeyManagement: Result[] }>(
          `mutation PreviewKeyManagement($rows:[KeyManagementRow!]!){previewKeyManagement(rows:$rows){${resultFields}}}`,
          { rows }
        )
      ).previewKeyManagement,
    apply: async (operationID: string, rows: ManagementRow[]) =>
      (
        await request<{ applyKeyManagement: Result[] }>(
          `mutation ApplyKeyManagement($operationID:String!,$rows:[KeyManagementRow!]!){applyKeyManagement(operationID:$operationID,rows:$rows){${resultFields}}}`,
          { operationID, rows }
        )
      ).applyKeyManagement,
    recover: async (operationID: string) =>
      (
        await request<{ keyManagementResults: Result[] }>(
          `query KeyManagementResults($operationID:String!){keyManagementResults(operationID:$operationID){${resultFields}}}`,
          { operationID }
        )
      ).keyManagementResults,
    credentials: async (ids: number[]) =>
      (
        await request<{ apiKeys: { edges: { node: { id: string; name: string; key: string; groupRef: number | null } }[] } }>(
          `query ManagementCredentials($where:APIKeyWhereInput!){apiKeys(first:100,where:$where){edges{node{id name key groupRef}}}}`,
          { where: { idIn: ids.map((id) => buildGUID('APIKey', String(id))) } }
        )
      ).apiKeys.edges.map((e) => e.node),
    refresh: () =>
      Promise.all(
        [
          'managedKeyGroups',
          'managedGroupKeys',
          'managementMoveCandidates',
          'apiKeyGroups',
          'apiKeys',
          'apiKey',
          'keyBudgets',
          'consumption',
          'requests',
          'request',
          'analyticsMetadata',
          'analyticsDimensionStats',
          'analyticsAPIKeyStats',
          'requestStatsByAPIKey',
          'tokenStatsByAPIKey',
          'costStatsByAPIKey',
        ].map((key) => cache.invalidateQueries({ queryKey: [key] }))
      ),
  };
}
export function useManagedGroups(search: string, status: string, offset: number, limit: number, descending: boolean) {
  const project = useSelectedProjectId();
  const { user } = usePermissions();
  return useQuery({
    queryKey: ['managedKeyGroups', user?.id, project, search, status, offset, limit, descending],
    enabled: !!project,
    queryFn: async ({ signal }) =>
      (
        await graphqlRequest<{ managedKeyGroups: { items: Group[]; total: number; ungroupedCount: number } }>(
          `query ManagedGroups($search:String!,$status:String!,$offset:Int!,$limit:Int!,$descending:Boolean!){managedKeyGroups(search:$search,status:$status,offset:$offset,limit:$limit,descending:$descending){total ungroupedCount items{id name note enabled revision keyCount createdAt budget{${budgetFields}}}}}`,
          { search, status, offset, limit, descending },
          { 'X-Project-ID': project! },
          { signal }
        )
      ).managedKeyGroups,
  });
}
export function useManagedMembers(groupRef: number, offset: number) {
  const project = useSelectedProjectId();
  const { user } = usePermissions();
  return useQuery({
    queryKey: ['managedGroupKeys', user?.id, project, groupRef, offset],
    enabled: !!project,
    queryFn: async ({ signal }) =>
      (
        await graphqlRequest<{ managedGroupKeys: { items: Member[]; total: number } }>(
          `query ManagedMembers($groupRef:Int!,$offset:Int!){managedGroupKeys(groupRef:$groupRef,offset:$offset,limit:10){total items{${memberFields}}}}`,
          { groupRef, offset },
          { 'X-Project-ID': project! },
          { signal }
        )
      ).managedGroupKeys,
  });
}
