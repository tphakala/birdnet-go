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
    // Note: Uses 'change' event instead of 'input' to match Svelte's event handling
    // NumberField component binds to 'change' event for proper validation timing
    await fireEvent.change(input, { target: { value: '123' } });

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
    await fireEvent.change(input, { target: { value: 'abc' } });

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

  it('clamps minimum constraint during input', async () => {
    const onUpdate = vi.fn();

    render(NumberField, {
      props: {
        label: 'Test Number',
        value: 5,
        onUpdate,
        min: 0,
      },
    });

    const input = screen.getByRole('spinbutton');

    // Try to input a value below minimum
    await fireEvent.change(input, { target: { value: '-5' } });

    // Should clamp to minimum value
    expect(onUpdate).toHaveBeenCalledWith(0);
  });

  it('clamps maximum constraint during input', async () => {
    const onUpdate = vi.fn();

    render(NumberField, {
      props: {
        label: 'Test Number',
        value: 5,
        onUpdate,
        max: 10,
      },
    });

    const input = screen.getByRole('spinbutton');

    // Try to input a value above maximum
    await fireEvent.change(input, { target: { value: '15' } });

    // Should clamp to maximum value
    expect(onUpdate).toHaveBeenCalledWith(10);
  });

  it('allows valid values within min/max range', async () => {
    const onUpdate = vi.fn();

    render(NumberField, {
      props: {
        label: 'Test Number',
        value: 5,
        onUpdate,
        min: 0,
        max: 10,
      },
    });

    const input = screen.getByRole('spinbutton');

    // Input a valid value within range
    await fireEvent.change(input, { target: { value: '7' } });

    // Should call onUpdate for valid values
    expect(onUpdate).toHaveBeenCalledWith(7);
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
    await fireEvent.change(input, { target: { value: '3.14' } });

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

  it('handles very large numbers', async () => {
    const onUpdate = vi.fn();

    render(NumberField, {
      props: {
        label: 'Test Number',
        value: 0,
        onUpdate,
      },
    });

    const input = screen.getByRole('spinbutton');

    // Test very large number
    await fireEvent.change(input, { target: { value: '9007199254740991' } });

    expect(onUpdate).toHaveBeenCalledWith(9007199254740991);
  });

  it('handles scientific notation', async () => {
    const onUpdate = vi.fn();

    render(NumberField, {
      props: {
        label: 'Test Number',
        value: 0,
        onUpdate,
      },
    });

    const input = screen.getByRole('spinbutton');

    // Test scientific notation
    await fireEvent.change(input, { target: { value: '1e6' } });

    expect(onUpdate).toHaveBeenCalledWith(1000000);
  });

  it('handles negative scientific notation', async () => {
    const onUpdate = vi.fn();

    render(NumberField, {
      props: {
        label: 'Test Number',
        value: 0,
        onUpdate,
      },
    });

    const input = screen.getByRole('spinbutton');

    // Test negative scientific notation
    await fireEvent.change(input, { target: { value: '-1e-6' } });

    expect(onUpdate).toHaveBeenCalledWith(-0.000001);
  });

  it('handles precision limits correctly', async () => {
    const onUpdate = vi.fn();

    render(NumberField, {
      props: {
        label: 'Test Number',
        value: 0,
        onUpdate,
      },
    });

    const input = screen.getByRole('spinbutton');

    // Test high precision decimal
    await fireEvent.change(input, { target: { value: '0.123456789012345' } });

    expect(onUpdate).toHaveBeenCalledWith(0.123456789012345);
  });

  it('handles infinite values when set programmatically', async () => {
    const onUpdate = vi.fn();

    const { rerender } = render(NumberField, {
      props: {
        label: 'Test Number',
        value: 0,
        onUpdate,
        min: 0,
        max: 100,
      },
    });

    // Simulate receiving Infinity from external source (e.g., API)
    await rerender({
      label: 'Test Number',
      value: Infinity,
      onUpdate,
      min: 0,
      max: 100,
    });

    const input = screen.getByRole('spinbutton');

    // Trigger blur to ensure clamping occurs
    await fireEvent.blur(input);

    // Should clamp Infinity to min (0)
    expect(onUpdate).toHaveBeenCalledWith(0);
  });

  it('handles NaN values when set programmatically', async () => {
    const onUpdate = vi.fn();

    const { rerender } = render(NumberField, {
      props: {
        label: 'Test Number',
        value: 0,
        onUpdate,
        min: 0,
        max: 100,
      },
    });

    // Simulate receiving NaN from external source (e.g., calculation result)
    await rerender({
      label: 'Test Number',
      value: NaN,
      onUpdate,
      min: 0,
      max: 100,
    });

    const input = screen.getByRole('spinbutton');

    // Trigger blur to ensure clamping occurs
    await fireEvent.blur(input);

    // Should clamp NaN to min (0)
    expect(onUpdate).toHaveBeenCalledWith(0);
  });

  it('handles negative infinity when set programmatically', async () => {
    const onUpdate = vi.fn();

    const { rerender } = render(NumberField, {
      props: {
        label: 'Test Number',
        value: 0,
        onUpdate,
        min: 0,
        max: 100,
      },
    });

    // Simulate receiving -Infinity from external source
    await rerender({
      label: 'Test Number',
      value: -Infinity,
      onUpdate,
      min: 0,
      max: 100,
    });

    const input = screen.getByRole('spinbutton');

    // Trigger blur to ensure clamping occurs
    await fireEvent.blur(input);

    // Should clamp -Infinity to min (0)
    expect(onUpdate).toHaveBeenCalledWith(0);
  });

  it('clamps values on blur-xs event', async () => {
    const onUpdate = vi.fn();

    render(NumberField, {
      props: {
        label: 'Test Number',
        value: 150, // Start with invalid value
        onUpdate,
        min: 0,
        max: 100,
      },
    });

    const input = screen.getByRole('spinbutton');

    // Trigger blur event
    await fireEvent.blur(input);

    // Should clamp to max value
    expect(onUpdate).toHaveBeenCalledWith(100);
  });

  it('shows clamping feedback message temporarily', async () => {
    const onUpdate = vi.fn();

    render(NumberField, {
      props: {
        label: 'Test Number',
        value: 5,
        onUpdate,
        min: 0,
        max: 10,
      },
    });

    const input = screen.getByRole('spinbutton');

    // Input value above maximum
    await fireEvent.change(input, { target: { value: '15' } });

    // Should show clamping message with interpolated value
    expect(screen.getByText(/Value was adjusted to maximum \(10\)/)).toBeInTheDocument();
  });

  it('handles extremely large numbers by clamping', async () => {
    const onUpdate = vi.fn();

    render(NumberField, {
      props: {
        label: 'Test Number',
        value: 0,
        onUpdate,
        min: 0,
        max: 1,
      },
    });

    const input = screen.getByRole('spinbutton');

    // Test extremely large number that should be clamped
    await fireEvent.change(input, { target: { value: '99999999999999999999' } });

    expect(onUpdate).toHaveBeenCalledWith(1); // Should clamp to max
  });
});
