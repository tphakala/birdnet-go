import { describe, it, expect, vi } from 'vitest';
import { render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import SpeciesConfigTable from './SpeciesConfigTable.svelte';
import { normalizeForLookup } from '$lib/utils/speciesNames';
import type { SpeciesConfig } from '$lib/stores/settings';

// i18n keys the mock passes through verbatim (not in the shared translation map).
const KEY_INTERVAL_DEFAULT = 'settings.species.customConfiguration.table.intervalDefault';
const KEY_NO_ACTIONS = 'settings.species.customConfiguration.table.noActions';
const KEY_CUSTOM_ACTION = 'settings.species.customConfiguration.badges.customAction';

function makeConfigs(): Record<string, SpeciesConfig> {
  // Raven first so a default ascending sort by species must reorder them.
  return {
    Raven: { threshold: 0.39, interval: 0, actions: [] },
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
    expect(screen.getByText('0.39')).toBeInTheDocument();
    expect(screen.getByText('300s')).toBeInTheDocument();
    // interval 0 renders the "Default" label
    expect(screen.getByText(KEY_INTERVAL_DEFAULT)).toBeInTheDocument();
  });

  it('shows an action badge only when actions exist', () => {
    renderTable();
    expect(screen.getByText(KEY_CUSTOM_ACTION)).toBeInTheDocument();
    expect(screen.getByText(KEY_NO_ACTIONS)).toBeInTheDocument();
  });

  it('sorts by species ascending by default', () => {
    renderTable();
    expect(speciesColumnOrder()).toEqual(['Blackbird', 'Raven']);
  });

  it('sorts by threshold descending when its header is clicked', async () => {
    const user = userEvent.setup();
    renderTable();
    await user.click(screen.getByTestId('sort-threshold'));
    // 0.50 (Blackbird) before 0.39 (Raven)
    expect(speciesColumnOrder()).toEqual(['Blackbird', 'Raven']);
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

  it('renders the empty state when there are no configurations', () => {
    renderTable({ configs: {} });
    expect(document.querySelector('tbody')).not.toBeInTheDocument();
    expect(screen.queryByTestId('add-configuration-button')).toBeInTheDocument();
  });
});
