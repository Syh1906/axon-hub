import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { useSelectedProjectId } from '@/stores/projectStore';
import { usePermissions } from '@/hooks/usePermissions';

export type BudgetPeriod = 'five_hour' | 'daily' | 'weekly' | 'monthly' | 'total';
export type BudgetRule = { period: BudgetPeriod; mode: 'fixed' | 'rolling'; limit: string; minute: number; restoreCalendar?: boolean };
export type BudgetWindow = BudgetRule & {
  settled: string;
  reserved: string;
  available: string;
  windowStart: string | null;
  windowEnd: string | null;
  nextReleaseAt: string | null;
  resetAt: string | null;
  canResetCycle: boolean;
  effectiveSchedule: string;
  timezone: string;
  pendingRule: BudgetRule | null;
  pendingAt: string | null;
};
export type BudgetView = {
  ownerKind: string;
  ownerID: number;
  revision: number;
  timezone: string;
  expiresAt: string | null;
  accountingStartedAt: string | null;
  serverTime: string;
  hasLimits: boolean;
  expired: boolean;
  windows: BudgetWindow[];
};
export type BudgetTarget = { kind: 'key' | 'group'; ownerID: number; name: string; groupID?: number };
export const budgetFields = `ownerKind ownerID revision timezone expiresAt accountingStartedAt serverTime hasLimits expired windows { period mode minute limit settled reserved available windowStart windowEnd nextReleaseAt resetAt canResetCycle effectiveSchedule timezone pendingRule { period mode minute limit } pendingAt }`;
export function useBudget(target: BudgetTarget, enabled = true) {
  const project = useSelectedProjectId();
  const { user } = usePermissions();
  return useQuery({
    queryKey: ['keyBudgets', user?.id, project, target.kind, target.ownerID],
    enabled: enabled && !!project && !!user && target.ownerID > 0,
    staleTime: 30_000,
    queryFn: async ({ signal }) =>
      (
        await graphqlRequest<{ apiKeyBudget: BudgetView }>(
          `query Budget($kind: String!, $ownerID: Int!) { apiKeyBudget(kind: $kind, ownerID: $ownerID) { ${budgetFields} } }`,
          { kind: target.kind, ownerID: target.ownerID },
          { 'X-Project-ID': project! },
          { signal }
        )
      ).apiKeyBudget,
  });
}
export type BudgetSettings = { rules: BudgetRule[]; timezone: string; expiresAt: string | null; revision: number; operationID: string };
export type BudgetReset = { periods: BudgetPeriod[]; resetCycle: boolean; revision: number; operationID: string };
export function useBudgetMutation(target: BudgetTarget, reset = false) {
  const project = useSelectedProjectId();
  const client = useQueryClient();
  return useMutation({
    mutationFn: async (input: BudgetSettings | BudgetReset) => {
      const field = reset ? 'resetAPIKeyBudget' : 'saveAPIKeyBudget';
      const type = reset ? 'BudgetResetInput' : 'BudgetSettingsInput';
      return graphqlRequest<Record<string, BudgetView>>(
        `mutation ChangeBudget($kind: String!, $ownerID: Int!, $input: ${type}!) { ${field}(kind: $kind, ownerID: $ownerID, input: $input) { ${budgetFields} } }`,
        { kind: target.kind, ownerID: target.ownerID, input },
        { 'X-Project-ID': project! }
      );
    },
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ['keyBudgets'] });
      await client.invalidateQueries({ queryKey: ['managedKeyGroups'] });
    },
  });
}
export function hasBudgetLimits(view: BudgetView) {
  return view.windows.some((rule) => Number(rule.limit) > 0);
}

export function useBudgetResetPreview(target: BudgetTarget, input: BudgetReset) {
  const project = useSelectedProjectId();
  const { user } = usePermissions();
  return useQuery({
    queryKey: ['budgetResetPreview', user?.id, project, target.kind, target.ownerID, input],
    enabled: !!project && input.periods.length > 0,
    retry: false,
    staleTime: 0,
    queryFn: async ({ signal }) =>
      (
        await graphqlRequest<{ previewAPIKeyBudgetReset: BudgetView }>(
          `query PreviewBudgetReset($kind:String!,$ownerID:Int!,$input:BudgetResetInput!){previewAPIKeyBudgetReset(kind:$kind,ownerID:$ownerID,input:$input){${budgetFields}}}`,
          { kind: target.kind, ownerID: target.ownerID, input },
          { 'X-Project-ID': project! },
          { signal }
        )
      ).previewAPIKeyBudgetReset,
  });
}
