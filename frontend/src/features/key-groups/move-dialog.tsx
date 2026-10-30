import { useState } from 'react';
import { useInfiniteQuery } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { useTranslation } from 'react-i18next';
import { useSelectedProjectId } from '@/stores/projectStore';
import { useDebounce } from '@/hooks/use-debounce';
import { usePermissions } from '@/hooks/usePermissions';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { useKeyGroups } from '@/features/apikeys/data/groups';
import { memberFields, type Member, type Group } from './data';
import { ManageDialog } from './manage-dialog';

export function MoveDialog({ group, onClose }: { group: Group; onClose: () => void }) {
  const { t } = useTranslation();
  const project = useSelectedProjectId();
  const { user } = usePermissions();
  const groups = useKeyGroups();
  const [search, setSearch] = useState('');
  const debounced = useDebounce(search, 300);
  const [selected, setSelected] = useState<Map<string, Member>>(new Map());
  const [review, setReview] = useState(false);
  const query = useInfiniteQuery({
    queryKey: ['managementMoveCandidates', user?.id, project, group.id, debounced],
    enabled: !!user && !!project,
    initialPageParam: undefined as string | undefined,
    queryFn: async ({ pageParam, signal }) =>
      (
        await graphqlRequest<{ apiKeys: { edges: { node: Member }[]; pageInfo: { hasNextPage: boolean; endCursor: string | null } } }>(
          `query MoveCandidates($after:Cursor,$where:APIKeyWhereInput!){apiKeys(first:20,after:$after,where:$where){edges{node{${memberFields}}}pageInfo{hasNextPage endCursor}}}`,
          {
            after: pageParam,
            where: {
              nameContainsFold: debounced,
              statusIn: ['enabled', 'disabled'],
              typeNotIn: ['noauth'],
              or: [{ groupRefNEQ: group.id }, { groupRefIsNil: true }],
            },
          },
          { 'X-Project-ID': project! },
          { signal }
        )
      ).apiKeys,
    getNextPageParam: (last) => (last.pageInfo.hasNextPage ? (last.pageInfo.endCursor ?? undefined) : undefined),
  });
  if (review) return <ManageDialog selection={{ kind: 'key', items: [...selected.values()] }} initialGroup={group.id} onClose={onClose} />;
  return (
    <Dialog
      open
      onOpenChange={(v) => {
        if (!v) onClose();
      }}
    >
      <DialogContent className='sm:max-w-xl'>
        <DialogHeader>
          <DialogTitle>
            {t('kg.moveExisting')} · {group.name}
          </DialogTitle>
          <DialogDescription>{t('kg.moveHelp')}</DialogDescription>
        </DialogHeader>
        <Input aria-label={t('kg.keyName')} placeholder={t('kg.keyName')} value={search} onChange={(e) => setSearch(e.target.value)} />
        <div className='flex max-h-80 flex-col gap-2 overflow-auto'>
          {query.data?.pages
            .flatMap((p) => p.edges)
            .map(({ node: k }) => (
              <label key={k.id} className='flex items-center gap-3 rounded-md border p-3'>
                <Checkbox
                  checked={selected.has(k.id)}
                  disabled={!selected.has(k.id) && selected.size >= 100}
                  onCheckedChange={(v) =>
                    setSelected((old) => {
                      const next = new Map(old);
                      v ? next.set(k.id, k) : next.delete(k.id);
                      return next;
                    })
                  }
                />
                <span>
                  {groups.data?.find((g) => g.id === k.groupRef)?.name ?? t('kg.ungrouped')} / {k.name}
                </span>
              </label>
            ))}
        </div>
        {query.error && <p role='alert'>{query.error.message}</p>}
        {query.isPending && <p>{t('kg.loading')}</p>}
        {query.hasNextPage && (
          <Button variant='outline' disabled={query.isFetchingNextPage} onClick={() => query.fetchNextPage()}>
            {t('kg.loadMore')}
          </Button>
        )}
        <DialogFooter>
          <Button variant='outline' onClick={onClose}>
            {t('common.buttons.cancel')}
          </Button>
          <Button disabled={!selected.size} onClick={() => setReview(true)}>
            {t('kg.reviewMove', { count: selected.size })}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
