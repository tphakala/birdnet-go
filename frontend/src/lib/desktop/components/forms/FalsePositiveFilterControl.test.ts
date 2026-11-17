import { describe, it, expect, vi } from 'vitest';
import { render } from '@testing-library/svelte';
import FalsePositiveFilterControl from './FalsePositiveFilterControl.svelte';

// Level values that differ from their positions, as the bat levels do (0, 2, 4), so a dot fill
// that reads the level value instead of its position fails.
const LEVELS = [0, 2, 4].map(value => ({ value, nameKey: `level.${value}`, badgeClass: '' }));
const MIDDLE_LEVEL = 2;

function dots(disabled: boolean, level = MIDDLE_LEVEL): Element[] {
  const { container } = render(FalsePositiveFilterControl, {
    props: {
      id: 'fp',
      level,
      levels: LEVELS,
      onUpdate: vi.fn(),
      getDescription: () => 'description',
      disabled,
    },
  });
  return Array.from(container.querySelectorAll('[aria-hidden="true"] > div'));
}

const isFilled = (dot: Element) => dot.className.includes('bg-[var(--color-primary)]');

describe('FalsePositiveFilterControl stepped dots', () => {
  it('fills the dots up to the level while enabled', () => {
    // The middle level, read dot by dot, so filling every dot, none, or from the wrong end fails.
    expect(dots(false).map(isFilled)).toEqual([true, true, false]);
  });

  it('shows every dot neutral while disabled', () => {
    expect(dots(true).map(isFilled)).toEqual([false, false, false]);
  });
});
