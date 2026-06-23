import { describe, it, expect } from 'vitest';

import { readableTextColor, SUCCESSION_HOURS } from '../succession';

describe('succession utils', () => {
  describe('SUCCESSION_HOURS', () => {
    it('is 24 hour-of-day buckets', () => {
      expect(SUCCESSION_HOURS).toBe(24);
    });
  });

  describe('readableTextColor', () => {
    it('picks dark text on a light fill', () => {
      expect(readableTextColor('rgb(255, 255, 255)')).toBe('#111827');
    });

    it('picks light text on a dark fill', () => {
      expect(readableTextColor('rgb(0, 0, 0)')).toBe('#f9fafb');
    });

    it('reads the underlying rgb of a translucent fill', () => {
      // Low-luminance color regardless of alpha -> light text.
      expect(readableTextColor('rgba(20, 20, 20, 0.5)')).toBe('#f9fafb');
    });

    it('falls back to a safe dark color when the fill cannot be parsed', () => {
      expect(readableTextColor('not-a-color')).toBe('#111827');
    });
  });
});
