import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  buildDetectionAudioFilename,
  downloadDetectionRecording,
  RECORDING_DOWNLOAD_FORMATS,
  type RecordingDownloadFormat,
} from './audioDownload';
import type { Detection } from '$lib/types/detection.types';
import { downloadBlob } from '$lib/utils/fileHelpers';

vi.mock('$lib/utils/fileHelpers', () => ({
  downloadBlob: vi.fn(),
}));

afterEach(() => {
  vi.clearAllMocks();
  vi.unstubAllGlobals();
});

function makeDetection(overrides: Partial<Detection>): Detection {
  return {
    id: 42,
    commonName: 'House Sparrow',
    scientificName: 'Passer domesticus',
    date: '2026-06-22',
    time: '14:30:05',
    confidence: 0.9,
    ...overrides,
  } as Detection;
}

describe('buildDetectionAudioFilename', () => {
  it('builds <commonName>_<date>_<time>.wav with colons replaced', () => {
    expect(buildDetectionAudioFilename(makeDetection({}))).toBe(
      'House Sparrow_2026-06-22_14-30-05.wav'
    );
  });

  it('sanitizes unsafe characters in the common name', () => {
    expect(
      buildDetectionAudioFilename(makeDetection({ commonName: 'Anna/..\\Hummingbird?' }))
    ).toBe('Anna_.._Hummingbird__2026-06-22_14-30-05.wav');
  });

  it('falls back to the id when date or time is missing', () => {
    expect(buildDetectionAudioFilename(makeDetection({ date: '', time: '' }))).toBe(
      'House Sparrow_42.wav'
    );
  });

  it('falls back to the default name when commonName is empty', () => {
    expect(buildDetectionAudioFilename(makeDetection({ commonName: '' }))).toBe(
      'detection_2026-06-22_14-30-05.wav'
    );
  });
});

describe('downloadDetectionRecording', () => {
  it('offers the original plus every supported transcode format', () => {
    expect(RECORDING_DOWNLOAD_FORMATS.map(format => format.id)).toEqual([
      'original',
      'wav',
      'flac',
      'mp3',
      'aac',
      'opus',
      'alac',
    ]);
  });

  it('uses the original clip extension and a non-empty download filename', async () => {
    let downloadName = '';
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (
      this: HTMLAnchorElement
    ) {
      downloadName = this.download;
    });

    await downloadDetectionRecording(
      makeDetection({ clipName: 'house_sparrow_90p_20260622T143005Z.m4a' }),
      'original'
    );

    expect(downloadName).toBe('House_Sparrow_2026-06-22_14-30-05.m4a');
  });

  it.each<[RecordingDownloadFormat, string]>([
    ['mp3', 'mp3'],
    ['aac', 'm4a'],
    ['opus', 'ogg'],
  ])(
    'posts a %s export and downloads it with the expected extension',
    async (format, extension) => {
      const audioBlob = new Blob(['audio']);
      const fetchMock = vi.fn().mockResolvedValue({
        ok: true,
        blob: vi.fn().mockResolvedValue(audioBlob),
      } as unknown as Response);
      vi.stubGlobal('fetch', fetchMock);

      await downloadDetectionRecording(makeDetection({}), format);

      expect(fetchMock).toHaveBeenCalledOnce();
      const [url, request] = fetchMock.mock.calls[0] as [string, RequestInit];
      expect(url).toContain('/api/v2/audio/42/export');
      expect(request).toMatchObject({
        method: 'POST',
        credentials: 'same-origin',
        body: JSON.stringify({ format }),
      });
      expect(new Headers(request.headers).get('Content-Type')).toBe('application/json');
      expect(downloadBlob).toHaveBeenCalledWith(
        expect.any(Blob),
        `House_Sparrow_2026-06-22_14-30-05.${extension}`
      );
    }
  );

  it('does not download an error response', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 500 })));

    await expect(downloadDetectionRecording(makeDetection({}), 'wav')).rejects.toThrow();
    expect(downloadBlob).not.toHaveBeenCalled();
  });

  it('passes cancellation to fetch and does not download a blob after abort', async () => {
    let resolveBlob!: (blob: Blob) => void;
    const blobPromise = new Promise<Blob>(resolve => {
      resolveBlob = resolve;
    });
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      blob: vi.fn(() => blobPromise),
    } as unknown as Response);
    vi.stubGlobal('fetch', fetchMock);

    const controller = new AbortController();
    const downloadPromise = downloadDetectionRecording(makeDetection({}), 'wav', controller.signal);

    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledOnce());
    const [, request] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(request.signal).toBe(controller.signal);

    controller.abort();
    resolveBlob(new Blob(['audio']));

    await expect(downloadPromise).rejects.toMatchObject({ name: 'AbortError' });
    expect(downloadBlob).not.toHaveBeenCalled();
  });
});
