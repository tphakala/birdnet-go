import { describe, it, expect } from 'vitest';
import { renderTyped, screen } from '../../../../test/render-helpers';
import StatusPill, { type StatusVariant } from './StatusPill.svelte';

/** The pill element around a label, narrowed so a markup change fails here and not later. */
function pillOf(label: string): HTMLElement {
  const pill = screen.getByText(label).closest<HTMLElement>('span.status-pill');
  if (!pill) throw new Error(`no pill around "${label}"`);
  return pill;
}

const VARIANTS: StatusVariant[] = ['success', 'warning', 'info', 'error', 'neutral'];

describe('StatusPill', () => {
  it.each(VARIANTS)(
    '%s takes its colours from the shared badge class, not inline styles',
    variant => {
      renderTyped(StatusPill, { props: { variant, label: `pill-${variant}` } });

      const pill = pillOf(`pill-${variant}`);
      expect(pill).toHaveClass(`badge-status-${variant}`);
      expect(pill).not.toHaveAttribute('style');

      const dot = pill.querySelector('.status-pill-dot');
      expect(dot).not.toBeNull();
      expect(dot).not.toHaveAttribute('style');
    }
  );

  it('falls back to the neutral class for an unknown variant', () => {
    renderTyped(StatusPill, {
      props: { variant: 'bogus' as StatusVariant, label: 'pill-unknown' },
    });

    expect(pillOf('pill-unknown')).toHaveClass('badge-status-neutral');
  });
});
