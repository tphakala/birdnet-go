import { describe, it, expect } from 'vitest';
import { screen, render } from '@testing-library/svelte';
import LoadingSpinner from './LoadingSpinner.svelte';
import type { ComponentProps } from 'svelte';

// Helper function to render LoadingSpinner with proper typing
const renderLoadingSpinner = (props?: Partial<ComponentProps<typeof LoadingSpinner>>) => {
  return render(LoadingSpinner, props ? { props } : { props: {} });
};

describe('LoadingSpinner', () => {
  it('renders with default props', () => {
    renderLoadingSpinner();

    const spinner = screen.getByRole('status');
    expect(spinner).toBeInTheDocument();
    expect(spinner).toHaveAttribute('aria-label', 'Loading...');

    // Now uses native Tailwind classes instead of daisyUI loading classes
    const spinnerElement = spinner.querySelector('span.animate-spin');
    expect(spinnerElement).toBeInTheDocument();
    // Default size is lg (w-10 h-10 border-2)
    expect(spinnerElement).toHaveClass('w-10', 'h-10', 'border-2');
    // Default color uses CSS variable
    expect(spinnerElement?.className).toContain('text-[var(--color-primary)]');
  });

  it('renders with custom size', () => {
    renderLoadingSpinner({ size: 'sm' });

    const spinner = screen.getByRole('status');
    const spinnerElement = spinner.querySelector('span.animate-spin');
    // sm size uses w-4 h-4 border
    expect(spinnerElement).toHaveClass('w-4', 'h-4', 'border');
  });

  it('renders with custom color', () => {
    renderLoadingSpinner({ color: 'text-secondary' });

    const spinner = screen.getByRole('status');
    const spinnerElement = spinner.querySelector('span.animate-spin');
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

    const spinnerElement = spinner.querySelector('span.animate-spin');
    // xl size uses w-14 h-14 border-[3px]
    expect(spinnerElement).toHaveClass('w-14', 'h-14', 'text-error');
  });
});
