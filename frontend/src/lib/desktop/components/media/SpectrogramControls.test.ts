import { describe, it, expect } from 'vitest';
import { createComponentTestFactory, screen } from '../../../../test/render-helpers';
import SpectrogramControls from './SpectrogramControls.svelte';

describe('SpectrogramControls accessibility', () => {
  const controlsTest = createComponentTestFactory(SpectrogramControls);

  it('names the color map dropdown independently of the selected color map', () => {
    controlsTest.render({ props: { frequencyRange: [0, 12000], colorMap: 'viridis' } });

    const trigger = screen.getByRole('button', { name: 'spectrogram.controls.colorMap' });
    expect(trigger).toHaveAttribute('aria-haspopup', 'listbox');
    expect(trigger).toHaveTextContent('spectrogram.colorMaps.viridis');
  });
});
