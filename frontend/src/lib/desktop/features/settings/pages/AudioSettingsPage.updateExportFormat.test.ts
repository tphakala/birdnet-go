import { describe, it, expect } from 'vitest';
import { chooseBitrateForFormat, type ExportFormat } from './audioExportFormat';

describe('chooseBitrateForFormat', () => {
  it('returns empty string for lossless formats', () => {
    expect(chooseBitrateForFormat('wav', '128k')).toBe('');
    expect(chooseBitrateForFormat('flac', '128k')).toBe('');
  });

  it('seeds format default when current bitrate is empty', () => {
    expect(chooseBitrateForFormat('mp3', '')).toBe('128k');
    expect(chooseBitrateForFormat('aac', '')).toBe('96k');
    expect(chooseBitrateForFormat('opus', '')).toBe('96k');
  });

  it('preserves current bitrate when it is within the target range', () => {
    expect(chooseBitrateForFormat('mp3', '192k')).toBe('192k');
    expect(chooseBitrateForFormat('aac', '128k')).toBe('128k');
    expect(chooseBitrateForFormat('opus', '128k')).toBe('128k');
  });

  it('snaps to format default when current bitrate is below the target min', () => {
    expect(chooseBitrateForFormat('mp3', '16k')).toBe('128k');
    expect(chooseBitrateForFormat('aac', '16k')).toBe('96k');
  });

  it('snaps to format default when current bitrate is above the target max', () => {
    // Opus tops out at 256k, so a 320k carryover from MP3 must be clamped.
    expect(chooseBitrateForFormat('opus', '320k')).toBe('96k');
  });

  it('snaps to format default when current bitrate is malformed', () => {
    expect(chooseBitrateForFormat('mp3', 'garbage')).toBe('128k');
    expect(chooseBitrateForFormat('mp3', 'not-a-bitrate')).toBe('128k');
  });

  it.each<[ExportFormat, ExportFormat]>([
    ['wav', 'mp3'],
    ['flac', 'mp3'],
    ['wav', 'aac'],
    ['flac', 'aac'],
    ['wav', 'opus'],
    ['flac', 'opus'],
  ])('lossless %s -> lossy %s always yields a valid bitrate', (_from, to) => {
    const next = chooseBitrateForFormat(to, '');
    expect(next).toMatch(/^\d+k$/);
  });
});
