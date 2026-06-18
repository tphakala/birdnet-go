import { describe, it, expect, afterEach } from 'vitest';
import { render, cleanup } from '@testing-library/svelte';
import Sparkline from './Sparkline.svelte';

describe('Sparkline', () => {
  afterEach(() => cleanup());

  it('is not aria-hidden by default', () => {
    const { container } = render(Sparkline, { props: { data: [1, 2, 3] } });
    const svg = container.querySelector('svg');
    expect(svg?.getAttribute('aria-hidden')).toBeNull();
  });

  it('marks the svg aria-hidden when decorative', () => {
    const { container } = render(Sparkline, { props: { data: [1, 2, 3], decorative: true } });
    const svg = container.querySelector('svg');
    expect(svg?.getAttribute('aria-hidden')).toBe('true');
  });

  it('shows the empty placeholder only when there is no data', () => {
    const { container } = render(Sparkline, {
      props: { data: [], emptyLabel: 'No data yet' },
    });
    expect(container.textContent).toContain('No data yet');
  });

  it('does not show the placeholder when data is present', () => {
    const { container } = render(Sparkline, {
      props: { data: [1, 2, 3], emptyLabel: 'No data yet' },
    });
    expect(container.textContent ?? '').not.toContain('No data yet');
  });
});
