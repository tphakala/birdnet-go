import { describe, it, expect } from 'vitest';
import { renderTyped } from '../../../../test/render-helpers';
import Skeleton from './Skeleton.svelte';

describe('Skeleton', () => {
  it('is a decorative bar that shrinks, stops pulsing under reduced motion and keeps aria-hidden', () => {
    const { container } = renderTyped(Skeleton, {
      props: {
        className: 'h-4 w-28 rounded-full',
        'aria-hidden': 'false',
        'data-testid': 'bar',
      },
    });

    const bar = container.querySelector('[data-skeleton]');
    expect(bar).not.toBeNull();
    // Every consumer inherits these: hidden from assistive technology, no overflow, no motion
    expect(bar).toHaveAttribute('aria-hidden', 'true');
    expect(bar).toHaveClass(
      'min-w-0',
      'max-w-full',
      'animate-pulse',
      'motion-reduce:animate-none',
      'h-4',
      'w-28',
      'rounded-full'
    );
    expect(bar).toHaveAttribute('data-testid', 'bar');
  });
});
