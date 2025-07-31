import { describe, it, expect, vi } from 'vitest';
import { createComponentTestFactory, screen } from '../../../../test/render-helpers';
import ProgressBar from './ProgressBar.svelte';

describe('ProgressBar', () => {
  const progressTest = createComponentTestFactory(ProgressBar);
  it('renders with default props', () => {
     
    const { container } = progressTest.render({
      props: {
        value: 50,
      },
    });

    const progressbar = screen.getByRole('progressbar');
    expect(progressbar).toBeInTheDocument();
    expect(progressbar).toHaveAttribute('aria-valuenow', '50');
    expect(progressbar).toHaveAttribute('aria-valuemin', '0');
    expect(progressbar).toHaveAttribute('aria-valuemax', '100');

    const bar = container.querySelector('.bg-primary');
    expect(bar).toHaveStyle('width: 50%');
  });

  it('renders with custom max value', () => {
     
    const { container } = progressTest.render({
      props: {
        value: 25,
        max: 50,
      },
    });

    const progressbar = screen.getByRole('progressbar');
    expect(progressbar).toHaveAttribute('aria-valuemax', '50');

    const bar = container.querySelector('.bg-primary');
    expect(bar).toHaveStyle('width: 50%'); // 25/50 = 50%
  });

  it('renders with different sizes', () => {
    const sizes = ['xs', 'sm', 'md', 'lg'] as const;

    sizes.forEach(size => {
       
      const { container, unmount } = progressTest.render({
        props: { value: 50, size },
      });

      const progressbar = container.querySelector('[role="progressbar"]');
      const expectedClass = {
        xs: 'h-1',
        sm: 'h-2',
        md: 'h-4',
        lg: 'h-6',
      }[size];

      expect(progressbar).toHaveClass(expectedClass);
      unmount();
    });
  });

  it('renders with different variants', () => {
    const variants = [
      'primary',
      'secondary',
      'accent',
      'info',
      'success',
      'warning',
      'error',
    ] as const;

    variants.forEach(variant => {
       
      const { container, unmount } = progressTest.render({
        props: { value: 50, variant },
      });

      const bar = container.querySelector(`.bg-${variant}`);
      expect(bar).toBeInTheDocument();
      unmount();
    });
  });

  it('shows label when showLabel is true', () => {
     
    progressTest.render({
      props: {
        value: 75,
        showLabel: true,
      },
    });

    expect(screen.getByText('75%')).toBeInTheDocument();
  });

  it('uses custom label format', () => {
    const labelFormat = vi.fn((value: number, max: number) => `${value} of ${max}`);

     
    progressTest.render({
      props: {
        value: 30,
        max: 100,
        showLabel: true,
        labelFormat,
      },
    });

    expect(labelFormat).toHaveBeenCalledWith(30, 100);
    expect(screen.getByText('30 of 100')).toBeInTheDocument();
  });

  it('applies color thresholds', async () => {
     
    const { container, rerender } = progressTest.render({
      props: {
        value: 20,
        colorThresholds: [
          { value: 25, variant: 'warning' },
          { value: 50, variant: 'info' },
          { value: 75, variant: 'success' },
        ],
      },
    });

    // Below first threshold - should use default variant
    let bar = container.querySelector('.bg-primary');
    expect(bar).toBeInTheDocument();

    // Between 25-50 - should be warning
    await rerender({
      value: 30,
      colorThresholds: [
        { value: 25, variant: 'warning' },
        { value: 50, variant: 'info' },
        { value: 75, variant: 'success' },
      ],
    });
    bar = container.querySelector('.bg-warning');
    expect(bar).toBeInTheDocument();

    // Between 50-75 - should be info
    await rerender({
      value: 60,
      colorThresholds: [
        { value: 25, variant: 'warning' },
        { value: 50, variant: 'info' },
        { value: 75, variant: 'success' },
      ],
    });
    bar = container.querySelector('.bg-info');
    expect(bar).toBeInTheDocument();

    // Above 75 - should be success
    await rerender({
      value: 80,
      colorThresholds: [
        { value: 25, variant: 'warning' },
        { value: 50, variant: 'info' },
        { value: 75, variant: 'success' },
      ],
    });
    bar = container.querySelector('.bg-success');
    expect(bar).toBeInTheDocument();
  });

  it('clamps value between 0 and max', () => {
     
    const { container } = progressTest.render({
      props: {
        value: 150,
        max: 100,
      },
    });

    const bar = container.querySelector('.bg-primary');
    expect(bar).toHaveStyle('width: 100%');

     
    const { container: container2 } = progressTest.render({
      props: {
        value: -20,
        max: 100,
      },
    });

    const bar2 = container2.querySelector('.bg-primary');
    expect(bar2).toHaveStyle('width: 0%');
  });

  it('applies striped styles', () => {
     
    const { container } = progressTest.render({
      props: {
        value: 50,
        striped: true,
      },
    });

    const bar = container.querySelector('.bg-stripes');
    expect(bar).toBeInTheDocument();
    expect(bar).not.toHaveClass('animate-stripes');
  });

  it('applies animated stripes', () => {
     
    const { container } = progressTest.render({
      props: {
        value: 50,
        striped: true,
        animated: true,
      },
    });

    const bar = container.querySelector('.animate-stripes');
    expect(bar).toBeInTheDocument();
  });

  it('applies custom classes', () => {
     
    const { container } = progressTest.render({
      props: {
        value: 50,
        className: 'custom-container',
        barClassName: 'custom-bar',
      },
    });

    const progressbar = screen.getByRole('progressbar');
    expect(progressbar).toHaveClass('custom-container');

    const bar = container.querySelector('.bg-primary');
    expect(bar).toHaveClass('custom-bar');
  });

  it('spreads additional props', () => {
     
    progressTest.render({
      props: {
        value: 50,
        id: 'test-progress',
        'data-testid': 'progress-bar',
      },
    });

    const progressbar = screen.getByRole('progressbar');
    expect(progressbar).toHaveAttribute('id', 'test-progress');
    expect(progressbar).toHaveAttribute('data-testid', 'progress-bar');
  });

  it('sets aria-label when showLabel is true', () => {
     
    progressTest.render({
      props: {
        value: 75,
        showLabel: true,
      },
    });

    const progressbar = screen.getByRole('progressbar');
    expect(progressbar).toHaveAttribute('aria-label', '75%');
  });

  it('adjusts label color based on progress', () => {
     
    const { container } = progressTest.render({
      props: {
        value: 30,
        showLabel: true,
      },
    });

    let label = container.querySelector('.text-base-content');
    expect(label).toBeInTheDocument();

     
    const { container: container2 } = progressTest.render({
      props: {
        value: 70,
        showLabel: true,
      },
    });

    label = container2.querySelector('.text-white');
    expect(label).toBeInTheDocument();
    expect(label).toHaveClass('mix-blend-difference');
  });
});
