import { describe, it, expect, vi, afterEach } from 'vitest';
import {
  buildDetectionAudioFilename,
  downloadDetectionAudio,
  CLIP_CONTAINER_EXTENSION,
  extensionFromContentDisposition,
} from './audioDownload';
import type { Detection } from '$lib/types/detection.types';

const playbackDefault = vi.hoisted(() => ({ preset: '' }));
vi.mock('$lib/utils/playbackDenoise', async importOriginal => ({
  ...(await importOriginal<typeof import('$lib/utils/playbackDenoise')>()),
  getPlaybackDenoiseDefault: () => playbackDefault.preset,
}));

function makeDetection(overrides: Partial<Detection>): Detection {
  return {
    id: 42,
    commonName: 'House Sparrow',
    scientificName: 'Passer domesticus',
    date: '2026-06-22',
    time: '14:30:05',
    beginTime: '2026-06-22T14:30:05',
    endTime: '2026-06-22T14:30:08',
    speciesCode: 'houspa',
    confidence: 0.9,
    verified: 'unverified',
    locked: false,
    ...overrides,
  };
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

describe('extensionFromContentDisposition', () => {
  it('reads the extension from the extended filename form', () => {
    expect(
      extensionFromContentDisposition("attachment; filename*=UTF-8''clip_0.5-1.5.m4a", 'aac')
    ).toBe('m4a');
    expect(
      extensionFromContentDisposition("attachment; filename*=UTF-8''clip_1.0-2.0.ogg", 'opus')
    ).toBe('ogg');
  });

  it('reads the extension from the plain filename form', () => {
    expect(extensionFromContentDisposition('attachment; filename="clip.flac"', 'wav')).toBe('flac');
  });

  it('decodes percent-encoded filenames', () => {
    expect(extensionFromContentDisposition("attachment; filename*=UTF-8''a%20b.mp3", 'wav')).toBe(
      'mp3'
    );
  });

  it('falls back when there is no usable filename', () => {
    expect(extensionFromContentDisposition(null, 'wav')).toBe('wav');
    expect(extensionFromContentDisposition('attachment', 'wav')).toBe('wav');
    expect(extensionFromContentDisposition('attachment; filename="clip"', 'wav')).toBe('wav');
    expect(extensionFromContentDisposition("attachment; filename*=UTF-8''%E0%A4%A", 'wav')).toBe(
      'wav'
    );
  });
});

describe('CLIP_CONTAINER_EXTENSION', () => {
  it('maps AAC and Opus to their containers', () => {
    expect(CLIP_CONTAINER_EXTENSION).toEqual({ aac: 'm4a', opus: 'ogg' });
  });
});

describe('downloadDetectionAudio', () => {
  afterEach(() => {
    playbackDefault.preset = '';
    vi.restoreAllMocks();
  });

  /** The href the download anchor carries when the download is triggered. */
  function triggeredHref(): string | null {
    let href: string | null = null;
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (
      this: HTMLAnchorElement
    ) {
      href = this.getAttribute('href');
    });
    downloadDetectionAudio(makeDetection({ id: 42 }));
    return href;
  }

  it('downloads the stored clip when no playback default is set', () => {
    expect(triggeredHref()).toBe('/api/v2/audio/42');
  });

  it('downloads the denoised clip when a playback default is set', () => {
    playbackDefault.preset = 'light-medium';
    expect(triggeredHref()).toBe('/api/v2/audio/42?denoise=light-medium');
  });
});
