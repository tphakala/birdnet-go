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
    expect(bar).toHaveClass('block');
    expect(bar).not.toHaveClass('inline-block');
  });

  it('sits on a line of text when inline', () => {
    const { container } = renderTyped(Skeleton, { props: { inline: true, className: 'h-3 w-8' } });

    const bar = container.querySelector('[data-skeleton]');
    expect(bar).toHaveClass('inline-block', 'align-middle', 'h-3', 'w-8');
    expect(bar).not.toHaveClass('block');
  });

  it('keeps a class passed as class', () => {
    const { container } = renderTyped(Skeleton, { props: { class: 'h-4 w-20' } });

    expect(container.querySelector('[data-skeleton]')).toHaveClass('h-4', 'w-20', 'animate-pulse');
  });
});
