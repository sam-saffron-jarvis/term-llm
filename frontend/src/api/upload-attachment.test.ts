import { afterEach, describe, expect, it, vi } from 'vitest';
import { APIClient, APIError } from './client';
import { endpoints } from './endpoints';
import { readInjectedConfig } from '../app/config';

const config = readInjectedConfig({
  TERM_LLM_UI_PREFIX: '/ui',
  TERM_LLM_UI_VERSION: 'build-one',
} as Window);
afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe('binary upload endpoint', () => {
  it('uses exact raw /uploads, URI filename, original MIME, auth/version/progress, and waits for 201', async () => {
    const requests: FakeXHR[] = [];
    class FakeXHR {
      status = 201;
      responseText = JSON.stringify({
        upload_id: `upl_${'a'.repeat(43)}`,
        filename: 'my photo.png',
        media_type: 'image/png',
        size_bytes: 4,
      });
      upload: { onprogress?: (event: unknown) => void } = {};
      headers: Record<string, string> = {};
      timeout = 0;
      withCredentials = false;
      open = vi.fn();
      send = vi.fn();
      abort = vi.fn();
      onload = () => {};
      ontimeout = () => {};
      setRequestHeader(name: string, value: string) {
        this.headers[name] = value;
      }
      getAllResponseHeaders = () => 'X-Term-LLM-UI-Version: build-one';
      constructor() {
        requests.push(this);
      }
    }
    vi.stubGlobal('XMLHttpRequest', FakeXHR);
    const api = new APIClient(config, { getToken: () => 'test-token', onAuthRequired: vi.fn() });
    const progress = vi.fn();
    const blob = new Blob(['data'], { type: 'image/png' });
    const uploading = endpoints(api).uploadAttachment(blob, 'my photo #é.png', {
      onProgress: progress,
    });
    await vi.waitFor(() => expect(requests).toHaveLength(1));
    const xhr = requests[0];
    expect(xhr.open).toHaveBeenCalledWith('POST', '/ui/uploads', true);
    expect(xhr.send).toHaveBeenCalledWith(blob);
    expect(xhr.timeout).toBe(7 * 60_000);
    expect(xhr.headers).toMatchObject({
      'Content-Type': 'image/png',
      'X-Term-LLM-Upload-Filename': encodeURIComponent('my photo #é.png'),
      Authorization: 'Bearer test-token',
      'X-Term-LLM-UI-Version': 'build-one',
    });
    expect(xhr.withCredentials).toBe(true);
    xhr.upload.onprogress?.({ loaded: 4, total: 4, lengthComputable: true });
    expect(progress).toHaveBeenCalledWith(4, 4);
    xhr.onload();
    await expect(uploading).resolves.toMatchObject({ upload_id: `upl_${'a'.repeat(43)}` });
  });

  it('retries writer saturation only once and honors Retry-After, keeping the same Blob and controls', async () => {
    vi.useFakeTimers();
    const api = new APIClient(config, { getToken: () => '', onAuthRequired: vi.fn() });
    const saturation = new APIError('writers busy', 503);
    saturation.retryAfterMs = 2_000;
    vi.spyOn(api, 'upload').mockRejectedValue(saturation);
    const blob = new Blob(['bytes']);
    const result = endpoints(api)
      .uploadAttachment(blob, 'file.bin')
      .catch((error: unknown) => error);
    await vi.dynamicImportSettled();
    await vi.advanceTimersByTimeAsync(1999);
    expect(api.upload).toHaveBeenCalledOnce();
    await vi.advanceTimersByTimeAsync(1);
    expect(await result).toBe(saturation);
    expect(api.upload).toHaveBeenCalledTimes(2);
    expect(vi.mocked(api.upload).mock.calls[1][1]).toBe(blob);
    expect(vi.mocked(api.upload).mock.calls[0][2]?.headers?.['Content-Type']).toBe(
      'application/octet-stream',
    );
    await vi.advanceTimersByTimeAsync(300_000);
    expect(api.upload).toHaveBeenCalledTimes(2);
  });

  it('aborts saturation backoff without starting a second request', async () => {
    vi.useFakeTimers();
    const api = new APIClient(config, { getToken: () => '', onAuthRequired: vi.fn() });
    vi.spyOn(api, 'upload').mockRejectedValue(new APIError('writers busy', 503));
    const controller = new AbortController();
    const result = endpoints(api)
      .uploadAttachment(new Blob(['bytes']), 'file', { signal: controller.signal })
      .catch((error: unknown) => error);
    await vi.dynamicImportSettled();
    await vi.advanceTimersByTimeAsync(0);
    controller.abort();
    expect(await result).toMatchObject({ name: 'AbortError' });
    await vi.advanceTimersByTimeAsync(30_000);
    expect(api.upload).toHaveBeenCalledOnce();
  });

  it('sends the frozen instance precondition and disables inner createResponse retries', async () => {
    const api = new APIClient(config, { getToken: () => '', onAuthRequired: vi.fn() });
    vi.spyOn(api, 'request').mockResolvedValue(new Response());
    await endpoints(api).createResponse({ stream: true }, 's1', 'request-1', undefined, undefined, {
      serverInstance: 'frozen-instance',
    });
    expect(api.request).toHaveBeenCalledWith(
      '/v1/responses',
      expect.objectContaining({
        headers: expect.objectContaining({ 'X-Term-LLM-Server-Instance': 'frozen-instance' }),
      }),
      expect.objectContaining({ retries: 0, timeoutMs: 0 }),
    );
  });
});

