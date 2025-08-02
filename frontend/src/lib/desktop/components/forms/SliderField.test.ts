import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import SliderField from './SliderField.svelte';

describe('SliderField', () => {
  it('renders with basic props', () => {
    render(SliderField, {
      props: {
        label: 'Test Slider',
        value: 5,
        onUpdate: vi.fn(),
        min: 0,
        max: 10,
      },
    });

    expect(screen.getByText('Test Slider')).toBeInTheDocument();
    expect(screen.getByRole('slider')).toBeInTheDocument();
  });

  it('displays the current value', () => {
    render(SliderField, {
      props: {
        label: 'Test Slider',
        value: 7,
        onUpdate: vi.fn(),
        min: 0,
        max: 10,
      },
    });

    const slider = screen.getByRole('slider');
    expect(slider).toHaveValue('7');
  });

  it('shows value badge when showValue is true', () => {
    render(SliderField, {
      props: {
        label: 'Test Slider',
        value: 5,
        onUpdate: vi.fn(),
        min: 0,
        max: 10,
        showValue: true,
      },
    });

    const badge = document.querySelector('.badge');
    expect(badge).toBeInTheDocument();
    expect(badge).toHaveTextContent('5');
  });

  it('hides value badge when showValue is false', () => {
    render(SliderField, {
      props: {
        label: 'Test Slider',
        value: 5,
        onUpdate: vi.fn(),
        min: 0,
        max: 10,
        showValue: false,
      },
    });

    const badge = document.querySelector('.badge');
    expect(badge).not.toBeInTheDocument();
  });

  it('displays value with unit when provided', () => {
    render(SliderField, {
      props: {
        label: 'Volume',
        value: 75,
        onUpdate: vi.fn(),
        min: 0,
        max: 100,
        unit: '%',
        showValue: true,
      },
    });

    expect(screen.getByText('75%')).toBeInTheDocument();
  });

  it('uses custom format function when provided', () => {
    const formatValue = (value: number) => `${value.toFixed(1)} dB`;

    render(SliderField, {
      props: {
        label: 'Volume',
        value: 5.5,
        onUpdate: vi.fn(),
        min: 0,
        max: 10,
        formatValue,
        showValue: true,
      },
    });

    expect(screen.getByText('5.5 dB')).toBeInTheDocument();
  });

  it('calls onUpdate when value changes', async () => {
    const onUpdate = vi.fn();

    render(SliderField, {
      props: {
        label: 'Test Slider',
        value: 5,
        onUpdate,
        min: 0,
        max: 10,
      },
    });

    const slider = screen.getByRole('slider');
    await fireEvent.input(slider, { target: { value: '8' } });

    expect(onUpdate).toHaveBeenCalledWith(8);
  });

  it('respects min and max constraints', () => {
    render(SliderField, {
      props: {
        label: 'Test Slider',
        value: 5,
        onUpdate: vi.fn(),
        min: 2,
        max: 8,
      },
    });

    const slider = screen.getByRole('slider');
    expect(slider).toHaveAttribute('min', '2');
    expect(slider).toHaveAttribute('max', '8');
  });

  it('uses custom step value', () => {
    render(SliderField, {
      props: {
        label: 'Test Slider',
        value: 5,
        onUpdate: vi.fn(),
        min: 0,
        max: 10,
        step: 0.5,
      },
    });

    const slider = screen.getByRole('slider');
    expect(slider).toHaveAttribute('step', '0.5');
  });

  it('shows required indicator when required', () => {
    render(SliderField, {
      props: {
        label: 'Test Slider',
        value: 5,
        onUpdate: vi.fn(),
        min: 0,
        max: 10,
        required: true,
      },
    });

    expect(screen.getByText('*')).toBeInTheDocument();
  });

  it('renders in disabled state', () => {
    render(SliderField, {
      props: {
        label: 'Test Slider',
        value: 5,
        onUpdate: vi.fn(),
        min: 0,
        max: 10,
        disabled: true,
      },
    });

    const slider = screen.getByRole('slider');
    expect(slider).toBeDisabled();
  });

  it('displays help text when provided', () => {
    render(SliderField, {
      props: {
        label: 'Test Slider',
        value: 5,
        onUpdate: vi.fn(),
        min: 0,
        max: 10,
        helpText: 'This is help text',
      },
    });

    expect(screen.getByText('This is help text')).toBeInTheDocument();
  });

  it('shows error message when provided', () => {
    render(SliderField, {
      props: {
        label: 'Test Slider',
        value: 5,
        onUpdate: vi.fn(),
        min: 0,
        max: 10,
        error: 'This is an error',
      },
    });

    expect(screen.getByText('This is an error')).toBeInTheDocument();
  });

  it('applies error styling when error is present', () => {
    render(SliderField, {
      props: {
        label: 'Test Slider',
        value: 5,
        onUpdate: vi.fn(),
        min: 0,
        max: 10,
        error: 'This is an error',
      },
    });

    const slider = screen.getByRole('slider');
    expect(slider).toHaveClass('range-error');
  });

  it('applies custom className', () => {
    render(SliderField, {
      props: {
        label: 'Test Slider',
        value: 5,
        onUpdate: vi.fn(),
        min: 0,
        max: 10,
        className: 'custom-class',
      },
    });

    const container = screen.getByText('Test Slider').closest('.form-control');
    expect(container).toHaveClass('custom-class');
  });

  it('handles decimal values correctly', async () => {
    const onUpdate = vi.fn();

    render(SliderField, {
      props: {
        label: 'Test Slider',
        value: 2.5,
        onUpdate,
        min: 0,
        max: 5,
        step: 0.1,
      },
    });

    const slider = screen.getByRole('slider');
    await fireEvent.input(slider, { target: { value: '3.7' } });

    expect(onUpdate).toHaveBeenCalledWith(3.7);
  });

  it('handles non-numeric input gracefully', async () => {
    const onUpdate = vi.fn();

    render(SliderField, {
      props: {
        label: 'Test Slider',
        value: 5,
        onUpdate,
        min: 0,
        max: 10,
      },
    });

    const slider = screen.getByRole('slider');
    await fireEvent.input(slider, { target: { value: 'abc' } });

    // Range input will convert invalid values to valid numbers
    expect(onUpdate).toHaveBeenCalled();
  });

  it('applies primary range styling by default', () => {
    render(SliderField, {
      props: {
        label: 'Test Slider',
        value: 5,
        onUpdate: vi.fn(),
        min: 0,
        max: 10,
      },
    });

    const slider = screen.getByRole('slider');
    expect(slider).toHaveClass('range-primary');
  });

  it('displays label and value in header layout', () => {
    render(SliderField, {
      props: {
        label: 'Volume Control',
        value: 75,
        onUpdate: vi.fn(),
        min: 0,
        max: 100,
        showValue: true,
      },
    });

    const header = screen.getByText('Volume Control').closest('.label');
    expect(header).toHaveClass('label');
  });

  it('passes through additional HTML attributes', () => {
    render(SliderField, {
      props: {
        label: 'Test Slider',
        value: 5,
        onUpdate: vi.fn(),
        min: 0,
        max: 10,
        'data-testid': 'custom-test-id',
      },
    });

    const container = screen.getByTestId('custom-test-id');
    expect(container).toBeInTheDocument();
  });
});
