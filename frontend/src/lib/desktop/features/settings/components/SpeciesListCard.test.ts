/**
 * Regression tests for SpeciesListCard's value/display split.
 *
 * The include/exclude lists this card feeds are persisted server-wide config, so
 * selecting a localized prediction MUST emit the canonical value, never the
 * localized label. These tests guard that invariant.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import { CirclePlus } from '@lucide/svelte';
import SpeciesListCard from './SpeciesListCard.svelte';

// Finnish labels for canonical English/scientific values.
const FI = new Map<string, string>([
  ['American Robin', 'Punarinta'],
  ['Blue Jay', 'Sinitöyhtönärhi'],
]);
const localizeLabel = (value: string): string => FI.get(value) ?? value;

function renderCard(overrides: Record<string, unknown> = {}) {
  const onAdd = vi.fn();
  render(SpeciesListCard, {
    props: {
      title: 'Always Include',
      species: [],
      icon: CirclePlus,
      predictions: ['American Robin', 'Blue Jay'],
      inputValue: 'puna',
      inputLabel: 'Add species',
      inputPlaceholder: 'Type a species name',
      emptyMessage: 'No species',
      localizeLabel,
      onAdd,
      onRemove: vi.fn(),
      onInput: vi.fn(),
      ...overrides,
    },
  });
  return { onAdd };
}

describe('SpeciesListCard value/display split', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders the localized label in the dropdown but emits the canonical value', async () => {
    const { onAdd } = renderCard();

    const input = screen.getByRole('combobox');
    await fireEvent.focus(input);

    // The dropdown shows the localized label, not the canonical value.
    const option = await screen.findByText('Punarinta');
    expect(screen.queryByText('American Robin')).not.toBeInTheDocument();

    await fireEvent.mouseDown(option);

    // The persisted value is canonical.
    expect(onAdd).toHaveBeenCalledWith('American Robin');
    expect(onAdd).not.toHaveBeenCalledWith('Punarinta');
  });

  it('maps a typed localized name to the canonical value on Add', async () => {
    const { onAdd } = renderCard({ inputValue: 'Punarinta' });

    const addButton = screen.getByRole('button', { name: 'Add species' });
    await fireEvent.click(addButton);

    // handleAdd emits synchronously (no deferred add), so assert directly.
    expect(onAdd).toHaveBeenCalledWith('American Robin');
    expect(onAdd).not.toHaveBeenCalledWith('Punarinta');
  });

  it('keeps unmatched free text as-is', async () => {
    const { onAdd } = renderCard({ inputValue: 'Unlisted Bird' });

    const addButton = screen.getByRole('button', { name: 'Add species' });
    await fireEvent.click(addButton);

    expect(onAdd).toHaveBeenCalledWith('Unlisted Bird');
  });
});
