import { describe, it, expect, vi, beforeEach } from 'vitest';
import { installModel, isNetworkDownloadError, reinstallModel } from './modelsApi';
import { api } from '$lib/utils/api';

vi.mock('$lib/utils/api', () => ({
  api: { get: vi.fn(), post: vi.fn().mockResolvedValue(null) },
}));

describe('install and reinstall request bodies', () => {
  beforeEach(() => {
    vi.mocked(api.post).mockClear();
  });

  // The server tolerates an empty body only when there is none at all, and older
  // servers never saw one, so a request with nothing to say must send no body.
  it('sends no body when there is no variant and nothing was accepted', async () => {
    await installModel('m');
    await installModel('m', undefined, []);
    await reinstallModel('m');
    await reinstallModel('m', []);
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
