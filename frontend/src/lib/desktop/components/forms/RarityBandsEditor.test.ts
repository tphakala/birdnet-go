import { describe, it, expect, vi } from 'vitest';
import { screen, fireEvent } from '@testing-library/svelte';
import { renderTyped } from '../../../../test/render-helpers';
import RarityBandsEditor from './RarityBandsEditor.svelte';
import type { RarityBand } from '$lib/stores/settings';

const bands: RarityBand[] = [
  { maxOccurrence: 0.1, minDetections: 3 },
  { maxOccurrence: 0.9, minDetections: 2 },
];

function renderEditor(overrides: { bands?: RarityBand[]; disabled?: boolean } = {}) {
  const onUpdate = vi.fn<(bands: RarityBand[]) => void>();
  renderTyped(RarityBandsEditor, {
    props: {
      id: 'rarity-bands',
      bands: overrides.bands ?? bands,
      onUpdate,
      disabled: overrides.disabled ?? false,
    },
  });
  return { onUpdate };
}

describe('RarityBandsEditor', () => {
  it('renders one row per band with its values', () => {
    renderEditor();

    const occurrenceInputs = screen.getAllByLabelText(
      'components.forms.rarityBands.maxOccurrence.label'
    );
    const detectionInputs = screen.getAllByLabelText(
      'components.forms.rarityBands.minDetections.label'
    );
    expect(occurrenceInputs).toHaveLength(2);
    expect(occurrenceInputs[0]).toHaveValue(0.1);
    expect(detectionInputs[1]).toHaveValue(2);
  });

  it('appends a band when add is clicked', async () => {
    const { onUpdate } = renderEditor();

    await fireEvent.click(screen.getByRole('button', { name: /addBand/ }));

    expect(onUpdate).toHaveBeenCalledWith([...bands, { maxOccurrence: 0.5, minDetections: 2 }]);
  });

  it('removes the chosen band', async () => {
    const { onUpdate } = renderEditor();

    // The mocked t() returns the bare key, so both remove buttons share a name.
    const removeButtons = screen.getAllByRole('button', {
      name: 'components.forms.rarityBands.removeBand',
    });
    await fireEvent.click(removeButtons[0]);

    expect(onUpdate).toHaveBeenCalledWith([bands[1]]);
  });

  it('updates the edited field of the edited band only', async () => {
    const { onUpdate } = renderEditor();

    const detectionInputs = screen.getAllByLabelText(
      'components.forms.rarityBands.minDetections.label'
    );
    await fireEvent.input(detectionInputs[0], { target: { value: '5' } });
    await fireEvent.change(detectionInputs[0], { target: { value: '5' } });

    expect(onUpdate).toHaveBeenLastCalledWith([{ maxOccurrence: 0.1, minDetections: 5 }, bands[1]]);
  });

  it('keeps the last band and says why', async () => {
    const { onUpdate } = renderEditor({ bands: [bands[0]] });

    const remove = screen.getByRole('button', {
      name: 'components.forms.rarityBands.removeBand',
    });
    expect(remove).toHaveAttribute('aria-disabled', 'true');
    expect(screen.getByText('components.forms.rarityBands.lastBandRequired')).toBeInTheDocument();

    await fireEvent.click(remove);
    expect(onUpdate).not.toHaveBeenCalled();
  });

  it('explains an empty band list', () => {
    renderEditor({ bands: [] });

    expect(screen.getByText('components.forms.rarityBands.emptyState')).toBeInTheDocument();
  });

  it('blocks adding past the band limit and says why', async () => {
    const full = Array.from({ length: 10 }, (_, i) => ({
      maxOccurrence: (i + 1) / 10,
      minDetections: 2,
    }));
    const { onUpdate } = renderEditor({ bands: full });

    const add = screen.getByRole('button', { name: /addBand/ });
    expect(add).toHaveAttribute('aria-disabled', 'true');
    expect(screen.getByText('components.forms.rarityBands.maxBandsReached')).toBeInTheDocument();

    await fireEvent.click(add);
    expect(onUpdate).not.toHaveBeenCalled();
  });
});