describe('linked binary uploads', () => {
  const api = new APIClient(config, { getToken: () => '', onAuthRequired: vi.fn() });
  const chunk = 32768;
  const accepted = () => Response.json({ token: 'continuation' }, { status: 202 });
  it.each([0, 1, chunk, chunk * 2, chunk * 2 + 1])(
    'sends exactly %i bytes with acknowledged offsets and no empty final request',
    async (size) => {
      const bytes = new Uint8Array(size).map((_, i) => i % 251);
      const original = new Blob([bytes], { type: 'application/pdf' });
      const ref = {
        upload_id: `upl_${'a'.repeat(43)}`,
        filename: '#é.pdf',
        media_type: original.type,
        size_bytes: size,
      };
      let offset = 0;
      const send = vi.fn(async (url: string, init: RequestInit) => {
        expect(url).toBe('/ui/v1/linked-uploads');
        expect(init.method).toBe('POST');
        const headers = new Headers(init.headers);
        expect(headers.get('Content-Type')).toBe(original.type);
        expect(headers.get('X-Term-LLM-Upload-Filename')).toBe(
          offset ? null : encodeURIComponent(ref.filename),
        );
        expect(headers.get('X-Term-LLM-Upload-Size')).toBe(offset ? null : String(size));
        expect(headers.get('X-Term-LLM-Upload-Offset')).toBe(String(offset));
        expect(headers.get('X-Term-LLM-Upload-Token')).toBe(offset ? 'continuation' : null);
        expect(init.body).toBeInstanceOf(Blob);
        const body = init.body as Blob;
        const end = Math.min(size, offset + chunk);
        expect(body.size).toBe(end - offset);
        expect(new Uint8Array(await body.arrayBuffer())).toEqual(bytes.slice(offset, end));
        offset = end;
        return offset === size ? Response.json(ref, { status: 201 }) : accepted();
      });
      vi.stubGlobal('fetch', send);
      const onProgress = vi.fn();
      await expect(
        endpoints(api).uploadLinkedAttachment(original, ref.filename, { onProgress }),
      ).resolves.toEqual(ref);
      expect(send).toHaveBeenCalledTimes(Math.max(1, Math.ceil(size / chunk)));
      expect(onProgress.mock.calls).toEqual(
        Array.from({ length: Math.ceil(size / chunk) - 1 }, (_, i) => [(i + 1) * chunk, size]),
      );
    },
  );
  it.each(['network', '503', 'unexpected status', 'missing token'])(
    'does not replay or skip an uncertain chunk: %s',
    async (failure) => {
      const send = vi.fn().mockResolvedValueOnce(accepted());
      if (failure === 'network') send.mockRejectedValueOnce(new TypeError('disconnected'));
      else
        send.mockResolvedValueOnce(
          Response.json(
            {},
            { status: failure === '503' ? 503 : failure === 'unexpected status' ? 201 : 202 },
          ),
        );
      vi.stubGlobal('fetch', send);
      await expect(
        endpoints(api).uploadLinkedAttachment(new Blob([new Uint8Array(chunk * 3)]), 'data'),
      ).rejects.toThrow();
      expect(send).toHaveBeenCalledTimes(2);
    },
  );
  it('stops before the next chunk when cancelled after an acknowledgement', async () => {
    const controller = new AbortController();
    const send = vi.fn().mockResolvedValue(accepted());
    vi.stubGlobal('fetch', send);
    await expect(
      endpoints(api).uploadLinkedAttachment(new Blob([new Uint8Array(chunk + 1)]), 'data', {
        signal: controller.signal,
        onProgress: () => controller.abort(),
      }),
    ).rejects.toMatchObject({ name: 'AbortError' });
    expect(send).toHaveBeenCalledOnce();
  });
});
