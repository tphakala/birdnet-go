import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import Pagination from './Pagination.svelte';

describe('Pagination', () => {
  it('renders with default props', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(Pagination as any);

    const nav = screen.getByLabelText('Pagination Navigation');
    expect(nav).toBeInTheDocument();

    const pageInfo = screen.getByText('Page 1 of 1');
    expect(pageInfo).toBeInTheDocument();
  });

  it('renders page numbers when totalPages > 1', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(Pagination as any, {
      props: {
        currentPage: 1,
        totalPages: 5,
      },
    });

    expect(screen.getByText('1')).toBeInTheDocument();
    expect(screen.getByText('2')).toBeInTheDocument();
    expect(screen.getByText('3')).toBeInTheDocument();
    expect(screen.getByText('4')).toBeInTheDocument();
    expect(screen.getByText('5')).toBeInTheDocument();
  });

  it('calls onPageChange when clicking page numbers', async () => {
    const onPageChange = vi.fn();

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(Pagination as any, {
      props: {
        currentPage: 1,
        totalPages: 5,
        onPageChange,
      },
    });

    await fireEvent.click(screen.getByText('3'));
    expect(onPageChange).toHaveBeenCalledWith(3);
  });

  it('disables previous button on first page', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(Pagination as any, {
      props: {
        currentPage: 1,
        totalPages: 5,
      },
    });

    const prevButton = screen.getByLabelText('Go to previous page');
    expect(prevButton).toBeDisabled();
  });

  it('disables next button on last page', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(Pagination as any, {
      props: {
        currentPage: 5,
        totalPages: 5,
      },
    });

    const nextButton = screen.getByLabelText('Go to next page');
    expect(nextButton).toBeDisabled();
  });

  it('navigates with previous and next buttons', async () => {
    const onPageChange = vi.fn();

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(Pagination as any, {
      props: {
        currentPage: 3,
        totalPages: 5,
        onPageChange,
      },
    });

    const prevButton = screen.getByLabelText('Go to previous page');
    const nextButton = screen.getByLabelText('Go to next page');

    await fireEvent.click(prevButton);
    expect(onPageChange).toHaveBeenCalledWith(2);

    await fireEvent.click(nextButton);
    expect(onPageChange).toHaveBeenCalledWith(4);
  });

  it('shows ellipsis for many pages', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(Pagination as any, {
      props: {
        currentPage: 10,
        totalPages: 20,
        maxVisiblePages: 5,
      },
    });

    // Should show: 1 ... 8 9 10 11 12 ... 20
    expect(screen.getByText('1')).toBeInTheDocument();
    expect(screen.getByText('20')).toBeInTheDocument();
    expect(screen.getAllByText('...')).toHaveLength(2);
  });

  it('highlights current page', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(Pagination as any, {
      props: {
        currentPage: 3,
        totalPages: 5,
      },
    });

    const currentPageButton = screen.getByText('3');
    expect(currentPageButton).toHaveClass('btn-active');
    expect(currentPageButton).toHaveAttribute('aria-current', 'page');
  });

  it('respects disabled prop', async () => {
    const onPageChange = vi.fn();

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(Pagination as any, {
      props: {
        currentPage: 2,
        totalPages: 5,
        disabled: true,
        onPageChange,
      },
    });

    const pageButton = screen.getByText('3');
    await fireEvent.click(pageButton);

    expect(onPageChange).not.toHaveBeenCalled();
  });

  it('hides page info when showPageInfo is false', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(Pagination as any, {
      props: {
        currentPage: 1,
        totalPages: 1,
        showPageInfo: false,
      },
    });

    expect(screen.queryByText('Page 1 of 1')).not.toBeInTheDocument();
  });

  it('applies custom className', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(Pagination as any, {
      props: {
        className: 'custom-pagination',
      },
    });

    const nav = screen.getByLabelText('Pagination Navigation');
    expect(nav).toHaveClass('custom-pagination');
  });
});
