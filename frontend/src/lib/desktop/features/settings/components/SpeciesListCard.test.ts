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
import { resolveCommonToScientificUnique } from '$lib/stores/speciesDictionary.svelte';
import { OPTION_HIGHLIGHT_OUTLINE_CLASS } from '$lib/desktop/components/forms/SelectDropdown.styles';

// Stub the visitor dictionary store. localizeScientific feeds localizeSpeciesName
// (list-row display); resolveCommonToScientificUnique is the stale-predictions
// fallback exercised by the race test below.
vi.mock('$lib/stores/speciesDictionary.svelte', () => ({
  localizeScientific: vi.fn(() => undefined),
  resolveCommonToScientificUnique: vi.fn(() => undefined),
}));

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

  it('resolves a typed localized name via the dictionary when predictions are stale', async () => {
    // Simulate the debounce race: the parent has not yet populated predictions, so
    // the typed localized name cannot match a prediction. The always-current
    // dictionary resolves it to a canonical scientific name (safe for include/exclude).
    vi.mocked(resolveCommonToScientificUnique).mockReturnValueOnce('Turdus migratorius');
    const { onAdd } = renderCard({ predictions: [], inputValue: 'Punarinta' });

    const addButton = screen.getByRole('button', { name: 'Add species' });
    await fireEvent.click(addButton);

    expect(resolveCommonToScientificUnique).toHaveBeenCalledWith('Punarinta');
    expect(onAdd).toHaveBeenCalledWith('Turdus migratorius');
    expect(onAdd).not.toHaveBeenCalledWith('Punarinta');
  });
});

