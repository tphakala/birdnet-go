import { describe, it, expect } from 'vitest';
import { render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import SortableDataTable from './SortableDataTable.svelte';
import type { Column } from './DataTable.types';

interface Row {
  id: number;
  name: string;
  score: number;
}

const data: Row[] = [
  { id: 1, name: 'Charlie', score: 10 },
  { id: 2, name: 'alpha', score: 30 },
  { id: 3, name: 'Bravo', score: 20 },
];

const columns: Column<Row>[] = [
  { key: 'name', header: 'Name', sortable: true, sortValue: r => r.name, defaultDirection: 'asc' },
  {
    key: 'score',
    header: 'Score',
    sortable: true,
    sortValue: r => r.score,
    defaultDirection: 'desc',
  },
  { key: 'id', header: 'ID' },
];

function renderTable(props: Record<string, unknown>) {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any -- generic Svelte component needs a cast for render()
  return render(SortableDataTable as any, {
    props: { columns, data, resizable: false, ...props },
  });
}

function rowNames(): string[] {
  return [...document.querySelectorAll('tbody tr')].map(tr =>
    (tr.querySelector('td')?.textContent ?? '').trim()
  );
}

describe('SortableDataTable', () => {
  it('renders headers and rows', () => {
    renderTable({});
    expect(screen.getByText('Name')).toBeInTheDocument();
    expect(screen.getByText('Score')).toBeInTheDocument();
    expect(screen.getByText('ID')).toBeInTheDocument();
    expect(document.querySelectorAll('tbody tr')).toHaveLength(3);
  });

  it('applies the default sort (name ascending)', () => {
    renderTable({ defaultSortKey: 'name' });
    expect(rowNames()).toEqual(['alpha', 'Bravo', 'Charlie']);
  });

  it('toggles sort direction on header click', async () => {
    const user = userEvent.setup();
    renderTable({ defaultSortKey: 'name' });
    await user.click(screen.getByTestId('sort-name'));
    expect(rowNames()).toEqual(['Charlie', 'Bravo', 'alpha']);
  });

  it('uses the column default direction when switching columns', async () => {
    const user = userEvent.setup();
    renderTable({ defaultSortKey: 'name' });
    // score defaultDirection is 'desc' -> highest score first
    await user.click(screen.getByTestId('sort-score'));
    expect(rowNames()).toEqual(['alpha', 'Bravo', 'Charlie']); // 30, 20, 10
  });

  it('does not render a sort button for non-sortable columns', () => {
    renderTable({});
    expect(screen.queryByTestId('sort-id')).not.toBeInTheDocument();
  });

  it('filters rows via the search accessor', async () => {
    const user = userEvent.setup();
    renderTable({
      searchable: true,
      searchAccessor: (r: Row) => r.name,
      searchPlaceholder: 'Search',
    });
    await user.type(screen.getByPlaceholderText('Search'), 'alp');
    const names = rowNames();
    expect(names).toEqual(['alpha']);
  });

  it('shows the count badge for the filtered rows', async () => {
    const user = userEvent.setup();
    const { container } = renderTable({
      searchable: true,
      searchAccessor: (r: Row) => r.name,
      searchPlaceholder: 'Search',
    });
    await user.type(screen.getByPlaceholderText('Search'), 'alp');
    // Badge is the only element with the count style; assert it reads 1.
    const badge = container.querySelector('span.rounded-full');
    expect((badge?.textContent ?? '').trim()).toBe('1');
  });

  it('renders the empty state when there is no data', () => {
    renderTable({ data: [], emptyTitle: 'Nothing here', emptyDescription: 'Add something' });
    expect(screen.getByText('Nothing here')).toBeInTheDocument();
    expect(screen.getByText('Add something')).toBeInTheDocument();
    expect(document.querySelector('table')).not.toBeInTheDocument();
  });

  it('renders the no-results message when a search matches nothing', async () => {
    const user = userEvent.setup();
    renderTable({
      searchable: true,
      searchAccessor: (r: Row) => r.name,
      searchPlaceholder: 'Search',
      noResultsMessage: 'No matches',
    });
    await user.type(screen.getByPlaceholderText('Search'), 'zzz');
    expect(screen.getByText('No matches')).toBeInTheDocument();
    expect(document.querySelector('tbody tr')).not.toBeInTheDocument();
  });

  it('keys rows with keyFn and renders all of them', () => {
    renderTable({ keyFn: (r: Row) => r.id });
    const body = document.querySelector('tbody');
    expect(body && within(body as HTMLElement).getByText('alpha')).toBeTruthy();
    expect(document.querySelectorAll('tbody tr')).toHaveLength(3);
  });
});
