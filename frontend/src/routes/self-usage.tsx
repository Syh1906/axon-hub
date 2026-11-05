import { z } from 'zod';
import { createFileRoute } from '@tanstack/react-router';
import { AuthGuard } from '@/components/auth-guard';
import { SelfUsagePage } from '@/features/self-usage';
import { ManagedGroupPage } from '@/features/self-usage/components/managed-group-page';

const searchSchema = z.union([
  z.object({ groupRef: z.coerce.number().int().nonnegative(), projectId: z.string().min(1) }),
  z.object({ groupRef: z.undefined().optional(), projectId: z.undefined().optional() }),
]);

export const Route = createFileRoute('/self-usage')({
  validateSearch: (search) => searchSchema.parse(search),
  component: UsageRoute,
});

function UsageRoute() {
  const { groupRef, projectId } = Route.useSearch();
  return groupRef !== undefined && projectId ? (
    <AuthGuard>
      <ManagedGroupPage key={`${projectId}:${groupRef}`} groupRef={groupRef} project={projectId} />
    </AuthGuard>
  ) : (
    <SelfUsagePage />
  );
}
