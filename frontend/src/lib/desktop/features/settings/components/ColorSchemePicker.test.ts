import { describe, it, expect, afterEach, beforeEach } from 'vitest';
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/svelte';
import { get } from 'svelte/store';
import { renderTyped } from '../../../../../test/render-helpers';
import { scheme } from '$lib/stores/scheme';
import ColorSchemePicker from './ColorSchemePicker.svelte';

const radios = () => screen.getAllByRole('radio');

describe('ColorSchemePicker', () => {
  beforeEach(() => {
    scheme.setScheme('blue');
  });

  afterEach(() => {
    cleanup();
    scheme.setScheme('blue');
  });

  it('makes only the checked scheme a Tab stop', () => {
    renderTyped(ColorSchemePicker);

    const stops = radios().filter(r => r.getAttribute('tabindex') === '0');
    expect(stops).toHaveLength(1);
    expect(stops[0]).toHaveAttribute('aria-checked', 'true');
  });

  it('selects the next scheme and moves focus to it on ArrowRight', async () => {
    renderTyped(ColorSchemePicker);
    const [blue, forest] = radios();

    blue.focus();
    await fireEvent.keyDown(blue, { key: 'ArrowRight' });

    expect(get(scheme)).toBe('forest');
    await waitFor(() => expect(document.activeElement).toBe(forest));
    expect(forest).toHaveAttribute('tabindex', '0');
  });

  it('keeps the radios focusable but does not change the scheme when disabled', async () => {
    renderTyped(ColorSchemePicker, { props: { disabled: true } });
    const [blue, forest] = radios();

    expect(blue).not.toBeDisabled();
    expect(forest).toHaveAttribute('aria-disabled', 'true');
    blue.focus();
    await fireEvent.keyDown(blue, { key: 'ArrowRight' });
    await fireEvent.click(forest);

    expect(get(scheme)).toBe('blue');
    expect(document.activeElement).toBe(blue);
  });
});
