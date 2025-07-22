import { describe, it, expect } from 'vitest';
import { screen, render } from '@testing-library/svelte';
import LoadingSpinner from './LoadingSpinner.svelte';
import type { ComponentProps } from 'svelte';

// Helper function to render LoadingSpinner with proper typing
const renderLoadingSpinner = (props?: Partial<ComponentProps<LoadingSpinner>>) => {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return render(LoadingSpinner as any, props ? { props } : undefined);
};

describe('LoadingSpinner', () => {
  it('renders with default props', () => {
    renderLoadingSpinner();

    const spinner = screen.getByRole('status');
    expect(spinner).toBeInTheDocument();
    expect(spinner).toHaveAttribute('aria-label', 'Loading...');

    const spinnerElement = spinner.querySelector('.loading');
    expect(spinnerElement).toHaveClass('loading-spinner', 'loading-lg', 'text-primary');
  });

  it('renders with custom size', () => {
    renderLoadingSpinner({ size: 'sm' });

    const spinner = screen.getByRole('status');
    const spinnerElement = spinner.querySelector('.loading');
    expect(spinnerElement).toHaveClass('loading-sm');
  });

  it('renders with custom color', () => {
    renderLoadingSpinner({ color: 'text-secondary' });

    const spinner = screen.getByRole('status');
    const spinnerElement = spinner.querySelector('.loading');
    expect(spinnerElement).toHaveClass('text-secondary');
  });

  it('renders with custom label', () => {
    renderLoadingSpinner({ label: 'Processing...' });

    const spinner = screen.getByRole('status');
    expect(spinner).toHaveAttribute('aria-label', 'Processing...');
    expect(screen.getByText('Processing...')).toHaveClass('sr-only');
  });

  it('renders with all custom props', () => {
    renderLoadingSpinner({
      size: 'xl',
      color: 'text-error',
      label: 'Please wait',
    });

    const spinner = screen.getByRole('status');
    expect(spinner).toHaveAttribute('aria-label', 'Please wait');

    const spinnerElement = spinner.querySelector('.loading');
    expect(spinnerElement).toHaveClass('loading-xl', 'text-error');
  });
});
