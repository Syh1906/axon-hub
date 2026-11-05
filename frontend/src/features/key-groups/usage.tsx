import { Navigate } from '@tanstack/react-router';
import { useSelectedProjectId } from '@/stores/projectStore';

export default function GroupUsage({ groupRef }: { groupRef: number }) {
  const project = useSelectedProjectId();
  return project ? <Navigate to='/self-usage' search={{ groupRef, projectId: project }} replace /> : null;
}
