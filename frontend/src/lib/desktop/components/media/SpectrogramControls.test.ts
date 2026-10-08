import { describe, it, expect } from 'vitest';
import { createComponentTestFactory, screen } from '../../../../test/render-helpers';
import SpectrogramControls from './SpectrogramControls.svelte';

describe('SpectrogramControls accessibility', () => {
  const controlsTest = createComponentTestFactory(SpectrogramControls);

  it('names the color map dropdown independently of the selected color map', () => {
    controlsTest.render({ props: { frequencyRange: [0, 12000], colorMap: 'viridis' } });

    const trigger = screen.getByRole<HTMLButtonElement>('button', {
      name: 'spectrogram.controls.colorMap',
    });
    expect(trigger).toHaveAttribute('aria-haspopup', 'listbox');
    // The visible text is the control's label, not a copy of it in aria-label
    expect(trigger).not.toHaveAttribute('aria-label');
    expect(trigger.labels).toHaveLength(1);
    expect(trigger.labels[0]).toHaveTextContent('spectrogram.controls.colorMap');
    expect(trigger).toHaveTextContent('spectrogram.colorMaps.viridis');
  });
});
