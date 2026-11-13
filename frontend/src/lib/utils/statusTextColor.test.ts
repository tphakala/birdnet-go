import { describe, it, expect } from 'vitest';
import { textColorFor, NEUTRAL_TEXT_COLOR as NEUTRAL } from './statusTextColor';

describe('textColorFor', () => {
  it.each([
    ['var(--color-error)', 'var(--text-error)'],
    ['var(--color-warning)', 'var(--text-warning)'],
    ['var(--color-success)', 'var(--text-success)'],
    ['var(--color-info)', 'var(--text-info)'],
  ])('maps the fill %s to %s', (fill, token) => {
    expect(textColorFor(fill)).toBe(token);
  });

  it.each([
    ['an unknown fill', 'var(--color-primary)'],
    ['the neutral fill', NEUTRAL],
    ['a prototype key', 'toString'],
  ])('falls back to base content for %s', (_name, fill) => {
    expect(textColorFor(fill)).toBe(NEUTRAL);
  });
});
