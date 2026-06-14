import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import SpeciesConfigTable from './SpeciesConfigTable.svelte';
import { normalizeForLookup } from '$lib/utils/speciesNames';
import type { SpeciesConfig } from '$lib/stores/settings';

// i18n keys the mock passes through verbatim (not in the shared translation map).
const KEY_INTERVAL_DEFAULT = 'settings.species.customConfiguration.table.intervalDefault';
const KEY_NO_ACTIONS = 'settings.species.customConfiguration.table.noActions';
const KEY_CUSTOM_ACTION = 'settings.species.customConfiguration.badges.customAction';
const KEY_NO_RESULTS = 'settings.species.customConfiguration.table.noResults';
const KEY_EMPTY_TITLE = 'settings.species.customConfiguration.emptyState.title';

function makeConfigs(): Record<string, SpeciesConfig> {
  // Raven first so a default ascending sort by species must reorder them. Raven's
  // threshold is the HIGHER one so a threshold sort reorders differently from the
  // species sort (otherwise the threshold test would not be discriminating).
  return {
    Raven: { threshold: 0.9, interval: 0, actions: [] },
    Blackbird: {
      threshold: 0.5,
      interval: 300,
      actions: [{ type: 'ExecuteCommand', command: '/x', parameters: [], executeDefaults: true }],
    },
  };
}

const scientificNameMap = new Map<string, string>([
  [normalizeForLookup('Blackbird'), 'Turdus merula'],
  [normalizeForLookup('Raven'), 'Corvus corax'],
]);

function renderTable(props: Record<string, unknown> = {}) {
  return render(SpeciesConfigTable, {
    props: {
      configs: makeConfigs(),
      scientificNameMap,
      editingSpecies: null,
      disabled: false,
      onAdd: vi.fn(),
      onEdit: vi.fn(),
      onDelete: vi.fn(),
      ...props,
    },
  });
}

function speciesColumnOrder(): string[] {
  return [...document.querySelectorAll('tbody tr')].map(tr =>
    (tr.querySelector('td')?.textContent ?? '').trim()
  );
}

