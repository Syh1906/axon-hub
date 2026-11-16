import { createFileRoute } from '@tanstack/react-router';
import { RouteGuard } from '@/components/route-guard';
import ProtocolRoutesPage from '@/features/protocol-routes';

export const Route = createFileRoute('/_authenticated/protocol-routes/')({
  component: () => (
    <RouteGuard requiredScopes={['read_channels']} scopeLevel='system'>
      <ProtocolRoutesPage />
    </RouteGuard>
  ),
});
