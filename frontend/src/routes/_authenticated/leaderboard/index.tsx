import { createFileRoute } from '@tanstack/react-router';
import { RouteGuard } from '@/components/route-guard';
import ConsumptionPage from '@/features/consumption';

export const Route = createFileRoute('/_authenticated/leaderboard/')({
  component: () => (
    <RouteGuard requiredScopes={['read_leaderboard']} scopeLevel='system'>
      <ConsumptionPage />
    </RouteGuard>
  ),
});
