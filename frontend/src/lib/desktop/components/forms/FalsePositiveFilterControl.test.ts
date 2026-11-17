import { describe, it, expect, vi } from 'vitest';
import { render } from '@testing-library/svelte';
import FalsePositiveFilterControl from './FalsePositiveFilterControl.svelte';

const LEVELS = [0, 1, 2].map(value => ({ value, nameKey: `level.${value}`, badgeClass: '' }));

function dots(disabled: boolean): Element[] {
  const { container } = render(FalsePositiveFilterControl, {
    props: {
      id: 'fp',
      level: 2,
      levels: LEVELS,
      onUpdate: vi.fn(),
      getDescription: () => 'description',
      disabled,
    },
  });
  return Array.from(container.querySelectorAll('[aria-hidden="true"] > div'));
}

describe('FalsePositiveFilterControl stepped dots', () => {
  it('fills the dots up to the level while enabled', () => {
    const filled = dots(false).filter(dot => dot.className.includes('bg-[var(--color-primary)]'));
    expect(filled).toHaveLength(LEVELS.length);
  });

  it('shows every dot neutral while disabled', () => {
    const filled = dots(true).filter(dot => dot.className.includes('bg-[var(--color-primary)]'));
    expect(filled).toHaveLength(0);
  });
});
