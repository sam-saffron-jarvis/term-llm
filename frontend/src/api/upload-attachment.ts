import { APIClient, APIError, responseError, type UploadControls } from './client';

export interface UploadedAttachment {
  upload_id: string;
  filename: string;
  media_type: string;
  size_bytes: number;
  download_url?: string;
}

function waitForUploadSlot(delay: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const abort = () => {
      clearTimeout(timer);
      signal?.removeEventListener('abort', abort);
      reject(signal?.reason || new DOMException('Upload cancelled', 'AbortError'));
    };
    const timer = setTimeout(() => {
      signal?.removeEventListener('abort', abort);
      resolve();
    }, delay);
    if (signal?.aborted) abort();
    else signal?.addEventListener('abort', abort, { once: true });
  });
}

/** One bounded saturation retry; ambiguous uploads are left to explicit Retry. */
export async function uploadAttachment(
  api: APIClient,
  blob: Blob,
  filename: string,
  controls: UploadControls = {},
): Promise<UploadedAttachment> {
  const send = () =>
    api.upload<UploadedAttachment>('/uploads', blob, {
      ...controls,
      // Two uploads can queue behind bounded three-minute reverse-Hub writes.
      // Keep a finite client timeout, but do not expire before that transport budget.
      timeoutMs: 7 * 60_000,
      expectedStatus: 201,
      headers: {
        'Content-Type': blob.type || 'application/octet-stream',
        'X-Term-LLM-Upload-Filename': encodeURIComponent(filename),
      },
    });
  try {
    return await send();
  } catch (error) {
    if (!(error instanceof APIError) || error.status !== 503) throw error;
    await waitForUploadSlot(error.retryAfterMs || 1_000, controls.signal);
    return send();
  }
}

const LINKED_CHUNK_BYTES = 32 * 1024;

/** Advance only after acknowledgement. Explicit Retry starts a new transfer at zero. */
export async function uploadLinkedAttachment(
  api: APIClient,
  blob: Blob,
  filename: string,
  controls: UploadControls = {},
): Promise<UploadedAttachment> {
  let token = '';
  let offset = 0;
  const type = blob.type || 'application/octet-stream';
  for (;;) {
    controls.signal?.throwIfAborted();
    const end = Math.min(offset + LINKED_CHUNK_BYTES, blob.size);
    const response = await api.request(
      '/v1/linked-uploads',
      {
        method: 'POST',
        redirect: 'error',
        signal: controls.signal,
        body: blob.slice(offset, end, type),
        headers: {
          'Content-Type': type,
          'X-Term-LLM-Upload-Offset': String(offset),
          ...(offset === 0
            ? {
                'X-Term-LLM-Upload-Size': String(blob.size),
                'X-Term-LLM-Upload-Filename': encodeURIComponent(filename),
              }
            : { 'X-Term-LLM-Upload-Token': token }),
        },
      },
      'mutation',
    );
    if (!response.ok)
      throw responseError(await response.text(), response.status, 'Linked upload failed');
    const final = end === blob.size;
    if (response.status !== (final ? 201 : 202))
      throw new Error(`Unexpected linked upload status ${response.status}.`);
    const result = await response.json();
    if (final) return result;
    if (typeof result?.token !== 'string' || !result.token.trim())
      throw new Error('Missing linked upload token.');
    token = result.token;
    offset = end;
    controls.onProgress?.(offset, blob.size);
  }
}
