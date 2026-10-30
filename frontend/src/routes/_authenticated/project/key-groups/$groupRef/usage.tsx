import { createFileRoute } from '@tanstack/react-router';
import { ProjectGuard } from '@/components/project-guard';
import { RouteGuard } from '@/components/route-guard';
import GroupUsage from '@/features/key-groups/usage';

export const Route = createFileRoute('/_authenticated/project/key-groups/$groupRef/usage')({
  component: GroupUsageRoute,
});

function GroupUsageRoute() {
  const { groupRef } = Route.useParams();
  return (
    <ProjectGuard>
      <RouteGuard requiredScopes={['read_api_keys']} scopeLevel='any'>
        <GroupUsage groupRef={Number(groupRef)} />
      </RouteGuard>
    </ProjectGuard>
  );
}
