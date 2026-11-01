import type { ComponentProps } from 'react';
import { useTranslation } from 'react-i18next';
import { JsonViewer } from '@/components/json-tree-view';
import { getPayloadState } from '../utils/payload-state';

export function PayloadNotice({ body }: { body: unknown }) {
  const { t } = useTranslation();
  const state = getPayloadState(body);
  if (!state) return null;
  const reason = ['disabled', 'too_large', 'expired', 'evicted', 'invalid'].includes(state) ? state : 'unavailable';
  return (
    <p role='status' className='text-muted-foreground rounded-md border p-4 text-sm'>
      {t(`requests.payload.${reason}`)}
    </p>
  );
}

export function RequestPayloadViewer(props: ComponentProps<typeof JsonViewer>) {
  return getPayloadState(props.data) ? <PayloadNotice body={props.data} /> : <JsonViewer {...props} />;
}
