import { describe, it, expect, vi, afterEach } from 'vitest';
import {
  SPECTROGRAM_REASON,
  MAX_SOURCE_MARKS,
  clearSpectrogramSourceMarks,
  fetchSpectrogramStatus,
  isSpectrogramSourceKnownMissing,
  isTerminalSpectrogramReason,
  markSpectrogramSourceMissing,
  parseSpectrogramStatus,
} from './spectrogramStatus';

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

  it('reads a reason only when it is a string', () => {
    expect(
      parseSpectrogramStatus({ data: { status: 'not_started', reason: 'source_missing' } })
    ).toEqual({
      status: 'not_started',
      queuePosition: 0,
      message: '',
      reason: 'source_missing',
    });
    expect(parseSpectrogramStatus({ status: 'failed', reason: 7 })?.reason).toBeUndefined();
    expect(parseSpectrogramStatus({ status: 'failed', reason: null })?.reason).toBeUndefined();
    // A value this client does not know is kept: it is not terminal, so it is harmless.
    expect(parseSpectrogramStatus({ status: 'failed', reason: 'something_new' })?.reason).toBe(
      'something_new'
    );
  });

  it.each([null, [], 'queued', { data: [] }, { status: 7 }, { data: { status: null } }])(
    'returns undefined for %j',
    body => {
      expect(parseSpectrogramStatus(body)).toBeUndefined();
    }
  );
});

describe('isTerminalSpectrogramReason', () => {
  it.each([
    [SPECTROGRAM_REASON.sourceMissing, true],
    [SPECTROGRAM_REASON.sourceInvalid, true],
    [SPECTROGRAM_REASON.sourcePending, false],
    [SPECTROGRAM_REASON.busy, false],
    [SPECTROGRAM_REASON.renderFailed, false],
    [SPECTROGRAM_REASON.notGenerated, false],
    ['something_new', false],
    [undefined, false],
  ])('answers for %s', (reason, expected) => {
    expect(isTerminalSpectrogramReason(reason)).toBe(expected);
  });
});

describe('known missing source marks', () => {
  afterEach(() => {
    clearSpectrogramSourceMarks();
  });

  it('answers by detection, whether the id is a number or a string', () => {
    expect(isSpectrogramSourceKnownMissing(42)).toBe(false);

    markSpectrogramSourceMissing('42');

    expect(isSpectrogramSourceKnownMissing(42)).toBe(true);
    expect(isSpectrogramSourceKnownMissing('42')).toBe(true);
    expect(isSpectrogramSourceKnownMissing(43)).toBe(false);
  });

  it('forgets the oldest mark once the cap is exceeded', () => {
    for (let id = 1; id <= MAX_SOURCE_MARKS + 1; id++) markSpectrogramSourceMissing(id);

    expect(isSpectrogramSourceKnownMissing(1)).toBe(false);
    expect(isSpectrogramSourceKnownMissing(2)).toBe(true);
    expect(isSpectrogramSourceKnownMissing(MAX_SOURCE_MARKS + 1)).toBe(true);
  });
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
