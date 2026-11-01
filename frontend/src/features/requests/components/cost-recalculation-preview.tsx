import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';

type Preview = {
  recordedCost: number | null;
  proposedCost: number | null;
  reason: string;
  pricedAt: string | null;
  images: { width: number; height: number; requestedWidth: number; requestedHeight: number; tier: string; source: string }[];
};
const query = `query RequestCostPreview($id: ID!) { node(id: $id) { ... on Request {
  costRecalculationPreview { recordedCost proposedCost reason pricedAt images { width height requestedWidth requestedHeight tier source } }
} } }`;

export function CostRecalculationPreview({
  requestId,
  projectId,
  formatCurrency,
}: {
  requestId: string;
  projectId?: string | null;
  formatCurrency: (value: number) => string;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const { data, error, isFetching } = useQuery({
    queryKey: ['request-cost-preview', requestId, projectId],
    enabled: open,
    staleTime: 0,
    queryFn: () =>
      graphqlRequest<{ node: { costRecalculationPreview: Preview } }>(
        query,
        { id: requestId },
        projectId ? { 'X-Project-ID': projectId } : undefined
      ),
  });
  const preview = data?.node?.costRecalculationPreview;
  return (
    <div className='mb-3 flex flex-col gap-2'>
      <Button variant='outline' size='sm' className='self-start' disabled={isFetching} onClick={() => setOpen(!open)}>
        {t(isFetching ? 'price.preview.loading' : open ? 'price.preview.close' : 'price.preview.open')}
      </Button>
      {open && (
        <div className='rounded-md border p-3 text-sm' role='status'>
          {error
            ? t('price.preview.failed')
            : preview && (
                <>
                  <p>
                    {preview.proposedCost == null
                      ? t(`price.preview.reason.${preview.reason}`, { defaultValue: t('price.preview.unavailable') })
                      : t('price.preview.amount', { amount: formatCurrency(preview.proposedCost) })}
                  </p>
                  {preview.images.map((image, index) => (
                    <p key={index} className='text-muted-foreground text-xs'>
                      {image.width > 0
                        ? `${image.width} × ${image.height}`
                        : image.requestedWidth > 0
                          ? t('price.image.requestedDimensions', { width: image.requestedWidth, height: image.requestedHeight })
                          : image.tier}
                      {' · '}
                      {t(`price.image.source.${image.source}`)}
                      {image.tier && ` · ${image.tier}`}
                    </p>
                  ))}
                  <p className='text-muted-foreground mt-1 text-xs'>{t('price.preview.notice')}</p>
                </>
              )}
        </div>
      )}
    </div>
  );
}
