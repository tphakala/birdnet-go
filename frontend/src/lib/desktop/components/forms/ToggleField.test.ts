import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { ShieldCheck } from '@lucide/svelte';
import { expectNoA11yViolations } from '$lib/utils/axe-utils';
import ToggleField from './ToggleField.svelte';

const VARIANTS = ['default', 'card'] as const;

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

    // Check that there's no element with description styling (help-text)
    const wrapper = container.querySelector('.min-w-0');
    const description = wrapper?.querySelector('.help-text');
    expect(description).toBeNull();
  });
});

describe('ToggleField naming and description', () => {
  it('aria-describedby points at the rendered description', () => {
    render(ToggleField, {
      props: {
        label: 'Test Toggle',
        description: 'Toggle description',
        value: false,
        onUpdate: vi.fn(),
      },
    });

    const describedBy = screen.getByRole('checkbox').getAttribute('aria-describedby');

    if (!describedBy) throw new Error('aria-describedby is missing');
    expect(document.getElementById(describedBy)).toHaveTextContent('Toggle description');
  });

  it.each(VARIANTS)(
    'names the switch with its label only and describes it with the description (%s)',
    variant => {
      render(ToggleField, {
        props: {
          label: 'Test Toggle',
          description: 'Toggle description',
          value: false,
          onUpdate: vi.fn(),
          variant,
        },
      });

      const toggle = screen.getByRole('checkbox');

      expect(toggle).toHaveAccessibleName('Test Toggle');
      expect(toggle).toHaveAccessibleDescription('Toggle description');
    }
  );
});

describe('ToggleField disabled, required and error state', () => {
  it.each(VARIANTS)('disables the switch and ignores clicks when disabled (%s)', async variant => {
    const onUpdate = vi.fn();
    render(ToggleField, {
      props: { label: 'Test Toggle', value: false, onUpdate, disabled: true, variant },
    });
    const toggle = screen.getByRole('checkbox');

    expect(toggle).toBeDisabled();
    await fireEvent.click(screen.getByText('Test Toggle'));

    expect(onUpdate).not.toHaveBeenCalled();
  });

  it('the default variant dims its own switch when disabled', () => {
    render(ToggleField, {
      props: { label: 'Test Toggle', value: false, onUpdate: vi.fn(), disabled: true },
    });

    expect(screen.getByRole('checkbox')).toHaveClass(
      'disabled:opacity-50',
      'disabled:cursor-not-allowed'
    );
  });

  it.each(VARIANTS)('marks the switch required and shows the asterisk (%s)', variant => {
    render(ToggleField, {
      props: { label: 'Test Toggle', value: false, onUpdate: vi.fn(), required: true, variant },
    });

    expect(screen.getByRole('checkbox')).toBeRequired();
    expect(screen.getByText('*')).toBeInTheDocument();
  });

  it.each(VARIANTS)('shows the error text and the error colour on the switch (%s)', variant => {
    render(ToggleField, {
      props: {
        label: 'Test Toggle',
        value: true,
        onUpdate: vi.fn(),
        error: 'Toggle error',
        variant,
      },
    });

    expect(screen.getByText('Toggle error')).toBeInTheDocument();
    expect(screen.getByRole('checkbox')).toHaveClass('checked:bg-[var(--color-error)]');
  });
});

describe('ToggleField error wiring', () => {
  it.each(VARIANTS)('keeps the description and adds the error text to it (%s)', variant => {
    render(ToggleField, {
      props: {
        label: 'Test Toggle',
        description: 'Toggle description',
        error: 'Toggle error',
        value: false,
        onUpdate: vi.fn(),
        variant,
      },
    });

    const toggle = screen.getByRole('checkbox');

    expect(toggle).toHaveAccessibleName('Test Toggle');
    expect(toggle).toHaveAccessibleDescription('Toggle description Toggle error');
  });

  it.each(VARIANTS)('marks the switch invalid only while an error is set (%s)', async variant => {
    const props = { label: 'Test Toggle', value: false, onUpdate: vi.fn(), variant };
    const { rerender } = render(ToggleField, { props });

    expect(screen.getByRole('checkbox')).not.toHaveAttribute('aria-invalid');

    await rerender({ ...props, error: 'Toggle error' });

    expect(screen.getByRole('checkbox')).toHaveAttribute('aria-invalid', 'true');
  });
});

