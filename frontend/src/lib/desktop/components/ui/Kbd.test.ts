import { describe, it, expect } from 'vitest';
import { renderTyped, screen } from '../../../../test/render-helpers';
import Kbd from './Kbd.svelte';

describe('Kbd', () => {
  it('hides the caps from assistive technology and reads the label instead', () => {
    renderTyped(Kbd, {
      props: {
        keys: ['⇧', '←'],
        label: 'Shift plus Left Arrow',
        className: 'extra-class',
        'data-testid': 'kbd-root',
      },
    });

    const root = screen.getByTestId('kbd-root');
    expect(root).toHaveClass('extra-class');

    const caps = root.querySelectorAll('kbd');
    expect([...caps].map(cap => cap.textContent)).toEqual(['⇧', '←']);
    caps.forEach(cap => expect(cap).toHaveAttribute('aria-hidden', 'true'));

    // Everything outside the hidden caps is the spoken label, and it is visually hidden
    const exposed = root.querySelectorAll(':scope > :not([aria-hidden="true"])');
    expect([...exposed].map(el => el.textContent)).toEqual(['Shift plus Left Arrow']);
    expect(exposed[0]).toHaveClass('sr-only');
  });
});
