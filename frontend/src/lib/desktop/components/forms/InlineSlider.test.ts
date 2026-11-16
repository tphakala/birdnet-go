/**
 * InlineSlider runs onUpdate on every move by default, and only on the
 * committed value (pointer release, key step) with updateOn="change", while
 * the shown value still follows the drag.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { cleanup, fireEvent, screen } from '@testing-library/svelte';
import { renderTyped } from '../../../../test/render-helpers';
import InlineSlider from './InlineSlider.svelte';

function renderSlider(updateOn?: 'input' | 'change') {
  const onUpdate = vi.fn();
  renderTyped(InlineSlider, {
    props: { label: 'Gain', value: 0, min: -40, max: 40, unit: ' dB', onUpdate, updateOn },
  });
  return { onUpdate, slider: screen.getByRole<HTMLInputElement>('slider') };
}

describe('InlineSlider', () => {
  afterEach(() => {
    cleanup();
  });

  it('updates on every move by default', async () => {
    const { onUpdate, slider } = renderSlider();

    await fireEvent.input(slider, { target: { value: '3' } });
    await fireEvent.input(slider, { target: { value: '5' } });
    expect(onUpdate.mock.calls).toEqual([[3], [5]]);
  });

  it('with updateOn="change" updates once on release while the label follows the drag', async () => {
    const { onUpdate, slider } = renderSlider('change');

    await fireEvent.input(slider, { target: { value: '3' } });
    await fireEvent.input(slider, { target: { value: '7' } });
    expect(onUpdate).not.toHaveBeenCalled();
    expect(screen.getByText('7 dB')).toBeInTheDocument();

    await fireEvent.change(slider, { target: { value: '7' } });
    expect(onUpdate).toHaveBeenCalledExactlyOnceWith(7);
  });
});
