import { describe, it, expect, vi, afterEach } from 'vitest';
import { fetchSpectrogramStatus, parseSpectrogramStatus } from './spectrogramStatus';

const STATUS_URL = '/api/v2/spectrogram/42/status?size=md';

function jsonResponse(body: unknown, ok = true): Response {
  // A partial Response: the helper reads only these members.
  return {
    ok,
    status: ok ? 200 : 500,
    json: () => Promise.resolve(body),
  } as unknown as Response;
}

describe('parseSpectrogramStatus', () => {
  it.each([
    [
      'a v2 envelope',
      { data: { status: 'queued', queuePosition: 3, message: 'waiting' } },
      { status: 'queued', queuePosition: 3, message: 'waiting' },
    ],
    [
      'a bare status',
      { status: 'exists', queuePosition: 0, message: '' },
      { status: 'exists', queuePosition: 0, message: '' },
    ],
    [
      'missing optional fields',
      { status: 'generating' },
      { status: 'generating', queuePosition: 0, message: '' },
    ],
  ])('reads %s', (_name, body, expected) => {
    expect(parseSpectrogramStatus(body)).toEqual(expected);
  });

  it.each([null, [], 'queued', { data: [] }, { status: 7 }, { data: { status: null } }])(
    'returns undefined for %j',
    body => {
      expect(parseSpectrogramStatus(body)).toBeUndefined();
    }
  );
});

describe('fetchSpectrogramStatus', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('returns the parsed status', async () => {
    const fetchMock = vi.fn(() => Promise.resolve(jsonResponse({ data: { status: 'queued' } })));
    vi.stubGlobal('fetch', fetchMock);
    const signal = new AbortController().signal;

    await expect(fetchSpectrogramStatus(STATUS_URL, signal)).resolves.toEqual({
      status: 'queued',
      queuePosition: 0,
      message: '',
    });
    expect(fetchMock).toHaveBeenCalledWith(STATUS_URL, { signal });
  });

  it.each([
    ['a network error', () => Promise.reject(new TypeError('Failed to fetch'))],
    ['an abort', () => Promise.reject(new DOMException('aborted', 'AbortError'))],
    ['a non-ok answer', () => Promise.resolve(jsonResponse({ status: 'queued' }, false))],
    [
      'an unreadable body',
      () =>
        Promise.resolve({
          ok: true,
          json: () => Promise.reject(new SyntaxError('bad json')),
        } as unknown as Response),
    ],
  ])('returns undefined after %s', async (_name, answer) => {
    vi.stubGlobal('fetch', vi.fn(answer));

    await expect(
      fetchSpectrogramStatus(STATUS_URL, new AbortController().signal)
    ).resolves.toBeUndefined();
  });
});
