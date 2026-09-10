import { describe, it, expect } from 'vitest';
import { isNetworkDownloadError } from './modelsApi';

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
