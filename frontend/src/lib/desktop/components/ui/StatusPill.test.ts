import { describe, it, expect } from 'vitest';
import { renderTyped, screen } from '../../../../test/render-helpers';
import StatusPill, { type StatusVariant } from './StatusPill.svelte';

/** The pill element around a label, narrowed so a markup change fails here and not later. */
function pillOf(label: string): HTMLElement {
  const pill = screen.getByText(label).closest<HTMLElement>('span.rounded-full.border');
  if (!pill) throw new Error(`no pill around "${label}"`);
  return pill;
}

/** The dot inside a pill. */
function dotOf(pill: HTMLElement): HTMLElement {
  const dot = pill.querySelector<HTMLElement>('span[aria-hidden="true"]');
  if (!dot) throw new Error('no dot in the pill');
  return dot;
}

const STATUS_VARIANTS: StatusVariant[] = ['success', 'warning', 'info', 'error'];

describe('StatusPill', () => {
  it.each(STATUS_VARIANTS)(
    '%s takes tint and text from the shared badge class and border and dot from --status-fill',
    variant => {
      renderTyped(StatusPill, { props: { variant, label: `pill-${variant}` } });

      const pill = pillOf(`pill-${variant}`);
      expect(pill).toHaveClass(`badge-status-${variant}`);
      expect(pill.className).toContain('var(--status-fill)');
      expect(pill).not.toHaveAttribute('style');

      const dot = dotOf(pill);
      expect(dot.className).toContain('var(--status-fill)');
      expect(dot).not.toHaveAttribute('style');
    }
  );

  it('colours the neutral pill and dot from base-content, without a status badge class', () => {
    renderTyped(StatusPill, { props: { label: 'pill-neutral' } });

    const pill = pillOf('pill-neutral');
    expect(pill.className).not.toContain('badge-status');
    expect(pill.className).not.toContain('--status-fill');
    expect(pill.className).toContain('var(--color-base-content)');
    expect(dotOf(pill).className).toContain('var(--color-base-content)');
  });

  it('falls back to the neutral look for an unknown variant', () => {
    renderTyped(StatusPill, {
      props: { variant: 'bogus' as StatusVariant, label: 'pill-unknown' },
    });

    const pill = pillOf('pill-unknown');
    expect(pill.className).not.toContain('badge-status');
    expect(pill.className).toContain('var(--color-base-content)');
    expect(dotOf(pill).className).toContain('var(--color-base-content)');
  });
});
