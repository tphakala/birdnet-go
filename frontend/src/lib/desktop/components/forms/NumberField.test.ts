import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import NumberField from './NumberField.svelte';

describe('NumberField', () => {
  it('renders with basic props', () => {
    render(NumberField, {
      props: {
        label: 'Test Number',
        value: 0,
        onUpdate: vi.fn(),
      },
    });

    expect(screen.getByText('Test Number')).toBeInTheDocument();
    expect(screen.getByRole('spinbutton')).toHaveValue(0);
  });

  it('displays the current value', () => {
    render(NumberField, {
      props: {
        label: 'Test Number',
        value: 42,
        onUpdate: vi.fn(),
      },
    });

    expect(screen.getByRole('spinbutton')).toHaveValue(42);
  });

  it('calls onUpdate when value changes', async () => {
    const onUpdate = vi.fn();

    render(NumberField, {
      props: {
        label: 'Test Number',
        value: 0,
        onUpdate,
      },
    });

    const input = screen.getByRole('spinbutton');
    await fireEvent.input(input, { target: { value: '123' } });

    expect(onUpdate).toHaveBeenCalledWith(123);
  });

  it('handles non-numeric input gracefully', async () => {
    const onUpdate = vi.fn();

    render(NumberField, {
      props: {
        label: 'Test Number',
        value: 0,
        onUpdate,
      },
    });

    const input = screen.getByRole('spinbutton');
    await fireEvent.input(input, { target: { value: 'abc' } });

    // Should not call onUpdate for invalid input
    expect(onUpdate).not.toHaveBeenCalled();
  });

  it('respects min and max constraints', () => {
    render(NumberField, {
      props: {
        label: 'Test Number',
        value: 5,
        onUpdate: vi.fn(),
        min: 0,
        max: 10,
      },
    });

    const input = screen.getByRole('spinbutton');
    expect(input).toHaveAttribute('min', '0');
    expect(input).toHaveAttribute('max', '10');
  });

  it('uses custom step value', () => {
    render(NumberField, {
      props: {
        label: 'Test Number',
        value: 0,
        onUpdate: vi.fn(),
        step: 0.5,
      },
    });

    const input = screen.getByRole('spinbutton');
    expect(input).toHaveAttribute('step', '0.5');
  });

  it('shows placeholder when provided', () => {
    render(NumberField, {
      props: {
        label: 'Test Number',
        value: 0,
        onUpdate: vi.fn(),
        placeholder: 'Enter a number',
      },
    });

    const input = screen.getByRole('spinbutton');
    expect(input).toHaveAttribute('placeholder', 'Enter a number');
  });

  it('displays help text when provided', () => {
    render(NumberField, {
      props: {
        label: 'Test Number',
        value: 0,
        onUpdate: vi.fn(),
        helpText: 'This is help text',
      },
    });

    expect(screen.getByText('This is help text')).toBeInTheDocument();
  });

  it('shows required indicator when required', () => {
    render(NumberField, {
      props: {
        label: 'Test Number',
        value: 0,
        onUpdate: vi.fn(),
        required: true,
      },
    });

    expect(screen.getByText('*')).toBeInTheDocument();
  });

  it('renders in disabled state', () => {
    render(NumberField, {
      props: {
        label: 'Test Number',
        value: 0,
        onUpdate: vi.fn(),
        disabled: true,
      },
    });

    const input = screen.getByRole('spinbutton');
    expect(input).toBeDisabled();
  });

  it('shows error message when provided', () => {
    render(NumberField, {
      props: {
        label: 'Test Number',
        value: 0,
        onUpdate: vi.fn(),
        error: 'This is an error',
      },
    });

    expect(screen.getByText('This is an error')).toBeInTheDocument();
  });

  it('applies error styling when error is present', () => {
    render(NumberField, {
      props: {
        label: 'Test Number',
        value: 0,
        onUpdate: vi.fn(),
        error: 'This is an error',
      },
    });

    const input = screen.getByRole('spinbutton');
    expect(input).toHaveClass('input-error');
  });

  it('applies custom className', () => {
    render(NumberField, {
      props: {
        label: 'Test Number',
        value: 0,
        onUpdate: vi.fn(),
        className: 'custom-class',
      },
    });

    const container = screen.getByText('Test Number').closest('.custom-class');
    expect(container).toBeInTheDocument();
  });

  it('handles decimal values correctly', async () => {
    const onUpdate = vi.fn();

    render(NumberField, {
      props: {
        label: 'Test Number',
        value: 0,
        onUpdate,
        step: 0.1,
      },
    });

    const input = screen.getByRole('spinbutton');
    await fireEvent.input(input, { target: { value: '3.14' } });

    expect(onUpdate).toHaveBeenCalledWith(3.14);
  });

  it('passes through additional HTML attributes', () => {
    render(NumberField, {
      props: {
        label: 'Test Number',
        value: 0,
        onUpdate: vi.fn(),
        'data-testid': 'custom-test-id',
      },
    });

    const container = screen.getByTestId('custom-test-id');
    expect(container).toBeInTheDocument();
  });
});
