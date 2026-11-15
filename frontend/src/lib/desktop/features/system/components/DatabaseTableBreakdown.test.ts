import { describe, it, expect, afterEach } from 'vitest';
import { cleanup, fireEvent, screen } from '@testing-library/svelte';
import { t } from '$lib/i18n';
import { renderTyped } from '../../../../../test/render-helpers';
import type { TableStats } from '$lib/types/database';
import DatabaseTableBreakdown from './DatabaseTableBreakdown.svelte';

const tables: TableStats[] = [
  { name: 'beta', row_count: 10, size_bytes: 3000, usage_pct: 30, engine: '' },
  { name: 'alpha', row_count: 30, size_bytes: 1000, usage_pct: 10, engine: '' },
  { name: 'gamma', row_count: 20, size_bytes: 6000, usage_pct: 60, engine: '' },
];

const rowNames = () =>
  screen
    .getAllByRole('row')
    .slice(1)
    .map(row => row.querySelector('td')?.textContent.trim());

const header = (field: string) => screen.getByTestId(`sort-${field}`).closest('th');

describe('DatabaseTableBreakdown sorting', () => {
  afterEach(cleanup);

  it('renders each sortable column header as a button inside a th and marks only the active one with aria-sort', () => {
    renderTyped(DatabaseTableBreakdown, { props: { tables } });

    for (const field of ['name', 'row_count', 'size_bytes']) {
      const button = screen.getByTestId(`sort-${field}`);
      expect(button.tagName).toBe('BUTTON');
      expect(button.closest('th')).not.toBeNull();
    }
    expect(header('size_bytes')).toHaveAttribute('aria-sort', 'descending');
    expect(header('name')).not.toHaveAttribute('aria-sort');
    expect(screen.queryByTestId('sort-engine')).not.toBeInTheDocument();
  });

  it('sorts a numeric column descending first and then toggles', async () => {
    renderTyped(DatabaseTableBreakdown, { props: { tables } });

    await fireEvent.click(screen.getByTestId('sort-row_count'));
    expect(rowNames()).toEqual(['alpha', 'gamma', 'beta']);
    expect(header('row_count')).toHaveAttribute('aria-sort', 'descending');

    await fireEvent.click(screen.getByTestId('sort-row_count'));
    expect(rowNames()).toEqual(['beta', 'gamma', 'alpha']);
    expect(header('row_count')).toHaveAttribute('aria-sort', 'ascending');
  });

  it('sorts the name column ascending first', async () => {
    renderTyped(DatabaseTableBreakdown, { props: { tables } });

    await fireEvent.click(screen.getByTestId('sort-name'));

    expect(rowNames()).toEqual(['alpha', 'beta', 'gamma']);
    expect(header('name')).toHaveAttribute('aria-sort', 'ascending');
  });

  it('announces the column and direction once after a sort', async () => {
    renderTyped(DatabaseTableBreakdown, { props: { tables } });
    const tableColumnLabel = t('system.database.dashboard.tables.table');
    const status = screen.getByRole('status');
    expect(status.textContent).toBe('');

    await fireEvent.click(screen.getByTestId('sort-name'));
    expect(status).toHaveTextContent(
      t('dataDisplay.table.sortedAscending', { column: tableColumnLabel })
    );

    await fireEvent.click(screen.getByTestId('sort-name'));
    expect(status).toHaveTextContent(
      t('dataDisplay.table.sortedDescending', { column: tableColumnLabel })
    );
  });

  it('names the table by its visible title', () => {
    renderTyped(DatabaseTableBreakdown, { props: { tables } });

    expect(
      screen.getByRole('table', { name: t('system.database.dashboard.tables.title') })
    ).toBeInTheDocument();
  });

  it('adds an engine column header when showEngine is set', () => {
    renderTyped(DatabaseTableBreakdown, { props: { tables, showEngine: true } });

    expect(screen.getByTestId('sort-engine').tagName).toBe('BUTTON');
  });

  it('sorts the engine column ascending first', async () => {
    const mysqlTables: TableStats[] = [
      { name: 'one', row_count: 1, size_bytes: 100, usage_pct: 10, engine: 'MyISAM' },
      { name: 'two', row_count: 2, size_bytes: 200, usage_pct: 20, engine: 'Aria' },
      { name: 'three', row_count: 3, size_bytes: 300, usage_pct: 30, engine: 'InnoDB' },
    ];
    renderTyped(DatabaseTableBreakdown, { props: { tables: mysqlTables, showEngine: true } });

    await fireEvent.click(screen.getByTestId('sort-engine'));

    expect(rowNames()).toEqual(['two', 'three', 'one']);
    expect(header('engine')).toHaveAttribute('aria-sort', 'ascending');
  });
});