describe('SpeciesListCard combobox keyboard highlight', () => {
  const crowPredictions = ['American Robin', 'American Crow', 'Blue Jay'];
  const threeAmericans = ['American Robin', 'American Crow', 'American Wren'];

  beforeEach(() => {
    vi.clearAllMocks();
    // jsdom does not implement scrollIntoView
    Element.prototype.scrollIntoView = vi.fn();
  });

  async function openList(overrides: Record<string, unknown> = {}) {
    renderCard({
      predictions: crowPredictions,
      inputValue: 'american',
      localizeLabel: undefined,
      ...overrides,
    });
    const input = screen.getByRole('combobox');
    await fireEvent.focus(input);
    await screen.findAllByRole('option');
    return input;
  }

  async function openListWithRerender(predictions: string[] = crowPredictions) {
    const { rerender } = render(SpeciesListCard, {
      props: {
        title: 'Always Include',
        species: [],
        icon: CirclePlus,
        predictions,
        inputValue: 'american',
        inputLabel: 'Add species',
        inputPlaceholder: '',
        emptyMessage: '',
        onAdd: vi.fn(),
        onRemove: vi.fn(),
        onInput: vi.fn(),
      },
    });
    const input = screen.getByRole('combobox');
    await fireEvent.focus(input);
    await screen.findAllByRole('option');
    return { input, rerender };
  }

  it('ArrowDown points aria-activedescendant at the first suggestion', async () => {
    const input = await openList();
    expect(input).not.toHaveAttribute('aria-activedescendant');

    await fireEvent.keyDown(input, { key: 'ArrowDown' });

    const [first] = screen.getAllByRole('option');
    expect(first.id).not.toBe('');
    expect(input).toHaveAttribute('aria-activedescendant', first.id);
    expect(first).toHaveAttribute('aria-selected', 'true');

    await fireEvent.keyDown(input, { key: 'ArrowDown' });
    const second = screen.getAllByRole('option')[1];
    expect(input).toHaveAttribute('aria-activedescendant', second.id);
    expect(first).toHaveAttribute('aria-selected', 'false');
  });

  it('ArrowUp from the first suggestion clears aria-activedescendant', async () => {
    const input = await openList();
    await fireEvent.keyDown(input, { key: 'ArrowDown' });
    expect(input).toHaveAttribute('aria-activedescendant');

    await fireEvent.keyDown(input, { key: 'ArrowUp' });

    expect(input).not.toHaveAttribute('aria-activedescendant');
  });

  it('Escape clears aria-activedescendant and is handled so a surrounding dialog keeps open', async () => {
    const input = await openList();
    await fireEvent.keyDown(input, { key: 'ArrowDown' });

    // fireEvent returns false when the event's default was prevented
    const notPrevented = await fireEvent.keyDown(input, { key: 'Escape' });

    expect(notPrevented).toBe(false);
    expect(input).not.toHaveAttribute('aria-activedescendant');
    expect(input).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryAllByRole('option')).toHaveLength(0);
  });

  it('leaves Escape alone when no suggestion list is open', async () => {
    renderCard({ predictions: [], inputValue: '' });
    const input = screen.getByRole('combobox');

    const notPrevented = await fireEvent.keyDown(input, { key: 'Escape' });

    expect(notPrevented).toBe(true);
  });

  it('gives each suggestion a unique id that the combobox controls', async () => {
    const input = await openList();
    const options = screen.getAllByRole('option');
    const ids = options.map(o => o.id);

    expect(new Set(ids).size).toBe(options.length);
    const listbox = screen.getByRole('listbox');
    expect(input).toHaveAttribute('aria-controls', listbox.id);
    for (const option of options) {
      expect(listbox).toContainElement(option);
    }
  });

  it('does not share listbox or option ids between two cards of the same color', async () => {
    const first = render(SpeciesListCard, {
      props: {
        title: 'Include',
        species: [],
        icon: CirclePlus,
        predictions: crowPredictions,
        inputValue: 'american',
        inputLabel: 'Include species',
        inputPlaceholder: '',
        emptyMessage: '',
        onAdd: vi.fn(),
        onRemove: vi.fn(),
        onInput: vi.fn(),
      },
    });
    render(SpeciesListCard, {
      props: {
        title: 'Exclude',
        species: [],
        icon: CirclePlus,
        predictions: crowPredictions,
        inputValue: 'american',
        inputLabel: 'Exclude species',
        inputPlaceholder: '',
        emptyMessage: '',
        onAdd: vi.fn(),
        onRemove: vi.fn(),
        onInput: vi.fn(),
      },
    });
    await fireEvent.focus(screen.getByRole('combobox', { name: 'Include species' }));
    await fireEvent.focus(screen.getByRole('combobox', { name: 'Exclude species' }));
    const lists = await screen.findAllByRole('listbox');
    expect(lists).toHaveLength(2);

    const ids = [...lists.map(l => l.id), ...screen.getAllByRole('option').map(o => o.id)];
    expect(new Set(ids).size).toBe(ids.length);

    // Each label and each aria-controls points at its own card's elements
    const include = screen.getByRole('combobox', { name: 'Include species' });
    const exclude = screen.getByRole('combobox', { name: 'Exclude species' });
    expect(include.id).not.toBe(exclude.id);
    expect(screen.getByText('Include species', { selector: 'label' })).toHaveAttribute(
      'for',
      include.id
    );
    expect(screen.getByText('Exclude species', { selector: 'label' })).toHaveAttribute(
      'for',
      exclude.id
    );
    for (const input of [include, exclude]) {
      const controlled = document.getElementById(input.getAttribute('aria-controls') ?? '');
      expect(controlled).not.toBeNull();
      expect(input.closest('.relative')).toContainElement(controlled);
    }
    first.unmount();
  });

  it('keeps the suggestions out of the Tab order', async () => {
    await openList();
    for (const option of screen.getAllByRole('option')) {
      expect(option.tagName).not.toBe('BUTTON');
      expect(option).not.toHaveAttribute('tabindex');
    }
  });

  it('typing clears the highlight even when the same suggestions remain', async () => {
    const { input } = await openListWithRerender(threeAmericans);
    await fireEvent.keyDown(input, { key: 'ArrowDown' });
    await fireEvent.keyDown(input, { key: 'ArrowDown' });
    expect(input).toHaveAttribute('aria-activedescendant', screen.getAllByRole('option')[1].id);

    await fireEvent.input(input, { target: { value: 'americ' } });

    expect(screen.getAllByRole('option')).toHaveLength(3);
    expect(input).not.toHaveAttribute('aria-activedescendant');
    for (const option of screen.getAllByRole('option')) {
      expect(option).toHaveAttribute('aria-selected', 'false');
    }
  });

  it('keeps the highlight on the last suggestion when ArrowDown goes past it', async () => {
    const input = await openList();
    for (let press = 0; press < 4; press++) {
      await fireEvent.keyDown(input, { key: 'ArrowDown' });
    }

    const options = screen.getAllByRole('option');
    expect(input).toHaveAttribute('aria-activedescendant', options[options.length - 1].id);
  });

  it('ArrowUp with nothing highlighted keeps nothing highlighted', async () => {
    const input = await openList();

    await fireEvent.keyDown(input, { key: 'ArrowUp' });

    expect(input).not.toHaveAttribute('aria-activedescendant');
    expect(Element.prototype.scrollIntoView).not.toHaveBeenCalled();

    // The index stays at -1, so the next ArrowDown reaches the first suggestion
    await fireEvent.keyDown(input, { key: 'ArrowDown' });
    expect(input).toHaveAttribute('aria-activedescendant', screen.getAllByRole('option')[0].id);
  });

  it('scrolls the highlighted suggestion into view on ArrowUp as well', async () => {
    const input = await openList();
    const scroll = vi.mocked(Element.prototype.scrollIntoView);
    await fireEvent.keyDown(input, { key: 'ArrowDown' });
    await fireEvent.keyDown(input, { key: 'ArrowDown' });
    scroll.mockClear();

    await fireEvent.keyDown(input, { key: 'ArrowUp' });

    expect(scroll).toHaveBeenCalledTimes(1);
    expect(scroll.mock.contexts[0]).toBe(screen.getAllByRole('option')[0]);
  });

  it('ArrowUp after the list shrank below the highlight lands on the last suggestion', async () => {
    const { input, rerender } = await openListWithRerender(threeAmericans);
    for (let press = 0; press < 3; press++) {
      await fireEvent.keyDown(input, { key: 'ArrowDown' });
    }
    await rerender({ predictions: ['American Robin'] });
    expect(input).not.toHaveAttribute('aria-activedescendant');

    await fireEvent.keyDown(input, { key: 'ArrowUp' });

    expect(input).toHaveAttribute('aria-activedescendant', screen.getByRole('option').id);
  });

  it('keeps the focus in the input when a suggestion is picked with the mouse', async () => {
    const { onAdd } = renderCard({
      predictions: crowPredictions,
      inputValue: 'american',
      localizeLabel: undefined,
    });
    const input = screen.getByRole('combobox');
    input.focus();
    await fireEvent.focus(input);
    const [option] = await screen.findAllByRole('option');

    // fireEvent returns false when the event's default was prevented, which is what keeps
    // the browser from moving the focus to the body
    const notPrevented = await fireEvent.mouseDown(option);

    expect(notPrevented).toBe(false);
    expect(onAdd).toHaveBeenCalledWith('American Robin');
    expect(input).toHaveFocus();
  });

  it('never points at a missing option when the predictions shrink without typing', async () => {
    const { input, rerender } = await openListWithRerender();
    await fireEvent.keyDown(input, { key: 'ArrowDown' });
    await fireEvent.keyDown(input, { key: 'ArrowDown' });
    expect(input).toHaveAttribute('aria-activedescendant', screen.getAllByRole('option')[1].id);

    await rerender({ predictions: ['American Robin'] });

    expect(screen.getAllByRole('option')).toHaveLength(1);
    expect(input).not.toHaveAttribute('aria-activedescendant');
  });

  it('marks the highlighted suggestion with the shared outline and base text color', async () => {
    const input = await openList();
    await fireEvent.keyDown(input, { key: 'ArrowDown' });

    const [highlighted, other] = screen.getAllByRole('option');

    for (const cls of OPTION_HIGHLIGHT_OUTLINE_CLASS.split(' ')) {
      expect(highlighted.className).toContain(cls);
      expect(other.className).not.toContain(cls);
    }
    expect(highlighted.className).toContain('text-[var(--color-base-content)]');
    expect(highlighted.className).not.toContain('--color-info');
  });

  it('scrolls the highlighted suggestion into view', async () => {
    const input = await openList();
    const scroll = vi.mocked(Element.prototype.scrollIntoView);

    await fireEvent.keyDown(input, { key: 'ArrowDown' });
    await fireEvent.keyDown(input, { key: 'ArrowDown' });

    expect(scroll).toHaveBeenLastCalledWith({ block: 'nearest' });
    expect(scroll.mock.contexts.at(-1)).toBe(screen.getAllByRole('option')[1]);
  });

  it('Enter adds the highlighted suggestion', async () => {
    const { onAdd } = renderCard({
      predictions: crowPredictions,
      inputValue: 'american',
      localizeLabel: undefined,
    });
    const input = screen.getByRole('combobox');
    await fireEvent.focus(input);
    await screen.findAllByRole('option');
    await fireEvent.keyDown(input, { key: 'ArrowDown' });
    await fireEvent.keyDown(input, { key: 'ArrowDown' });

    await fireEvent.keyDown(input, { key: 'Enter' });

    expect(onAdd).toHaveBeenCalledWith('American Crow');
  });
});
