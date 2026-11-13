import { describe, it, expect, afterEach } from 'vitest';
import { cleanup, fireEvent, screen } from '@testing-library/svelte';
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

  it('renders each sortable column header as a button inside a th with aria-sort', () => {
    renderTyped(DatabaseTableBreakdown, { props: { tables } });

    for (const field of ['name', 'row_count', 'size_bytes']) {
      const button = screen.getByTestId(`sort-${field}`);
      expect(button.tagName).toBe('BUTTON');
      expect(button.closest('th')).toHaveAttribute('aria-sort');
    }
    expect(header('size_bytes')).toHaveAttribute('aria-sort', 'descending');
    expect(header('name')).toHaveAttribute('aria-sort', 'none');
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

  it('adds an engine column header only when showEngine is set', () => {
    renderTyped(DatabaseTableBreakdown, { props: { tables, showEngine: true } });

    expect(screen.getByTestId('sort-engine').tagName).toBe('BUTTON');
  });
});
