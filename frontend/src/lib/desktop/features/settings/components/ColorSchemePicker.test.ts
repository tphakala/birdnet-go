import { describe, it, expect, afterEach, beforeEach } from 'vitest';
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/svelte';
import { get } from 'svelte/store';
import { renderTyped } from '../../../../../test/render-helpers';
import { runAxeTest } from '$lib/utils/axe-utils';
import { scheme } from '$lib/stores/scheme';
import { logoStyle } from '$lib/stores/logoStyle';
import ColorSchemePicker from './ColorSchemePicker.svelte';

const BUSY_REASON = 'Saving...';
const SCHEME_LABEL_PREFIX = 'settings.appearance.scheme';
const LOGO_SWITCH_NAME = 'settings.appearance.logoGradient';

const radioNamed = (id: string) =>
  screen.getByRole('radio', { name: `${SCHEME_LABEL_PREFIX}${id}` });

describe('ColorSchemePicker', () => {
  beforeEach(() => {
    scheme.setScheme('blue');
  });

  afterEach(() => {
    cleanup();
    scheme.setScheme('blue');
  });

  it('makes only the checked scheme a Tab stop, also after the scheme changes', async () => {
    renderTyped(ColorSchemePicker, { props: {} });
    const radios = screen.getAllByRole('radio');
    expect(radios).toHaveLength(6);

    expect(radioNamed('Blue')).toHaveAttribute('tabindex', '0');
    expect(radios.filter(r => r.getAttribute('tabindex') === '0')).toHaveLength(1);

    scheme.setScheme('rose');

    await waitFor(() => expect(radioNamed('Rose')).toHaveAttribute('tabindex', '0'));
    expect(radioNamed('Rose')).toHaveAttribute('aria-checked', 'true');
    expect(radios.filter(r => r.getAttribute('tabindex') === '0')).toHaveLength(1);
  });

  it('selects the next scheme and moves focus to it on an arrow key', async () => {
    renderTyped(ColorSchemePicker, { props: {} });
    const blue = radioNamed('Blue');
    const forest = radioNamed('Forest');

    blue.focus();
    await fireEvent.keyDown(blue, { key: 'ArrowRight' });

    expect(get(scheme)).toBe('forest');
    await waitFor(() => expect(document.activeElement).toBe(forest));
    expect(forest).toHaveAttribute('tabindex', '0');
    expect(blue).toHaveAttribute('tabindex', '-1');
  });

  it('stays focusable and says why while Settings is busy', async () => {
    renderTyped(ColorSchemePicker, { props: { disabledReason: BUSY_REASON } });
    const reason = screen.getByText(BUSY_REASON);
    const radios = screen.getAllByRole('radio');

    for (const radio of radios) {
      expect(radio).toHaveAttribute('aria-disabled', 'true');
      expect(radio).not.toBeDisabled();
      expect(radio).toHaveAccessibleDescription(BUSY_REASON);
    }
    const logoSwitch = screen.getByRole('switch', { name: LOGO_SWITCH_NAME });
    expect(logoSwitch).toHaveAttribute('aria-disabled', 'true');
    expect(logoSwitch).not.toBeDisabled();
    expect(logoSwitch).toHaveAccessibleDescription(BUSY_REASON);
    expect(reason).toBeInTheDocument();

    const blue = radioNamed('Blue');
    const forest = radioNamed('Forest');
    blue.focus();
    await fireEvent.keyDown(blue, { key: 'ArrowRight' });
    await fireEvent.click(forest);

    expect(get(scheme)).toBe('blue');
    expect(document.activeElement).toBe(blue);
  });

  it('describes the blocked colour inputs by the reason', () => {
    scheme.setScheme('custom');
    renderTyped(ColorSchemePicker, { props: { disabledReason: BUSY_REASON } });

    for (const name of ['settings.appearance.customPrimary', 'settings.appearance.customAccent']) {
      const input = screen.getByLabelText(name);
      expect(input).toBeDisabled();
      expect(input).toHaveAccessibleDescription(BUSY_REASON);
    }
  });

  it('keeps the custom colours when a colour input reports a value while blocked', async () => {
    scheme.setScheme('custom');
    const before = get(scheme.customColors);
    renderTyped(ColorSchemePicker, { props: { disabledReason: BUSY_REASON } });

    // A native colour dialog opened before the block can still report a value.
    for (const name of ['settings.appearance.customPrimary', 'settings.appearance.customAccent']) {
      await fireEvent.input(screen.getByLabelText(name), { target: { value: '#000000' } });
    }

    expect(get(scheme.customColors)).toEqual(before);
  });

  it('is not blocked by an empty reason', () => {
    renderTyped(ColorSchemePicker, { props: { disabledReason: '' } });

    for (const radio of screen.getAllByRole('radio')) {
      expect(radio).not.toHaveAttribute('aria-disabled');
      expect(radio).not.toHaveAttribute('aria-describedby');
    }
  });

  it('leaves the logo style alone when the switch is clicked while blocked', async () => {
    logoStyle.setStyle('gradient');
    renderTyped(ColorSchemePicker, { props: { disabledReason: BUSY_REASON } });

    await fireEvent.click(screen.getByRole('switch', { name: LOGO_SWITCH_NAME }));

    expect(get(logoStyle)).toBe('gradient');
  });

  it('paints a fixed swatch in its light and dark primary and the custom swatch as a gradient', () => {
    const { container } = renderTyped(ColorSchemePicker, { props: {} });
    const swatchOf = (id: string) => {
      const swatch = container.querySelector<HTMLElement>(
        `[data-testid="color-scheme-${id}"] > div`
      );
      if (!swatch) throw new Error(`no swatch for ${id}`);
      return swatch;
    };

    const rose = swatchOf('rose');
    expect(rose).toHaveClass('bg-[var(--swatch)]', 'dark:bg-[var(--swatch-dark)]');
    expect(rose.style.getPropertyValue('--swatch')).toBe('#e11d48');
    expect(rose.style.getPropertyValue('--swatch-dark')).toBe('#fb7185');

    const custom = swatchOf('custom');
    expect(custom).not.toHaveClass('bg-[var(--swatch)]');
    expect(custom.style.background).toContain('linear-gradient');
  });

  it('keeps the swatch fills in forced colours because the colour is the information', () => {
    const { container } = renderTyped(ColorSchemePicker, { props: {} });

    for (const id of ['blue', 'forest', 'amber', 'violet', 'rose', 'custom']) {
      const swatch = container.querySelector(`[data-testid="color-scheme-${id}"] > div`);
      expect(swatch, `${id} swatch`).toHaveClass('forced-color-adjust-none');
    }
    // The selection ring is drawn in the system text colour, which stays visible on any contrast theme.
    const selected = container.querySelector('[data-testid="color-scheme-blue"] > div');
    expect(selected).toHaveClass('forced-colors:border-[CanvasText]');
  });

  it('has no Accessibility violations', async () => {
    const idle = renderTyped(ColorSchemePicker, { props: {} });
    expect((await runAxeTest(idle.container)).violations).toEqual([]);
    cleanup();

    const busy = renderTyped(ColorSchemePicker, { props: { disabledReason: BUSY_REASON } });
    expect((await runAxeTest(busy.container)).violations).toEqual([]);
  });
});
