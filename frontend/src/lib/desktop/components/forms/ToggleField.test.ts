import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import ToggleField from './ToggleField.svelte';

describe('ToggleField', () => {
  it('renders with basic props', () => {
    render(ToggleField, {
      props: {
        label: 'Test Toggle',
        value: false,
        onUpdate: vi.fn(),
      },
    });

    expect(screen.getByText('Test Toggle')).toBeInTheDocument();
    expect(screen.getByRole('checkbox')).toBeInTheDocument();
  });

  it('displays current value correctly', () => {
    render(ToggleField, {
      props: {
        label: 'Test Toggle',
        value: true,
        onUpdate: vi.fn(),
      },
    });

    const toggle = screen.getByRole('checkbox');
    expect(toggle).toBeChecked();
  });

  it('displays unchecked state correctly', () => {
    render(ToggleField, {
      props: {
        label: 'Test Toggle',
        value: false,
        onUpdate: vi.fn(),
      },
    });

    const toggle = screen.getByRole('checkbox');
    expect(toggle).not.toBeChecked();
  });

  it('calls onUpdate when toggle is clicked', async () => {
    const onUpdate = vi.fn();

    render(ToggleField, {
      props: {
        label: 'Test Toggle',
        value: false,
        onUpdate,
      },
    });

    const toggle = screen.getByRole('checkbox');
    await fireEvent.click(toggle);

    expect(onUpdate).toHaveBeenCalledWith(true);
  });

  it('calls onUpdate with opposite value when toggled', async () => {
    const onUpdate = vi.fn();

    render(ToggleField, {
      props: {
        label: 'Test Toggle',
        value: true,
        onUpdate,
      },
    });

    const toggle = screen.getByRole('checkbox');
    await fireEvent.click(toggle);

    expect(onUpdate).toHaveBeenCalledWith(false);
  });

  it('displays description when provided', () => {
    render(ToggleField, {
      props: {
        label: 'Test Toggle',
        description: 'This is a description of the toggle',
        value: false,
        onUpdate: vi.fn(),
      },
    });

    expect(screen.getByText('This is a description of the toggle')).toBeInTheDocument();
  });

  it('shows required indicator when required', () => {
    render(ToggleField, {
      props: {
        label: 'Required Toggle',
        value: false,
        onUpdate: vi.fn(),
        required: true,
      },
    });

    expect(screen.getByText('*')).toBeInTheDocument();
  });

  it('renders in disabled state', () => {
    render(ToggleField, {
      props: {
        label: 'Test Toggle',
        value: false,
        onUpdate: vi.fn(),
        disabled: true,
      },
    });

    const toggle = screen.getByRole('checkbox');
    expect(toggle).toBeDisabled();
  });

  it('does not call onUpdate when disabled', async () => {
    const onUpdate = vi.fn();
    const user = userEvent.setup();

    render(ToggleField, {
      props: {
        label: 'Test Toggle',
        value: false,
        onUpdate,
        disabled: true,
      },
    });

    const toggle = screen.getByRole('checkbox');
    expect(toggle).toBeDisabled();

    // Attempt to click the disabled toggle
    try {
      await user.click(toggle);
    } catch {
      // Click may fail on disabled elements, which is expected
    }

    // Verify onUpdate was never called
    expect(onUpdate).not.toHaveBeenCalled();
  });

  it('shows error message when provided', () => {
    render(ToggleField, {
      props: {
        label: 'Test Toggle',
        value: false,
        onUpdate: vi.fn(),
        error: 'This is an error message',
      },
    });

    expect(screen.getByText('This is an error message')).toBeInTheDocument();
  });

  it('applies error styling when error is present', () => {
    render(ToggleField, {
      props: {
        label: 'Test Toggle',
        value: false,
        onUpdate: vi.fn(),
        error: 'Error message',
      },
    });

    const toggle = screen.getByRole('checkbox');
    // Now uses native Tailwind class for error state
    expect(toggle.className).toContain('checked:bg-[var(--color-error)]');
  });

  it('applies primary styling by default', () => {
    render(ToggleField, {
      props: {
        label: 'Test Toggle',
        value: false,
        onUpdate: vi.fn(),
      },
    });

    const toggle = screen.getByRole('checkbox');
    // Now uses native Tailwind class for primary state
    expect(toggle.className).toContain('checked:bg-[var(--color-primary)]');
  });

  it('applies custom className', () => {
    const { container } = render(ToggleField, {
      props: {
        label: 'Test Toggle',
        value: false,
        onUpdate: vi.fn(),
        className: 'custom-class',
      },
    });

    // The wrapper div has the custom class - now uses min-w-0 instead of form-control
    const wrapper = container.querySelector('.min-w-0');
    expect(wrapper).toHaveClass('custom-class');
  });

  it('generates unique field IDs', () => {
    const { unmount } = render(ToggleField, {
      props: {
        label: 'Toggle 1',
        value: false,
        onUpdate: vi.fn(),
      },
    });

    const toggle1 = screen.getByRole('checkbox');
    const id1 = toggle1.getAttribute('id');

    unmount();

    render(ToggleField, {
      props: {
        label: 'Toggle 2',
        value: false,
        onUpdate: vi.fn(),
      },
    });

    const toggle2 = screen.getByRole('checkbox');
    const id2 = toggle2.getAttribute('id');

    expect(id1).not.toBe(id2);
    expect(id1).toMatch(/^toggle-/);
    expect(id2).toMatch(/^toggle-/);
  });

  it('has proper label association', () => {
    render(ToggleField, {
      props: {
        label: 'Test Toggle',
        value: false,
        onUpdate: vi.fn(),
      },
    });

    const toggle = screen.getByRole('checkbox');
    const label = screen.getByText('Test Toggle').closest('label');

    expect(label).toHaveAttribute('for', toggle.getAttribute('id'));
  });

  it('uses flexbox layout with proper alignment', () => {
    const { container } = render(ToggleField, {
      props: {
        label: 'Test Toggle',
        value: false,
        onUpdate: vi.fn(),
      },
    });

    // The wrapper now uses min-w-0 instead of form-control
    const wrapper = container.querySelector('.min-w-0');
    const flexContainer = wrapper?.querySelector('.flex');

    expect(flexContainer).toHaveClass('items-center', 'justify-between');
  });

  it('handles change event correctly', async () => {
    const onUpdate = vi.fn();

    render(ToggleField, {
      props: {
        label: 'Test Toggle',
        value: false,
        onUpdate,
      },
    });

    const toggle = screen.getByRole('checkbox');
    await fireEvent.change(toggle, { target: { checked: true } });

    expect(onUpdate).toHaveBeenCalledWith(true);
  });

  it('maintains toggle state correctly', async () => {
    const onUpdate = vi.fn();

    render(ToggleField, {
      props: {
        label: 'Test Toggle',
        value: false,
        onUpdate,
      },
    });

    const toggle = screen.getByRole('checkbox');

    // Initial state
    expect(toggle).not.toBeChecked();

    // Click to toggle
    await fireEvent.click(toggle);
    expect(onUpdate).toHaveBeenCalledWith(true);
  });

  it('sets aria-describedby when error is present', () => {
    render(ToggleField, {
      props: {
        label: 'Test Toggle',
        value: false,
        onUpdate: vi.fn(),
        error: 'Error message',
      },
    });

    const toggle = screen.getByRole('checkbox');
    const errorId = toggle.getAttribute('aria-describedby');

    expect(errorId).toBeTruthy();
    const errorElement = document.getElementById(errorId as string);
    expect(errorElement).toHaveTextContent('Error message');
  });

  it('sets aria-describedby when description is present without error', () => {
    render(ToggleField, {
      props: {
        label: 'Test Toggle',
        description: 'Toggle description',
        value: false,
        onUpdate: vi.fn(),
      },
    });

    const toggle = screen.getByRole('checkbox');
    const describedBy = toggle.getAttribute('aria-describedby');

    expect(describedBy).toMatch(/description$/);
  });

  it('passes through additional HTML attributes', () => {
    render(ToggleField, {
      props: {
        label: 'Test Toggle',
        value: false,
        onUpdate: vi.fn(),
        'data-testid': 'custom-test-id',
      },
    });

    const container = screen.getByTestId('custom-test-id');
    expect(container).toBeInTheDocument();
  });

  it('renders without description when not provided', () => {
    const { container } = render(ToggleField, {
      props: {
        label: 'Test Toggle',
        value: false,
        onUpdate: vi.fn(),
      },
    });

    // Should only have the main label, no description
    expect(screen.getByText('Test Toggle')).toBeInTheDocument();

    // Check that there's no element with description styling (text-xs opacity-70)
    const wrapper = container.querySelector('.min-w-0');
    const description = wrapper?.querySelector('.text-xs.opacity-70');
    expect(description).toBeNull();
  });
});
