import { useQuery } from '@tanstack/react-query';
import { getTokenFromStorage } from '@/stores/authStore';
import { apiRequest } from '@/lib/api-client';

export interface TransferManifest {
  format: string;
  appVersion: string;
  postgresMajor: number;
  databaseBytes: number;
  dumpBytes: number;
  sha256: string;
  exportedAt: string;
}

export interface TransferJob {
  id: string;
  kind: 'export' | 'import';
  phase: 'awaiting_upload' | 'exporting' | 'uploading' | 'checking' | 'ready' | 'importing' | 'completed' | 'failed' | 'expired';
  bytes: number;
  totalBytes: number;
  createdAt: string;
  updatedAt: string;
  manifest?: TransferManifest;
  targetDatabase?: string;
  error?: string;
  downloadAvailable: boolean;
}

export interface TransferStatus {
  supported: boolean;
  reason: string;
  dialect: string;
  postgresMajor: number;
  importAllowed: boolean;
  importReason: string;
  maxFileBytes: number;
  job: TransferJob | null;
}

const base = '/admin/database-transfer';
export const transferKey = ['database-transfer'] as const;
export const transferActive = (job?: TransferJob | null) =>
  !!job && ['exporting', 'uploading', 'checking', 'importing'].includes(job.phase);
export const transferReserved = (job?: TransferJob | null) =>
  transferActive(job) || job?.phase === 'ready' || job?.phase === 'awaiting_upload';

export function useTransferStatus() {
  return useQuery({
    queryKey: [...transferKey, 'status'],
    queryFn: () => apiRequest<TransferStatus>(`${base}/status`, { requireAuth: true }),
    staleTime: 60_000,
    retry: false,
  });
}

export function useTransferJob(enabled: boolean) {
  return useQuery({
    queryKey: [...transferKey, 'job'],
    queryFn: async () => (await apiRequest<{ job: TransferJob | null }>(`${base}/job`, { requireAuth: true })).job,
    enabled,
    refetchInterval: (query) => (transferActive(query.state.data) ? 1500 : 10_000),
    refetchIntervalInBackground: false,
    retry: false,
  });
}

export const transferRequest = <T>(path: string, method: 'POST' | 'DELETE', body?: unknown) =>
  apiRequest<T>(`${base}${path}`, { method, body, requireAuth: true });

// getRandomValues also works on plain HTTP installations; randomUUID requires HTTPS.
export function transferID() {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6] & 15) | 64;
  bytes[8] = (bytes[8] & 63) | 128;
  const hex = Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join('');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

// Only the small first TAR member is read into memory. The File itself is sent
// directly by XHR; neither upload nor download creates a whole-file ArrayBuffer.
export async function readTransferManifest(file: File): Promise<string> {
  const header = new Uint8Array(await file.slice(0, 512).arrayBuffer());
  const decoder = new TextDecoder('utf-8', { fatal: true });
  const name = decoder.decode(header.slice(0, 100)).replace(/\0.*$/, '');
  const rawSize = decoder.decode(header.slice(124, 136)).replace(/\0.*$/, '').trim();
  const size = /^[0-7]+$/.test(rawSize) ? Number.parseInt(rawSize, 8) : 0;
  if (header.length !== 512 || name !== 'manifest.json' || size < 1 || size > 64 * 1024 || file.size < 512 + size)
    throw new Error('invalid_package');
  const raw = decoder.decode(await file.slice(512, 512 + size).arrayBuffer());
  const manifest = JSON.parse(raw) as Partial<TransferManifest>;
  if (manifest.format !== 'axonhub-postgres-v1' || typeof manifest.sha256 !== 'string') throw new Error('invalid_package');
  return raw;
}

export function uploadTransfer(id: string, file: File, signal: AbortSignal, progress: (value: number) => void): Promise<TransferJob> {
  return new Promise((resolve, reject) => {
    const request = new XMLHttpRequest();
    request.open('PUT', `${base}/uploads/${id}`);
    request.setRequestHeader('Authorization', `Bearer ${getTokenFromStorage() ?? ''}`);
    request.setRequestHeader('Content-Type', 'application/octet-stream');
    request.upload.onprogress = (event) => {
      if (event.lengthComputable) progress(Math.min(100, (event.loaded * 100) / event.total));
    };
    const abort = () => request.abort();
    signal.addEventListener('abort', abort, { once: true });
    request.onloadend = () => signal.removeEventListener('abort', abort);
    request.onerror = () => reject(new Error('status_unknown'));
    request.onabort = () => reject(new Error('upload_interrupted'));
    request.onload = () => {
      try {
        const data = JSON.parse(request.responseText);
        if (request.status < 200 || request.status >= 300) reject(new Error(data.error || 'operation_failed'));
        else resolve(data as TransferJob);
      } catch {
        reject(new Error('status_unknown'));
      }
    };
    if (signal.aborted) {
      reject(new Error('upload_interrupted'));
      return;
    }
    request.send(file);
  });
}
