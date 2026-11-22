import { describe, it, expect, vi, beforeEach } from 'vitest';
import { cancelInstall, installModel, isNetworkDownloadError, reinstallModel } from './modelsApi';
import { api } from '$lib/utils/api';

vi.mock('$lib/utils/api', () => ({
  api: { get: vi.fn(), post: vi.fn().mockResolvedValue(null) },
}));

describe('install and reinstall request bodies', () => {
  beforeEach(() => {
    vi.mocked(api.post).mockClear();
  });

  // A request with nothing to send keeps the bodiless form these endpoints have
  // always received.
  it('sends no body when there is no variant and nothing was accepted', async () => {
    await installModel('m');
    await installModel('m', undefined, []);
    await reinstallModel('m');
    await reinstallModel('m', []);
    expect(api.post).toHaveBeenCalledTimes(4);
    for (const call of vi.mocked(api.post).mock.calls) {
      expect(call[1]).toBeUndefined();
    }
  });

  it('sends the accepted documents with the variant, and on a reinstall', async () => {
    await installModel('m', 'fp32', ['abc']);
    await reinstallModel('m', ['abc']);
    expect(vi.mocked(api.post).mock.calls).toEqual([
      ['/api/v2/models/install/m', { variantId: 'fp32', acceptedDocuments: ['abc'] }],
      ['/api/v2/models/reinstall/m', { acceptedDocuments: ['abc'] }],
    ]);
  });
});

describe('subscribeInstallProgress', () => {
  // Opens a subscription against a fake event source and delivers one progress event.
  async function subscribeAndDeliver(status: string, withOnCancelled: boolean) {
    const listeners = new Map<string, (event: Event) => void>();
    const close = vi.fn();
    vi.doMock('$lib/utils/ReconnectingEventSource', () => ({
      ReconnectingEventSource: class {
        onerror: (() => void) | null = null;
        addEventListener(type: string, listener: (event: Event) => void) {
          listeners.set(type, listener);
        }
        close = close;
      },
    }));
    vi.resetModules();
    const { subscribeInstallProgress } = await import('./modelsApi');
    const handlers = {
      onProgress: vi.fn(),
      onComplete: vi.fn(),
      onError: vi.fn(),
      onCancelled: vi.fn(),
    };

    subscribeInstallProgress(
      'm',
      handlers.onProgress,
      handlers.onComplete,
      handlers.onError,
      withOnCancelled ? handlers.onCancelled : undefined
    );
    listeners.get('progress')?.(
      new MessageEvent('progress', { data: JSON.stringify({ catalogId: 'm', status }) })
    );
    vi.doUnmock('$lib/utils/ReconnectingEventSource');
    return { ...handlers, close };
  }

  it('calls onCancelled, never onError, for a cancelled event', async () => {
    const { onCancelled, onError, onComplete, close } = await subscribeAndDeliver(
      'cancelled',
      true
    );

    expect(onCancelled).toHaveBeenCalledTimes(1);
    expect(onError).not.toHaveBeenCalled();
    expect(onComplete).not.toHaveBeenCalled();
    expect(close).toHaveBeenCalled();
  });

  it('ends a subscriber without onCancelled through onError for a cancelled event', async () => {
    const { onError, onComplete, close } = await subscribeAndDeliver('cancelled', false);

    expect(onError).toHaveBeenCalledTimes(1);
    expect(onComplete).not.toHaveBeenCalled();
    expect(close).toHaveBeenCalled();
  });
});

describe('cancelInstall', () => {
  it('posts to the cancel route of the entry', async () => {
    vi.mocked(api.post).mockClear();

    await cancelInstall('perch v2');

    expect(api.post).toHaveBeenCalledWith('/api/v2/models/install/perch%20v2/cancel');
  });
});

describe('isNetworkDownloadError', () => {
  // The three CategoryNetwork download-failure formats the backend emits
  // (internal/classifier/model_manager.go), which a mirror endpoint could remedy.
  it.each([
    'HTTP request failed for https://huggingface.co/repo/file: dial tcp: connection refused',
    'HTTP 403 for https://huggingface.co/repo/file',
    'HTTP 429 for https://huggingface.co/repo/file',
    'HTTP 503 for https://hf-mirror.com/repo/file',
    'read error downloading https://huggingface.co/repo/file: unexpected EOF',
  ])('matches a network-shaped download failure: %s', msg => {
    expect(isNetworkDownloadError(msg)).toBe(true);
  });

  // Failures a mirror cannot fix, so the hint must not be offered.
  it.each([
    'Connection to server lost', // frontend-generated: the BirdNET-Go server dropped
    'checksum mismatch for model.onnx',
    'install timed out or failed before progress could be tracked',
    'no space left on device',
    '',
  ])('does not match a non-network failure: %s', msg => {
    expect(isNetworkDownloadError(msg)).toBe(false);
  });
});
