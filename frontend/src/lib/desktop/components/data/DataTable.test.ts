import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import DataTable from './DataTable.svelte';
import type { Column } from './DataTable.types';

interface TestData {
  id: number;
  name: string;
  age: number;
  email: string;
}

describe('DataTable', () => {
  const mockData: TestData[] = [
    { id: 1, name: 'John Doe', age: 30, email: 'john@example.com' },
    { id: 2, name: 'Jane Smith', age: 25, email: 'jane@example.com' },
    { id: 3, name: 'Bob Johnson', age: 35, email: 'bob@example.com' },
  ];

  const columns: Column<TestData>[] = [
    { key: 'id', header: 'ID', sortable: true },
    { key: 'name', header: 'Name', sortable: true },
    { key: 'age', header: 'Age', sortable: true, align: 'center' },
    { key: 'email', header: 'Email' },
  ];

  // Helper function to render DataTable with necessary type casting
  const renderDataTable = (props: Record<string, unknown>) => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    return render(DataTable as any, { props });
  };

  it('renders with data', () => {
    renderDataTable({
      columns,
      data: mockData,
    });

    // Check headers
    expect(screen.getByText('ID')).toBeInTheDocument();
    expect(screen.getByText('Name')).toBeInTheDocument();
    expect(screen.getByText('Age')).toBeInTheDocument();
    expect(screen.getByText('Email')).toBeInTheDocument();

    // Check data
    expect(screen.getByText('John Doe')).toBeInTheDocument();
    expect(screen.getByText('jane@example.com')).toBeInTheDocument();
    expect(screen.getByText('35')).toBeInTheDocument();
  });

  it('renders empty state when no data', () => {
    renderDataTable({
      columns,
      data: [],
    });

    expect(screen.getByText('No data available')).toBeInTheDocument();
  });

  it('renders custom empty message', () => {
    renderDataTable({
      columns,
      data: [],
      emptyMessage: 'No records found',
    });

    expect(screen.getByText('No records found')).toBeInTheDocument();
  });

  it('renders loading state', () => {
    renderDataTable({
      columns,
      data: [],
      loading: true,
    });

    expect(screen.queryByRole('table')).not.toBeInTheDocument();
    const spinner = document.querySelector('.loading-spinner');
    expect(spinner).toBeInTheDocument();
  });

  it('renders error state', () => {
    const errorMessage = 'Failed to load data';
    renderDataTable({
      columns,
      data: [],
      error: errorMessage,
    });

    expect(screen.getByText(errorMessage)).toBeInTheDocument();
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });

  it('handles sorting when column is sortable', async () => {
    const onSort = vi.fn();

    renderDataTable({
      columns,
      data: mockData,
      onSort,
    });

    // Find the Name button by its text content
    const buttons = screen.getAllByRole('button');
    const nameHeader = buttons.find(btn => btn.textContent?.includes('Name'));
    expect(nameHeader).toBeDefined();
    if (nameHeader) {
      await fireEvent.click(nameHeader);
    }

    expect(onSort).toHaveBeenCalledWith('name', 'asc');
  });

  it('cycles through sort directions', async () => {
    const onSort = vi.fn();

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { rerender } = render(DataTable as any, {
      props: {
        columns,
        data: mockData,
        onSort,
        sortColumn: 'name',
        sortDirection: 'asc',
      },
    });

    // Find the Name button by its text content
    const buttons = screen.getAllByRole('button');
    const nameHeader = buttons.find(btn => btn.textContent?.includes('Name'));
    expect(nameHeader).toBeDefined();

    // Click to sort desc
    if (nameHeader) {
      await fireEvent.click(nameHeader);
    }
    expect(onSort).toHaveBeenCalledWith('name', 'desc');

    // Update props to reflect new sort
    await rerender({
      columns,
      data: mockData,
      onSort,
      sortColumn: 'name',
      sortDirection: 'desc',
    });

    // Click to remove sort
    if (nameHeader) {
      await fireEvent.click(nameHeader);
    }
    expect(onSort).toHaveBeenCalledWith('name', null);
  });

  it('renders with custom cell renderer', () => {
    const customColumns: Column<TestData>[] = [
      {
        key: 'name',
        header: 'Name',
        render: item => item.name.toUpperCase(),
      },
      {
        key: 'age',
        header: 'Age',
        renderHtml: item => `<strong>${item.age}</strong>`,
      },
    ];

    renderDataTable({
      columns: customColumns,
      data: mockData,
    });

    expect(screen.getByText('JOHN DOE')).toBeInTheDocument();
    const strongElements = screen.getAllByRole('strong');
    expect(strongElements).toHaveLength(3);
  });

  it('applies alignment classes', () => {
    const alignColumns: Column<TestData>[] = [
      { key: 'name', header: 'Name', align: 'left' },
      { key: 'age', header: 'Age', align: 'center' },
      { key: 'email', header: 'Email', align: 'right' },
    ];

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(DataTable as any, {
      props: {
        columns: alignColumns,
        data: mockData,
      },
    });

    const headers = container.querySelectorAll('th');
    expect(headers[0]).toHaveClass('text-left');
    expect(headers[1]).toHaveClass('text-center');
    expect(headers[2]).toHaveClass('text-right');
  });

  it('applies custom column width', () => {
    const columnsWithWidth: Column<TestData>[] = [
      { key: 'id', header: 'ID', width: '50px' },
      { key: 'name', header: 'Name', width: '200px' },
    ];

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(DataTable as any, {
      props: {
        columns: columnsWithWidth,
        data: mockData,
      },
    });

    const headers = container.querySelectorAll('th');
    // Svelte style directives (style:width={value}) set inline styles directly on the element
    // In test environments, computed styles may not reflect these inline styles accurately
    // Therefore, we check the style attribute directly to verify Svelte's rendered output
    // This is the correct approach for testing Svelte style directives vs traditional CSS classes
    expect(headers[0]).toHaveAttribute('style', expect.stringContaining('width: 50px'));
    expect(headers[1]).toHaveAttribute('style', expect.stringContaining('width: 200px'));
  });

  it('applies table styling classes', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(DataTable as any, {
      props: {
        columns,
        data: mockData,
        striped: false,
        hoverable: false,
        compact: true,
        fullWidth: false,
        className: 'custom-table',
      },
    });

    const table = container.querySelector('table');
    expect(table).toHaveClass('table', 'table-compact', 'custom-table');
    expect(table).not.toHaveClass('table-zebra', 'w-full');
  });

  it('displays sort indicators', () => {
    renderDataTable({
      columns,
      data: mockData,
      onSort: vi.fn(),
      sortColumn: 'name',
      sortDirection: 'asc',
    });

    // Find the Name button by its text content
    const buttons = screen.getAllByRole('button');
    const nameHeader = buttons.find(btn => btn.textContent?.includes('Name'));
    expect(nameHeader).toBeDefined();
    const svg = nameHeader?.querySelector('svg');
    expect(svg).toBeInTheDocument();
    expect(svg?.querySelector('path')).toBeInTheDocument(); // Check icon is rendered without relying on specific path data
  });

  it('does not show sort button for non-sortable columns', () => {
    const nonSortableColumns: Column<TestData>[] = [
      { key: 'id', header: 'ID', sortable: false },
      { key: 'name', header: 'Name' }, // sortable not specified
    ];

    renderDataTable({
      columns: nonSortableColumns,
      data: mockData,
      onSort: vi.fn(),
    });

    expect(screen.queryByRole('button', { name: /ID/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Name/i })).not.toBeInTheDocument();
    expect(screen.getByText('ID')).toBeInTheDocument();
    expect(screen.getByText('Name')).toBeInTheDocument();
  });

  it('applies hover effect when hoverable', () => {
    const { container } = renderDataTable({
      columns,
      data: mockData,
      hoverable: true,
    });

    const rows = container.querySelectorAll('tbody tr');
    rows.forEach(row => {
      expect(row).toHaveClass('hover:bg-base-200/50', 'transition-colors');
    });
  });
});
