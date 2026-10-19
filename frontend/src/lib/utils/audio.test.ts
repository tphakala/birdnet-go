import { describe, it, expect } from 'vitest';
import { DASHBOARD_AUDIO_RETRY_DELAYS_MS, playButtonLabelKey } from './audio';

describe('playButtonLabelKey', () => {
  it('reads as loading while loading, even when playing', () => {
    expect(playButtonLabelKey(true, true)).toBe('media.audio.loading');
    expect(playButtonLabelKey(true, false)).toBe('media.audio.loading');
  });

  it('reads as pause when playing and play otherwise', () => {
    expect(playButtonLabelKey(false, true)).toBe('media.audio.pause');
    expect(playButtonLabelKey(false, false)).toBe('media.audio.play');
  });
});

describe('DASHBOARD_AUDIO_RETRY_DELAYS_MS', () => {
  it('keeps the schedule that spans the backend pending window', () => {
    expect(DASHBOARD_AUDIO_RETRY_DELAYS_MS).toEqual([2000, 4000, 8000, 16000, 30000, 30000]);
  });
});
