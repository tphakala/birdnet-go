import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { ShieldCheck } from '@lucide/svelte';
import { expectNoA11yViolations } from '$lib/utils/axe-utils';
import ToggleField from './ToggleField.svelte';
import { opacityClasses } from '../../../../test/class-helpers';

const VARIANTS = ['default', 'card', 'inline'] as const;

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

  it.each(['default', 'inline'] as const)(
    'shows the not-allowed cursor on the %s label when disabled',
    variant => {
      render(ToggleField, {
        props: { label: 'Test Toggle', value: false, onUpdate: vi.fn(), disabled: true, variant },
      });

      const label = screen.getByText('Test Toggle').closest('label');
      expect(label).toHaveClass('cursor-not-allowed');
      expect(label).not.toHaveClass('cursor-pointer');
    }
  );

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

    if (!errorId) throw new Error('aria-describedby is missing');
    const errorElement = document.getElementById(errorId);
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

describe('ToggleField off-state track contrast', () => {
  it.each(VARIANTS)(
    'draws the %s variant off track with at least 3:1 contrast against its card',
    variant => {
      render(ToggleField, {
        props: { label: 'Test Toggle', value: false, onUpdate: vi.fn(), variant },
      });

      // WCAG 1.4.11: the track is the only cue for the off state. base-content at 60% is at
      // least 3:1 on base-100 and base-200 in both themes; base-300 is about 1.2:1.
      const toggle = screen.getByRole('checkbox');
      expect(toggle).toHaveClass('bg-[var(--color-base-content)]/60');
      expect(toggle).not.toHaveClass('bg-[var(--color-base-300)]');
      expect(toggle).toHaveClass('checked:bg-[var(--color-primary)]');
    }
  );
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
  it.each(VARIANTS)(
    'a disabled %s switch stays in the tab order, reports aria-disabled and ignores clicks',
    async variant => {
      const onUpdate = vi.fn();
      const user = userEvent.setup();
      render(ToggleField, {
        props: {
          label: 'Test Toggle',
          description: 'Blocked until setup is done',
          value: false,
          onUpdate,
          disabled: true,
          required: true,
          variant,
        },
      });
      const toggle = screen.getByRole('checkbox');

      // aria-disabled instead of native disabled, so Tab still reaches it and reads its description
      expect(toggle).not.toBeDisabled();
      expect(toggle).toHaveAttribute('aria-disabled', 'true');
      expect(toggle).toHaveAccessibleDescription('Blocked until setup is done');
      // Not required while disabled, as a natively disabled field is not, so it never blocks a submit
      expect(toggle).not.toBeRequired();
      await user.tab();
      expect(toggle).toHaveFocus();

      // Label text, the switch itself and Space all leave it off and report nothing
      await fireEvent.click(screen.getByText('Test Toggle'));
      await fireEvent.click(toggle);
      await user.keyboard(' ');
      expect(toggle).not.toBeChecked();
      expect(onUpdate).not.toHaveBeenCalled();
    }
  );

  it('keeps focus when it becomes disabled and toggles again once enabled', async () => {
    const onUpdate = vi.fn();
    const user = userEvent.setup();
    const props = { label: 'Test Toggle', value: false, onUpdate };
    const { rerender } = render(ToggleField, { props });
    const toggle = screen.getByRole('checkbox');

    await user.tab();
    expect(toggle).toHaveFocus();

    await rerender({ ...props, disabled: true });
    expect(toggle).toHaveAttribute('aria-disabled', 'true');
    expect(toggle).toHaveFocus();
    await user.keyboard(' ');
    expect(onUpdate).not.toHaveBeenCalled();
    expect(toggle).not.toBeChecked();

    await rerender({ ...props, disabled: false });
    expect(toggle).not.toHaveAttribute('aria-disabled');
    await user.keyboard(' ');
    expect(onUpdate).toHaveBeenCalledTimes(1);
    expect(onUpdate).toHaveBeenLastCalledWith(true);
  });

  it.each([false, true])(
    'a disabled inline switch keeps its state (%s) even when an ancestor stops the click',
    async value => {
      const onUpdate = vi.fn();
      render(ToggleField, {
        props: { label: 'Test Toggle', value, onUpdate, disabled: true, variant: 'inline' },
      });
      const toggle = screen.getByRole('checkbox');
      // A native listener between the switch and the app root keeps the click from reaching the
      // delegated handler that cancels it, so the change handler must hold the state itself
      toggle.closest('label')?.addEventListener('click', event => event.stopPropagation());

      await fireEvent.click(toggle);

      expect(toggle).toHaveProperty('checked', value);
      expect(onUpdate).not.toHaveBeenCalled();
    }
  );

  it('a disabled inline switch stays off even when an ancestor stops the change', async () => {
    const onUpdate = vi.fn();
    render(ToggleField, {
      props: { label: 'Test Toggle', value: false, onUpdate, disabled: true, variant: 'inline' },
    });
    const toggle = screen.getByRole('checkbox');
    // With the change kept from the delegated handler that restores the state, only the
    // cancelled click keeps the switch from flipping
    toggle.closest('label')?.addEventListener('change', event => event.stopPropagation());

    await fireEvent.click(toggle);

    expect(toggle).not.toBeChecked();
    expect(onUpdate).not.toHaveBeenCalled();
  });

  it.each(VARIANTS)(
    'a disabled %s switch keeps its state even when an ancestor stops both the click and the change',
    async variant => {
      const onUpdate = vi.fn();
      render(ToggleField, {
        props: { label: 'Test Toggle', value: false, onUpdate, disabled: true, variant },
      });
      const toggle = screen.getByRole('checkbox');
      const ancestor = toggle.parentElement;
      ancestor?.addEventListener('click', event => event.stopPropagation());
      ancestor?.addEventListener('change', event => event.stopPropagation());

      await fireEvent.click(toggle);

      expect(toggle).not.toBeChecked();
      expect(onUpdate).not.toHaveBeenCalled();
    }
  );

  it.each(['default', 'inline'] as const)(
    'a disabled %s switch dims through colour, not opacity, so its focus ring stays at full strength',
    variant => {
      render(ToggleField, {
        props: { label: 'Test Toggle', value: false, onUpdate: vi.fn(), disabled: true, variant },
      });
      const toggle = screen.getByRole('checkbox');

      expect(opacityClasses(toggle)).toEqual([]);
      expect(toggle).toHaveClass('cursor-not-allowed');
      expect(toggle).not.toHaveClass('cursor-pointer');
      expect(toggle).not.toHaveClass('bg-[var(--color-base-content)]/60');
      expect(toggle).not.toHaveClass('checked:bg-[var(--color-primary)]');
      expect(toggle).toHaveClass(
        'bg-[var(--color-base-content)]/30',
        'checked:bg-[var(--color-primary)]/50'
      );
      expect(toggle).toHaveClass(
        'focus-visible:outline-2',
        'focus-visible:outline-[var(--color-primary)]'
      );
    }
  );

  it.each(VARIANTS)(
    '%s variant draws the required mark and error text in the error text token',
    variant => {
      render(ToggleField, {
        props: {
          label: 'Show all',
          value: false,
          onUpdate: vi.fn(),
          required: true,
          error: 'Toggle error',
          variant,
        },
      });

      // --text-error holds 4.5:1 on base-100, 200 and 300; --color-error is for fills
      expect(screen.getByText('*')).toHaveClass('text-[var(--text-error)]');
      expect(screen.getByText('Toggle error')).toHaveClass('text-[var(--text-error)]');
    }
  );

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

  it('a disabled card shows only the not-allowed cursor, ignores hover and is dimmed once', () => {
    render(ToggleField, {
      props: { ...baseProps, value: false, onUpdate: vi.fn(), disabled: true },
    });
    const toggle = screen.getByRole('checkbox');
    const card = toggle.closest('label');
    const content = toggle.parentElement;

    // cn does not merge conflicting utilities, so a pointer class would win over not-allowed
    expect(card).toHaveClass('cursor-not-allowed');
    expect(card).not.toHaveClass('cursor-pointer');
    expect(card).not.toHaveClass('hover:border-[var(--border-300)]');
    expect(card).not.toHaveClass('border-[var(--border-200)]');
    // The card frame draws the focus ring, so it dims through its border colour and never
    // through element opacity, which would fade the ring
    expect(opacityClasses(card)).toEqual([]);
    // The content dims once, in a wrapper inside the frame that holds the icon, text and switch
    expect(content).not.toBe(card);
    expect(content).toHaveClass('opacity-50');
    expect(card).toContainElement(content);
    expect(content).toContainElement(card?.querySelector('svg') ?? null);
    expect(content).toContainElement(screen.getByText('Test Toggle'));
    expect(opacityClasses(toggle)).toEqual([]);
    // The wrapper already dims the switch, so it keeps the enabled track
    expect(toggle).toHaveClass(
      'bg-[var(--color-base-content)]/60',
      'checked:bg-[var(--color-primary)]'
    );
    expect(toggle).not.toHaveClass('bg-[var(--color-base-content)]/30');
  });

  it.each([
    [false, 'border-[var(--border-200)]/50', 'border-[var(--border-200)]'],
    [true, 'border-[var(--color-primary)]/50', 'border-[var(--color-primary)]'],
  ])(
    'a disabled card with value %s dims its frame through its border colour',
    (value, dimmed, full) => {
      render(ToggleField, { props: { ...baseProps, value, onUpdate: vi.fn(), disabled: true } });
      const card = screen.getByRole('checkbox').closest('label');

      expect(card).toHaveClass(dimmed);
      expect(card).not.toHaveClass(full);
    }
  );

  it('an enabled card keeps its content at full opacity', () => {
    render(ToggleField, { props: { ...baseProps, value: false, onUpdate: vi.fn() } });
    const toggle = screen.getByRole('checkbox');

    expect(toggle.parentElement).not.toHaveClass('opacity-50');
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

  it.each(['default', 'inline'] as const)(
    'keeps the %s variant ring on the switch itself',
    variant => {
      render(ToggleField, {
        props: { label: 'Test Toggle', value: false, onUpdate: vi.fn(), variant },
      });

      expect(screen.getByRole('checkbox')).toHaveClass(
        'focus-visible:outline-2',
        'focus-visible:outline-[var(--color-primary)]'
      );
    }
  );
});

describe('ToggleField inline variant', () => {
  it('inline variant: clicking the label text toggles', async () => {
    const onUpdate = vi.fn();
    const props = { label: 'Show all', variant: 'inline' as const, onUpdate };
    const { rerender } = render(ToggleField, { props: { ...props, value: false } });
    const toggle = screen.getByRole('checkbox');

    // The text and the switch share one label, so the gap between them is clickable too
    expect(toggle.closest('label')).toContainElement(screen.getByText('Show all'));

    await fireEvent.click(screen.getByText('Show all'));
    expect(onUpdate).toHaveBeenCalledTimes(1);
    expect(onUpdate).toHaveBeenLastCalledWith(true);

    // A click on the switch inside the label toggles once, not twice
    await rerender({ ...props, value: true });
    await fireEvent.click(toggle);
    expect(onUpdate).toHaveBeenCalledTimes(2);
    expect(onUpdate).toHaveBeenLastCalledWith(false);
  });

  it('inline variant sets regular weight on its label, description and error text', () => {
    render(ToggleField, {
      props: {
        label: 'Show all',
        description: 'Toggle description',
        error: 'Toggle error',
        value: false,
        onUpdate: vi.fn(),
        variant: 'inline',
      },
    });

    // Set explicitly so a semibold toolbar or table header does not make the text bold
    const regular = screen.getByText('Show all').closest('.font-normal');
    expect(regular).toContainElement(screen.getByText('Toggle description'));
    expect(regular).toContainElement(screen.getByText('Toggle error'));
  });

  it('inline variant label is a touch target taller than its row, under its content', () => {
    render(ToggleField, {
      props: { label: 'Show all', value: false, onUpdate: vi.fn(), variant: 'inline' },
    });

    // 2rem box plus a 0.375rem layer above and below: 2.75rem to touch, never sideways. The
    // layer sits behind the text and the switch inside the label's own stacking context
    // (isolate, -z-10), so pointer hits on them reach them and not the layer.
    expect(screen.getByRole('checkbox').closest('label')).toHaveClass(
      'relative',
      'isolate',
      'min-h-8',
      'after:absolute',
      'after:inset-x-0',
      'after:-inset-y-1.5',
      'after:-z-10',
      'cursor-pointer'
    );
  });

  it('inline variant draws its icon inside the label, before the label text', () => {
    render(ToggleField, {
      props: {
        label: 'Show all',
        value: false,
        onUpdate: vi.fn(),
        icon: ShieldCheck,
        variant: 'inline',
      },
    });

    const label = screen.getByRole('checkbox').closest('label');
    const icon = label?.querySelector('svg');
    expect(icon).not.toBeNull();
    // Icon first, then the text: the icon precedes the text in document order
    expect(icon?.compareDocumentPosition(screen.getByText('Show all'))).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING
    );
  });

  it('inline variant keeps its description and error clear of the hit layer', () => {
    // The label's hit layer reaches 0.375rem (mt-1.5) below it; text inside that band would
    // toggle the switch when clicked
    const { unmount } = render(ToggleField, {
      props: {
        label: 'Show all',
        description: 'Toggle description',
        error: 'Toggle error',
        value: false,
        onUpdate: vi.fn(),
        variant: 'inline',
      },
    });
    expect(screen.getByText('Toggle description')).toHaveClass('mt-1.5');
    unmount();

    // Without a description the error follows the label: mt-0.5 plus its py-1 is 0.375rem
    render(ToggleField, {
      props: {
        label: 'Show all',
        error: 'Toggle error',
        value: false,
        onUpdate: vi.fn(),
        variant: 'inline',
      },
    });
    expect(screen.getByText('Toggle error').parentElement).toHaveClass('mt-0.5', 'py-1');
  });

  it.each(['default', 'card'] as const)('the %s variant keeps its inherited weight', variant => {
    render(ToggleField, {
      props: {
        label: 'Test Toggle',
        value: false,
        onUpdate: vi.fn(),
        error: 'Toggle error',
        variant,
      },
    });

    expect(screen.getByText('Toggle error').closest('.font-normal')).toBeNull();
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
