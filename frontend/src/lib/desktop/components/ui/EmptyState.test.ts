import { describe, it, expect, vi } from 'vitest';
import { renderTyped, screen, fireEvent } from '../../../../test/render-helpers';
import EmptyState from './EmptyState.svelte';
import EmptyStateTestWrapper from './EmptyState.test.svelte';
import type { ComponentProps } from 'svelte';

// Helper function to render EmptyState with proper typing
const renderEmptyState = (props?: Partial<ComponentProps<typeof EmptyState>>) => {
  return renderTyped(EmptyState, props ? { props } : { props: {} });
};

describe('EmptyState', () => {
  it('renders with default props', () => {
    const { container } = renderEmptyState();

    const emptyState = container.querySelector('div');
    expect(emptyState).toBeInTheDocument();
    expect(emptyState).toHaveClass('flex', 'flex-col', 'items-center', 'justify-center');

    // Should show default icon
    const svg = container.querySelector('svg');
    expect(svg).toBeInTheDocument();
  });

  it('renders with title and description', () => {
    renderEmptyState({
      title: 'No data found',
      description: 'Try adjusting your search criteria',
    });

    expect(screen.getByText('No data found')).toBeInTheDocument();
    expect(screen.getByText('Try adjusting your search criteria')).toBeInTheDocument();
  });

  it('renders with custom icon', () => {
    renderTyped(EmptyStateTestWrapper, {
      props: {
        showCustomIcon: true,
        description: 'Test with custom icon',
        action: { label: 'Test action', onClick: () => {} },
      },
    });

    expect(screen.getByTestId('custom-icon')).toBeInTheDocument();
  });

  it('renders with action button', async () => {
    const onClick = vi.fn();

    renderEmptyState({
      title: 'No results',
      action: {
        label: 'Try again',
        onClick,
      },
    });

    const button = screen.getByText('Try again');
    expect(button).toBeInTheDocument();
    expect(button).toHaveClass('btn', 'btn-primary');

    await fireEvent.click(button);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('renders with custom children', () => {
    renderTyped(EmptyStateTestWrapper, {
      props: {
        showChildren: true,
        description: 'Test with custom children',
        action: { label: 'Test action', onClick: () => {} },
      },
    });

    expect(screen.getByText('Custom child content')).toBeInTheDocument();
    expect(screen.getByText('Additional info')).toBeInTheDocument();
  });

  it('renders with custom className', () => {
    const { container } = renderEmptyState({
      className: 'custom-empty-state',
    });

    const emptyState = container.querySelector('div');
    expect(emptyState).toHaveClass('custom-empty-state');
  });

  it('renders complete empty state with all props', () => {
    const onClick = vi.fn();

    renderTyped(EmptyStateTestWrapper, {
      props: {
        showCustomIcon: true,
        showChildren: true,
        title: 'Complete Empty State',
        description: 'This has all the features',
        action: {
          label: 'Take action',
          onClick,
        },
      },
    });

    expect(screen.getByTestId('custom-icon')).toBeInTheDocument();
    expect(screen.getByText('Complete Empty State')).toBeInTheDocument();
    expect(screen.getByText('This has all the features')).toBeInTheDocument();
    expect(screen.getByText('Custom child content')).toBeInTheDocument();
    expect(screen.getByText('Take action')).toBeInTheDocument();
  });

  it('spreads additional props', () => {
    const { container } = renderEmptyState({
      id: 'test-empty-state',
      'data-testid': 'empty-state',
    });

    const emptyState = container.querySelector('div');
    expect(emptyState).toHaveAttribute('id', 'test-empty-state');
    expect(emptyState).toHaveAttribute('data-testid', 'empty-state');
  });
});
