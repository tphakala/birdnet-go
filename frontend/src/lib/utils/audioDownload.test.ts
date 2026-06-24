import { describe, it, expect } from 'vitest';
import { buildDetectionAudioFilename } from './audioDownload';
import type { Detection } from '$lib/types/detection.types';

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
