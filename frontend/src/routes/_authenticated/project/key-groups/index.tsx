import { createFileRoute } from '@tanstack/react-router';
import { ProjectGuard } from '@/components/project-guard';
import { RouteGuard } from '@/components/route-guard';
import KeyGroups from '@/features/key-groups';

export const Route = createFileRoute('/_authenticated/project/key-groups/')({
  component: () => (
    <ProjectGuard>
      <RouteGuard requiredScopes={['read_api_keys']} scopeLevel='any'>
        <KeyGroups />
      </RouteGuard>
    </ProjectGuard>
  ),
});
