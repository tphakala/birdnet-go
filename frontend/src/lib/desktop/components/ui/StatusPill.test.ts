import { describe, it, expect } from 'vitest';
import { renderTyped, screen } from '../../../../test/render-helpers';
import StatusPill, { type StatusVariant } from './StatusPill.svelte';

/** The pill element around a label, narrowed so a markup change fails here and not later. */
function pillOf(label: string): HTMLElement {
  const pill = screen.getByText(label).closest<HTMLElement>('span.rounded-full.border');
  if (!pill) throw new Error(`no pill around "${label}"`);
  return pill;
}

// Text uses the text token; the tint, border and dot keep the fill token.
const VARIANT_TOKENS: ReadonlyArray<[StatusVariant, string, string]> = [
  ['success', '--text-success', '--color-success'],
  ['warning', '--text-warning', '--color-warning'],
  ['info', '--text-info', '--color-info'],
  ['error', '--text-error', '--color-error'],
];

describe('StatusPill', () => {
  it.each(VARIANT_TOKENS)(
    '%s colours its text with %s and keeps %s for tint, border and dot',
    (variant, text, fill) => {
      renderTyped(StatusPill, { props: { variant, label: `pill-${variant}` } });

      const style = pillOf(`pill-${variant}`).getAttribute('style') ?? '';
      expect(style).toContain(`color: var(${text})`);
      expect(style).toContain(`background-color: color-mix(in srgb, var(${fill}) 15%`);
      expect(style).toContain(`border-color: color-mix(in srgb, var(${fill}) 30%`);

      const dot = pillOf(`pill-${variant}`).querySelector('[aria-hidden="true"]');
      expect(dot?.getAttribute('style')).toBe(`background-color: var(${fill});`);
    }
  );

  it('keeps the neutral pill on the base-content mix, with no text token', () => {
    renderTyped(StatusPill, { props: { label: 'pill-neutral' } });

    const style = pillOf('pill-neutral').getAttribute('style') ?? '';
    expect(style).toContain('color: color-mix(in srgb, var(--color-base-content) 70%');
    expect(style).not.toContain('--text-');
  });
});