describe('ToggleField card variant', () => {
  const baseProps = {
    label: 'Test Toggle',
    description: 'Toggle description',
    icon: ShieldCheck,
    variant: 'card' as const,
  };

  it('a click anywhere on the card toggles once', async () => {
    const onUpdate = vi.fn();
    const { rerender } = render(ToggleField, { props: { ...baseProps, value: false, onUpdate } });

    await fireEvent.click(screen.getByText('Toggle description'));
    expect(onUpdate).toHaveBeenCalledTimes(1);
    expect(onUpdate).toHaveBeenLastCalledWith(true);

    await rerender({ ...baseProps, value: true, onUpdate });
    await fireEvent.click(screen.getByText('Test Toggle'));
    expect(onUpdate).toHaveBeenCalledTimes(2);
    expect(onUpdate).toHaveBeenLastCalledWith(false);

    await rerender({ ...baseProps, value: false, onUpdate });
    await fireEvent.click(screen.getByRole('checkbox'));
    expect(onUpdate).toHaveBeenCalledTimes(3);
    expect(onUpdate).toHaveBeenLastCalledWith(true);
  });

  it('shows the checked state on the card and colours the icon primary only when on', async () => {
    const onUpdate = vi.fn();
    const { rerender } = render(ToggleField, { props: { ...baseProps, value: false, onUpdate } });
    const card = screen.getByRole('checkbox').closest('label');
    const icon = () => card?.querySelector('svg');

    expect(card).toHaveClass('border-[var(--border-200)]');
    expect(card).not.toHaveClass('border-[var(--color-primary)]');
    expect(icon()).not.toHaveClass('text-[var(--color-primary)]');

    await rerender({ ...baseProps, value: true, onUpdate });

    expect(card).toHaveClass('border-[var(--color-primary)]', 'bg-[var(--color-primary)]/5');
    expect(icon()).toHaveClass('text-[var(--color-primary)]');
  });

  it('draws the focus ring on the card, not on the switch', () => {
    render(ToggleField, { props: { ...baseProps, value: false, onUpdate: vi.fn() } });
    const toggle = screen.getByRole('checkbox');

    expect(toggle.closest('label')).toHaveClass(
      'has-[input:focus-visible]:outline-2',
      'has-[input:focus-visible]:outline-[var(--color-primary)]'
    );
    expect(toggle).not.toHaveClass('focus-visible:outline-2');
    // The browser's own ring on the switch would double the card ring
    expect(toggle).toHaveClass('focus-visible:outline-none');
  });

  it('a disabled card shows only the not-allowed cursor, is dimmed once and ignores hover', () => {
    render(ToggleField, {
      props: { ...baseProps, value: false, onUpdate: vi.fn(), disabled: true },
    });
    const toggle = screen.getByRole('checkbox');
    const card = toggle.closest('label');

    // cn does not merge conflicting utilities, so a pointer class would win over not-allowed
    expect(card).toHaveClass('opacity-50', 'cursor-not-allowed');
    expect(card).not.toHaveClass('cursor-pointer');
    expect(card).not.toHaveClass('hover:border-[var(--border-300)]');
    // The card is already dimmed, so the switch must not dim a second time
    expect(toggle).not.toHaveClass('disabled:opacity-50');
  });

  it('an enabled card shows the pointer cursor and only an unchecked one reacts to hover', async () => {
    const onUpdate = vi.fn();
    const { rerender } = render(ToggleField, { props: { ...baseProps, value: false, onUpdate } });
    const card = screen.getByRole('checkbox').closest('label');

    expect(card).toHaveClass('cursor-pointer', 'hover:border-[var(--border-300)]');
    expect(card).not.toHaveClass('cursor-not-allowed');

    await rerender({ ...baseProps, value: true, onUpdate });

    expect(card).toHaveClass('cursor-pointer', 'border-[var(--color-primary)]');
    // A checked card keeps its primary border on hover
    expect(card).not.toHaveClass('hover:border-[var(--border-300)]');
  });

  it('keeps the default variant ring on the switch itself', () => {
    render(ToggleField, { props: { label: 'Test Toggle', value: false, onUpdate: vi.fn() } });

    expect(screen.getByRole('checkbox')).toHaveClass('focus-visible:outline-2');
  });
});

describe('ToggleField Accessibility', () => {
  it.each(VARIANTS)('has no axe violations (%s)', async variant => {
    for (const value of [false, true]) {
      const { container, unmount } = render(ToggleField, {
        props: {
          label: 'Test Toggle',
          description: 'Toggle description',
          icon: ShieldCheck,
          value,
          onUpdate: vi.fn(),
          variant,
        },
      });

      await expect(expectNoA11yViolations(container)).resolves.toBeUndefined();
      unmount();
    }
  });
});
