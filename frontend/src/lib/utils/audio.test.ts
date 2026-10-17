import { describe, it, expect } from 'vitest';
import { playButtonLabelKey } from './audio';

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
