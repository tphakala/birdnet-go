import { render, screen, fireEvent, cleanup } from '@testing-library/svelte';
import { describe, it, expect, afterEach } from 'vitest';
import SpeciesSelector from './SpeciesSelector.svelte';
import { createSpeciesId } from '$lib/types/species';

afterEach(() => cleanup());

describe('SpeciesSelector chip dropdown', () => {
  it('marks a selected option as checked', async () => {
    render(SpeciesSelector, {
      props: {
        species: [
          {
            id: createSpeciesId('sp1'),
            commonName: 'Robin',
            scientificName: 'Turdus migratorius',
          },
        ],
        selected: ['sp1'],
        variant: 'chip',
      },
    });

    // The dropdown options only render once the combobox is expanded.
    const input = screen.getByRole('combobox');
    await fireEvent.focus(input);

    const option = screen.getByRole('option', { name: /Robin/ });
    expect(option).toHaveAttribute('aria-selected', 'true');
    expect(option.querySelector('[data-checked="true"]')).not.toBeNull();
  });
});