describe('SpeciesConfigTable', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders a row per configuration with display and scientific names', () => {
    renderTable();
    expect(screen.getByText('Blackbird')).toBeInTheDocument();
    expect(screen.getByText('Raven')).toBeInTheDocument();
    expect(screen.getByText('Turdus merula')).toBeInTheDocument();
    expect(screen.getByText('Corvus corax')).toBeInTheDocument();
    expect(document.querySelectorAll('tbody tr')).toHaveLength(2);
  });

  it('formats thresholds and intervals', () => {
    renderTable();
    expect(screen.getByText('0.50')).toBeInTheDocument();
    expect(screen.getByText('0.90')).toBeInTheDocument();
    expect(screen.getByText('300s')).toBeInTheDocument();
    // interval 0 renders the "Default" label
    expect(screen.getByText(KEY_INTERVAL_DEFAULT)).toBeInTheDocument();
  });

  it('shows an action badge only when actions exist', () => {
    renderTable();
    expect(screen.getByText(KEY_CUSTOM_ACTION)).toBeInTheDocument();
    expect(screen.getByText(KEY_NO_ACTIONS)).toBeInTheDocument();
  });

  it('renders a species missing from the scientific-name map with its raw key and no scientific cell', () => {
    renderTable({
      configs: { dog: { threshold: 0.3, interval: 15, actions: [] } },
      scientificNameMap: new Map<string, string>(),
    });
    const row = screen.getByText('dog').closest('tr') as HTMLElement;
    const cells = row.querySelectorAll('td');
    expect(cells[0].textContent.trim()).toBe('dog'); // display name falls back to raw key
    expect(cells[1].textContent.trim()).toBe(''); // no scientific name rendered
  });

  it('sorts by species ascending by default', () => {
    renderTable();
    expect(speciesColumnOrder()).toEqual(['Blackbird', 'Raven']);
  });

  it('sorts by threshold (desc first, asc on toggle) independently of the species order', async () => {
    const user = userEvent.setup();
    renderTable();
    // First click: descending. Raven (0.90) outranks Blackbird (0.50), which is the
    // REVERSE of the default species-asc order, so this proves threshold sorting.
    await user.click(screen.getByTestId('sort-threshold'));
    expect(speciesColumnOrder()).toEqual(['Raven', 'Blackbird']);
    // Second click: ascending.
    await user.click(screen.getByTestId('sort-threshold'));
    expect(speciesColumnOrder()).toEqual(['Blackbird', 'Raven']);
  });

  it('filters rows by the search accessor (matches scientific name)', async () => {
    const user = userEvent.setup();
    renderTable();
    await user.type(screen.getByRole('textbox'), 'corax');
    expect(speciesColumnOrder()).toEqual(['Raven']);
  });

  it('shows the no-results message when a search matches nothing', async () => {
    const user = userEvent.setup();
    renderTable();
    await user.type(screen.getByRole('textbox'), 'zzznomatch');
    expect(screen.getByText(KEY_NO_RESULTS)).toBeInTheDocument();
    expect(document.querySelector('tbody tr')).not.toBeInTheDocument();
  });

  it('calls onEdit and onDelete with the raw species key', async () => {
    const user = userEvent.setup();
    const onEdit = vi.fn();
    const onDelete = vi.fn();
    renderTable({ onEdit, onDelete });

    const row = screen.getByText('Blackbird').closest('tr') as HTMLElement;
    const buttons = within(row).getAllByRole('button');
    await user.click(buttons[0]); // edit
    expect(onEdit).toHaveBeenCalledWith('Blackbird');
    await user.click(buttons[1]); // delete
    expect(onDelete).toHaveBeenCalledWith('Blackbird');
  });

  it('calls onAdd from the Add Configuration button', async () => {
    const user = userEvent.setup();
    const onAdd = vi.fn();
    renderTable({ onAdd });
    await user.click(screen.getByTestId('add-configuration-button'));
    expect(onAdd).toHaveBeenCalledOnce();
  });

  it('disables editing other rows and hides Add while an editor is open', () => {
    renderTable({ editingSpecies: 'Blackbird' });

    // Add button is hidden while editing.
    expect(screen.queryByTestId('add-configuration-button')).not.toBeInTheDocument();

    // The other row's edit button is disabled with an explanatory label.
    const ravenRow = screen.getByText('Raven').closest('tr') as HTMLElement;
    const ravenEdit = within(ravenRow).getAllByRole('button')[0];
    expect(ravenEdit).toBeDisabled();
    expect(ravenEdit.getAttribute('aria-label')).toBe(
      'settings.species.customConfiguration.table.editDisabledReason'
    );

    // The edited row keeps its edit button enabled.
    const blackbirdRow = screen.getByText('Blackbird').closest('tr') as HTMLElement;
    const blackbirdEdit = within(blackbirdRow).getAllByRole('button')[0];
    expect(blackbirdEdit).not.toBeDisabled();
  });

  it('disables edit and delete with a busy reason while the page is loading or saving', () => {
    renderTable({ disabled: true });
    const row = screen.getByText('Blackbird').closest('tr') as HTMLElement;
    const buttons = within(row).getAllByRole('button');
    const busyReason = 'settings.species.customConfiguration.table.actionsDisabledBusy';
    for (const btn of buttons) {
      expect(btn).toBeDisabled();
      expect(btn.getAttribute('aria-label')).toBe(busyReason);
    }
  });

  it('renders the empty state (title) when there are no configurations', () => {
    renderTable({ configs: {} });
    expect(document.querySelector('tbody')).not.toBeInTheDocument();
    expect(screen.getByText(KEY_EMPTY_TITLE)).toBeInTheDocument();
    expect(screen.queryByTestId('add-configuration-button')).toBeInTheDocument();
  });
});
