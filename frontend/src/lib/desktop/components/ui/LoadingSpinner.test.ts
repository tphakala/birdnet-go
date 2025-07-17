import { describe, it, expect } from 'vitest';
import { screen, render } from '@testing-library/svelte';
import LoadingSpinner from './LoadingSpinner.svelte';

describe('LoadingSpinner', () => {
  it('renders with default props', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(LoadingSpinner as any);

    const spinner = screen.getByRole('status');
    expect(spinner).toBeInTheDocument();
    expect(spinner).toHaveAttribute('aria-label', 'Loading...');

    const spinnerElement = spinner.querySelector('.loading');
    expect(spinnerElement).toHaveClass('loading-spinner', 'loading-lg', 'text-primary');
  });

  it('renders with custom size', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(LoadingSpinner as any, { props: { size: 'sm' } });

    const spinner = screen.getByRole('status');
    const spinnerElement = spinner.querySelector('.loading');
    expect(spinnerElement).toHaveClass('loading-sm');
  });

  it('renders with custom color', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(LoadingSpinner as any, { props: { color: 'text-secondary' } });

    const spinner = screen.getByRole('status');
    const spinnerElement = spinner.querySelector('.loading');
    expect(spinnerElement).toHaveClass('text-secondary');
  });

  it('renders with custom label', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(LoadingSpinner as any, { props: { label: 'Processing...' } });

    const spinner = screen.getByRole('status');
    expect(spinner).toHaveAttribute('aria-label', 'Processing...');
    expect(screen.getByText('Processing...')).toHaveClass('sr-only');
  });

  it('renders with all custom props', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(LoadingSpinner as any, {
      props: {
        size: 'xl',
        color: 'text-error',
        label: 'Please wait',
      },
    });

    const spinner = screen.getByRole('status');
    expect(spinner).toHaveAttribute('aria-label', 'Please wait');

    const spinnerElement = spinner.querySelector('.loading');
    expect(spinnerElement).toHaveClass('loading-xl', 'text-error');
  });
});
