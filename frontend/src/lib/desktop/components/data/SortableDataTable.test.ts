import { describe, it, expect } from 'vitest';
import { createRawSnippet } from 'svelte';
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

function htmlSnippet(html: string) {
  return createRawSnippet(() => ({ render: () => html }));
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

  it('applies the column width to sortable header cells', () => {
    const widthColumns: Column<Row>[] = [
      { key: 'name', header: 'Name', sortable: true, sortValue: r => r.name, width: '7rem' },
      { key: 'score', header: 'Score', sortable: true, sortValue: r => r.score },
    ];
    const { container } = renderTable({ columns: widthColumns });
    const nameHeader = container.querySelector('th');
    expect(nameHeader?.getAttribute('style')).toContain('width: 7rem');
  });

  it('shows a labelled loading state and no table while loading', () => {
    const view = renderTable({ loading: true });
    expect(screen.getByRole('status')).toHaveTextContent('common.loading');
    expect(document.querySelector('table')).not.toBeInTheDocument();
    view.unmount();

    renderTable({ loading: true, loadingLabel: 'Loading things' });
    expect(screen.getByRole('status')).toHaveTextContent('Loading things');
  });

  it('keeps one status region across a loading change', async () => {
    const view = renderTable({ loading: false });
    const region = screen.getByRole('status');
    expect(region).toBeEmptyDOMElement();

    await view.rerender({ loading: true, loadingLabel: 'Loading things' });
    expect(screen.getByRole('status')).toBe(region);
    expect(region).toHaveTextContent('Loading things');

    await view.rerender({ loading: false });
    expect(screen.getByRole('status')).toBe(region);
    expect(region).toBeEmptyDOMElement();
  });

  it('uses the count prop for the badge when provided, overriding the row count', () => {
    const { container } = renderTable({ count: 99 });
    const badge = container.querySelector('span.rounded-full');
    expect((badge?.textContent ?? '').trim()).toBe('99');
  });

  it('sorts a sortable column that has no sortValue by its raw value', () => {
    const rawColumns: Column<Row>[] = [
      { key: 'name', header: 'Name', sortable: true },
      { key: 'score', header: 'Score', sortable: true, defaultDirection: 'desc' },
    ];
    const rows: Row[] = [
      { id: 1, name: 'nine', score: 9 },
      { id: 2, name: 'hundred', score: 100 },
      { id: 3, name: 'ten', score: 10 },
    ];
    renderTable({ columns: rawColumns, data: rows, defaultSortKey: 'score' });
    // Numerically 100, 10, 9; compared as strings it would be 9, 100, 10.
    expect(rowNames()).toEqual(['hundred', 'ten', 'nine']);
  });

  it('sorts a raw numeric field holding NaN consistently', () => {
    const rawColumns: Column<Row>[] = [
      { key: 'name', header: 'Name' },
      { key: 'score', header: 'Score', sortable: true },
    ];
    const rows: Row[] = [
      { id: 1, name: 'five', score: 5 },
      { id: 2, name: 'nan', score: Number.NaN },
      { id: 3, name: 'one', score: 1 },
    ];
    renderTable({ columns: rawColumns, data: rows, defaultSortKey: 'score' });
    // NaN sorts as an empty value, ahead of every number, instead of scrambling the rest.
    expect(rowNames()).toEqual(['nan', 'one', 'five']);
  });

  it('sorts empty and invalid raw values first ascending and last descending', async () => {
    const user = userEvent.setup();
    interface Event {
      id: number;
      label: string;
      at: Date | null;
    }
    const events: Event[] = [
      { id: 1, label: 'Mon', at: new Date(2026, 10, 9) },
      { id: 2, label: 'Never', at: null },
      { id: 3, label: 'Broken', at: new Date('not a date') },
      { id: 4, label: 'Sun', at: new Date(2026, 10, 8) },
    ];
    const eventColumns: Column<Event>[] = [
      { key: 'label', header: 'Label' },
      { key: 'at', header: 'At', sortable: true },
    ];
    renderTable({ columns: eventColumns, data: events, defaultSortKey: 'at' });
    // null and an invalid Date both become '', which orders below every time value.
    expect(rowNames().slice(2)).toEqual(['Sun', 'Mon']);
    expect(rowNames().slice(0, 2).sort()).toEqual(['Broken', 'Never']);

    await user.click(screen.getByTestId('sort-at'));
    expect(rowNames().slice(0, 2)).toEqual(['Mon', 'Sun']);
    expect(rowNames().slice(2).sort()).toEqual(['Broken', 'Never']);
  });

  it('sorts a Date field without sortValue chronologically', () => {
    interface Event {
      id: number;
      label: string;
      at: Date;
    }
    const events: Event[] = [
      { id: 1, label: 'Mon', at: new Date(2026, 10, 9) },
      { id: 2, label: 'Sun', at: new Date(2026, 10, 8) },
      { id: 3, label: 'Tue', at: new Date(2026, 10, 10) },
    ];
    const eventColumns: Column<Event>[] = [
      { key: 'label', header: 'Label' },
      { key: 'at', header: 'At', sortable: true },
    ];
    renderTable({ columns: eventColumns, data: events, defaultSortKey: 'at' });
    // As strings these would order Mon, Sun, Tue (by weekday name).
    expect(rowNames()).toEqual(['Sun', 'Mon', 'Tue']);
  });

  it('bounds the body height when bodyMaxHeight is set and resizable is off', () => {
    const { container } = renderTable({ bodyMaxHeight: 'min(70vh, 40rem)' });
    const body = container.querySelector('table')?.parentElement;
    // jsdom loads no Tailwind CSS: check the cap value and the classes that use it.
    expect(body?.style.getPropertyValue('--table-body-max-height')).toBe('min(70vh, 40rem)');
    expect(body).toHaveClass('max-h-(--table-body-max-height)', 'overflow-y-auto');
  });

  it.each([
    ['an uncapped body', {}],
    ['a height-capped body', { bodyMaxHeight: '20rem' }],
    ['a resizable body', { resizable: true }],
  ])('makes %s without sortable columns keyboard focusable, named by the title', (_name, extra) => {
    const plainColumns: Column<Row>[] = [
      { key: 'name', header: 'Name' },
      { key: 'score', header: 'Score' },
    ];
    renderTable({ columns: plainColumns, title: 'Things', ...extra });
    const body = screen.getByRole('region', { name: 'Things' });
    expect(body).toHaveAttribute('tabindex', '0');
    expect(body).toContainElement(document.querySelector('table'));
  });

  it('draws the search box focus ring at full strength so it reaches 3:1', () => {
    renderTable({ title: 'Things', searchable: true, searchAccessor: (r: Row) => r.name });
    const search = screen.getByRole('textbox');
    const ringColours = [...search.classList].filter(c => c.startsWith('focus:ring-['));

    expect(search).toHaveClass('focus:ring-2');
    expect(ringColours).toEqual(['focus:ring-[var(--color-primary)]']);
  });

  it('keeps the capped body focusable without a title, as an unnamed scroll area', () => {
    const plainColumns: Column<Row>[] = [{ key: 'name', header: 'Name' }];
    const { container } = renderTable({ columns: plainColumns, title: '', bodyMaxHeight: '20rem' });
    const body = container.querySelector('table')?.parentElement;
    expect(body).toHaveAttribute('tabindex', '0');
    expect(body).not.toHaveAttribute('role');
    expect(body).not.toHaveAttribute('aria-label');
  });

  it('adds no extra tab stop when a sort button can already focus the capped body', () => {
    const { container } = renderTable({ title: 'Things', bodyMaxHeight: '20rem' });
    const body = container.querySelector('table')?.parentElement;
    expect(body).not.toHaveAttribute('tabindex');
    expect(screen.queryByRole('region')).not.toBeInTheDocument();
  });

  it('keeps the natural body height when bodyMaxHeight is not set', () => {
    const { container } = renderTable({});
    const body = container.querySelector('table')?.parentElement;
    expect(body?.style.getPropertyValue('--table-body-max-height')).toBe('');
    expect(body).not.toHaveClass('max-h-(--table-body-max-height)');
    expect(body).not.toHaveClass('overflow-y-auto');
  });

  it('ignores bodyMaxHeight when the body is resizable', () => {
    const { container } = renderTable({ resizable: true, bodyMaxHeight: 'min(70vh, 40rem)' });
    expect(screen.getByRole('separator')).toBeInTheDocument();
    const body = container.querySelector('table')?.parentElement;
    expect(body?.style.getPropertyValue('--table-body-max-height')).toBe('');
  });

  describe.each([
    { align: undefined, expected: 'text-left' },
    { align: 'left', expected: 'text-left' },
    { align: 'center', expected: 'text-center' },
    { align: 'right', expected: 'text-right' },
  ] as const)('column align $align', ({ align, expected }) => {
    it.each([true, false])('header (sortable %s) and cells share %s', sortable => {
      const alignedColumns: Column<Row>[] = [
        { key: 'name', header: 'Name' },
        { key: 'score', header: 'Score', sortable, sortValue: r => r.score, align },
      ];
      const { container } = renderTable({ columns: alignedColumns });
      const headers = container.querySelectorAll('thead th');
      expect(headers[1]).toHaveClass(expected);
      const cells = [...container.querySelectorAll('tbody tr')].map(tr => tr.children[1]);
      for (const cell of cells) {
        expect(cell).toHaveClass(expected);
      }
    });
  });

  it('right-aligned columns align header and cells', () => {
    const alignedColumns: Column<Row>[] = [
      { key: 'name', header: 'Name', sortable: true, sortValue: r => r.name },
      { key: 'score', header: 'Score', sortable: true, sortValue: r => r.score, align: 'right' },
    ];
    const { container } = renderTable({ columns: alignedColumns });
    const scoreHeader = screen.getByTestId('sort-score');
    expect(scoreHeader.closest('th')).toHaveClass('text-right');
    // The indicator leads, so the label's right edge meets the cell values' edge.
    expect(scoreHeader.firstElementChild?.getAttribute('aria-hidden')).toBe('true');
    const scoreCells = [...container.querySelectorAll('tbody tr')].map(tr => tr.children[1]);
    for (const cell of scoreCells) {
      expect(cell).toHaveClass('text-right', 'tabular-nums');
    }
  });

  describe('notice', () => {
    const notice = htmlSnippet('<div data-testid="notice">Load failed</div>');

    /** Whether the notice follows the header bar and precedes `body` in document order. */
    function noticeIsBetweenHeaderAnd(body: Element): boolean {
      const noticeEl = screen.getByTestId('notice');
      const heading = screen.getByRole('heading', { name: 'Things' });
      return (
        heading.compareDocumentPosition(noticeEl) === Node.DOCUMENT_POSITION_FOLLOWING &&
        noticeEl.compareDocumentPosition(body) === Node.DOCUMENT_POSITION_FOLLOWING
      );
    }

    it('renders between the header bar and the rows', () => {
      renderTable({ title: 'Things', notice });
      const table = document.querySelector('table');
      expect(table).not.toBeNull();
      expect(table && noticeIsBetweenHeaderAnd(table)).toBe(true);
    });

    it('renders between the header bar and the empty state', () => {
      renderTable({ title: 'Things', data: [], emptyTitle: 'Nothing here', notice });
      expect(noticeIsBetweenHeaderAnd(screen.getByText('Nothing here'))).toBe(true);
    });

    it('renders between the header bar and the loading indicator', () => {
      renderTable({ title: 'Things', loading: true, notice });
      expect(noticeIsBetweenHeaderAnd(screen.getByRole('status'))).toBe(true);
    });

    it('renders between the header bar and the no-results message', async () => {
      const user = userEvent.setup();
      renderTable({
        title: 'Things',
        notice,
        searchable: true,
        searchAccessor: (r: Row) => r.name,
        searchPlaceholder: 'Search',
        noResultsMessage: 'No matches',
      });
      await user.type(screen.getByPlaceholderText('Search'), 'zzz');
      expect(noticeIsBetweenHeaderAnd(screen.getByText('No matches'))).toBe(true);
    });
  });

  it('gives the notice wrapper its vertical padding through a has-[*] variant', () => {
    // A notice snippet whose content is all behind {#if} still renders its
    // wrapper (comment anchors, and whitespace between blocks); the wrapper must
    // take no space then. jsdom loads no Tailwind CSS, so this checks the class
    // that adds the vertical padding only when an element is present.
    renderTable({ notice: htmlSnippet('<span data-testid="notice"></span>') });
    const wrapper = screen.getByTestId('notice').parentElement;
    expect(wrapper).toHaveClass('has-[*]:py-3');
    expect(wrapper).not.toHaveClass('py-3');
  });

  describe('empty snippet', () => {
    const empty = htmlSnippet('<p>No backups yet</p>');

    it('replaces the default empty state', () => {
      renderTable({ data: [], emptyTitle: 'Nothing here', empty });
      expect(screen.getByText('No backups yet')).toBeInTheDocument();
      expect(screen.queryByText('Nothing here')).not.toBeInTheDocument();
    });

    it('is not shown while loading', () => {
      renderTable({ data: [], loading: true, empty });
      expect(screen.getByRole('status')).toBeInTheDocument();
      expect(screen.queryByText('No backups yet')).not.toBeInTheDocument();
    });

    it('is not shown when a search matches no rows', async () => {
      const user = userEvent.setup();
      renderTable({
        empty,
        searchable: true,
        searchAccessor: (r: Row) => r.name,
        searchPlaceholder: 'Search',
        noResultsMessage: 'No matches',
      });
      await user.type(screen.getByPlaceholderText('Search'), 'zzz');
      expect(screen.getByText('No matches')).toBeInTheDocument();
      expect(screen.queryByText('No backups yet')).not.toBeInTheDocument();
    });
  });
});
