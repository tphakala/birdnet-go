/**
 * Regression tests for SpeciesConfigEditor's field-level value/display split.
 *
 * The species field shows a localized label but the config-map KEY persisted on
 * save must stay canonical. The highest-risk path is the rename trap: opening an
 * existing entry and saving it must re-emit the original canonical key, never the
 * displayed label, even after a UI locale switch while the editor is open.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import SpeciesConfigEditor from './SpeciesConfigEditor.svelte';
import { resolveCommonToScientificUnique } from '$lib/stores/speciesDictionary.svelte';

// The dictionary store is the only dict dependency; stub the unique resolver so the
// free-typed-name path is deterministic.
vi.mock('$lib/stores/speciesDictionary.svelte', () => ({
  resolveCommonToScientificUnique: vi.fn(() => undefined),
}));

// Finnish labels for canonical (config-key / prediction) values.
const FI = new Map<string, string>([
  ['barn owl', 'Tornipöllö'],
  ['Barn Owl', 'Tornipöllö'],
  ['Tawny Owl', 'Lehtopöllö'],
]);
const fiLocalize = (value: string): string => FI.get(value) ?? value;

const SAVE_BUTTON = 'settings.species.customConfiguration.save';

interface RenderOpts {
  predictions?: string[];
  localizeLabel?: (_value: string) => string;
}

function renderEditor(opts: RenderOpts = {}) {
  const onSave = vi.fn();
  const result = render(SpeciesConfigEditor, {
    props: {
      species: 'barn owl', // existing canonical config key (lowercased)
      config: { threshold: 0.5, interval: 0, actions: [] },
      predictions: opts.predictions ?? ['Barn Owl', 'Tawny Owl'],
      localizeLabel: opts.localizeLabel ?? fiLocalize,
      onSave,
      onClose: vi.fn(),
      onInput: vi.fn(),
      onPredictionSelect: vi.fn(),
    },
  });
  return { onSave, ...result };
}

function savedSpecies(onSave: ReturnType<typeof vi.fn>): string {
  return onSave.mock.calls[0][0].species;
}

describe('SpeciesConfigEditor canonical-key persistence', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('shows the localized label in the field for an existing entry', () => {
    renderEditor();
    expect(screen.getByRole('combobox')).toHaveValue('Tornipöllö');
  });

  it('re-emits the canonical key on an unchanged save (no rename to the label)', async () => {
    const { onSave } = renderEditor();

    await fireEvent.click(screen.getByRole('button', { name: SAVE_BUTTON }));

    expect(onSave).toHaveBeenCalledTimes(1);
    expect(savedSpecies(onSave)).toBe('barn owl');
    expect(savedSpecies(onSave)).not.toBe('Tornipöllö');
  });

  it('re-emits the canonical key after a UI locale switch while open (rename-trap guard)', async () => {
    const { onSave, rerender } = renderEditor();

    // Simulate switching the UI locale: localizeLabel now returns German labels,
    // but the user has not touched the field.
    const DE = new Map<string, string>([['barn owl', 'Schleiereule']]);
    await rerender({
      species: 'barn owl',
      config: { threshold: 0.5, interval: 0, actions: [] },
      predictions: ['Barn Owl', 'Tawny Owl'],
      localizeLabel: (value: string) => DE.get(value) ?? value,
      onSave,
      onClose: vi.fn(),
      onInput: vi.fn(),
      onPredictionSelect: vi.fn(),
    });

    await fireEvent.click(screen.getByRole('button', { name: SAVE_BUTTON }));

    // Must still be the canonical key, not the stale Finnish label nor the German one.
    expect(savedSpecies(onSave)).toBe('barn owl');
  });

  it('emits the picked prediction canonical value, not its localized label', async () => {
    const { onSave } = renderEditor();

    // The portal dropdown renders the localized labels; pick "Tornipöllö" (Barn Owl).
    // The portal delegates selection on click.
    const option = await screen.findByText('Tornipöllö');
    await fireEvent.click(option);

    await fireEvent.click(screen.getByRole('button', { name: SAVE_BUTTON }));

    expect(savedSpecies(onSave)).toBe('Barn Owl');
  });

  it('resolves a free-typed localized common name to a unique scientific name', async () => {
    vi.mocked(resolveCommonToScientificUnique).mockReturnValue('Strix aluco');
    // No predictions, so the typed text cannot match a prediction and falls through
    // to the unique scientific-name resolution.
    const { onSave } = renderEditor({ predictions: [] });

    const input = screen.getByRole('combobox');
    await fireEvent.input(input, { target: { value: 'Lehtopöllö' } });

    await fireEvent.click(screen.getByRole('button', { name: SAVE_BUTTON }));

    expect(resolveCommonToScientificUnique).toHaveBeenCalledWith('Lehtopöllö');
    expect(savedSpecies(onSave)).toBe('Strix aluco');
  });

  it('keeps an unmatched free-typed name as-is (advanced raw entry)', async () => {
    vi.mocked(resolveCommonToScientificUnique).mockReturnValue(undefined);
    const { onSave } = renderEditor({ predictions: [] });

    const input = screen.getByRole('combobox');
    await fireEvent.input(input, { target: { value: 'Some Custom Name' } });

    await fireEvent.click(screen.getByRole('button', { name: SAVE_BUTTON }));

    expect(savedSpecies(onSave)).toBe('Some Custom Name');
  });
});
