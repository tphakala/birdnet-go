import { describe, it, expect } from 'vitest';
import { createRawSnippet } from 'svelte';
import { render } from '@testing-library/svelte';
import ResizableContainer from './ResizableContainer.svelte';

const children = createRawSnippet(() => ({ render: () => '<p>content</p>' }));

function scroller(props: Record<string, unknown>): HTMLElement {
  const { container } = render(ResizableContainer, { props: { children, ...props } });
  const el = container.querySelector('p')?.parentElement;
  if (!el) throw new Error('scroller not rendered');
  return el;
}

describe('ResizableContainer scroll area', () => {
  it('is not a tab stop by default, so content that can hold focus is not doubled', () => {
    const el = scroller({ scrollLabel: 'Things' });

    expect(el).not.toHaveAttribute('tabindex');
    expect(el).not.toHaveAttribute('role');
    expect(el).not.toHaveAttribute('aria-label');
  });

  it('is an unnamed tab stop with scrollTabStop alone', () => {
    const el = scroller({ scrollTabStop: true });

    expect(el).toHaveAttribute('tabindex', '0');
    expect(el).not.toHaveAttribute('role');
    expect(el).not.toHaveAttribute('aria-label');
  });

  it('is a named region tab stop with scrollTabStop and scrollLabel', () => {
    const el = scroller({ scrollTabStop: true, scrollLabel: 'Things' });

    expect(el).toHaveAttribute('tabindex', '0');
    expect(el).toHaveAttribute('role', 'region');
    expect(el).toHaveAttribute('aria-label', 'Things');
  });
});
