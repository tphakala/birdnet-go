import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import PasswordField from './PasswordField.svelte';

describe('PasswordField', () => {
  it('renders with basic props', () => {
    render(PasswordField, {
      props: {
        label: 'Password',
        value: '',
        onUpdate: vi.fn(),
      },
    });

    expect(screen.getByText('Password')).toBeInTheDocument();
    expect(screen.getByLabelText('Password')).toBeInTheDocument();
  });

  it('renders as password type by default', () => {
    render(PasswordField, {
      props: {
        label: 'Password',
        value: '',
        onUpdate: vi.fn(),
      },
    });

    const input = screen.getByLabelText('Password');
    expect(input).toHaveAttribute('type', 'password');
  });

  it('displays the current value', () => {
    render(PasswordField, {
      props: {
        label: 'Password',
        value: 'test123',
        onUpdate: vi.fn(),
      },
    });

    const input = screen.getByLabelText('Password');
    expect(input).toHaveValue('test123');
  });

  it('calls onUpdate when value changes', async () => {
    const onUpdate = vi.fn();

    render(PasswordField, {
      props: {
        label: 'Password',
        value: '',
        onUpdate,
      },
    });

    const input = screen.getByLabelText('Password');
    await fireEvent.input(input, { target: { value: 'newpassword' } });

    expect(onUpdate).toHaveBeenCalledWith('newpassword');
  });

  it('shows password toggle button when allowReveal is true', () => {
    render(PasswordField, {
      props: {
        label: 'Password',
        value: '',
        onUpdate: vi.fn(),
        allowReveal: true,
      },
    });

    expect(screen.getByRole('button', { name: 'Show password' })).toBeInTheDocument();
  });

  it('hides password toggle button when allowReveal is false', () => {
    render(PasswordField, {
      props: {
        label: 'Password',
        value: '',
        onUpdate: vi.fn(),
        allowReveal: false,
      },
    });

    expect(screen.queryByRole('button', { name: 'Show password' })).not.toBeInTheDocument();
  });

  it('toggles password visibility when toggle button is clicked', async () => {
    render(PasswordField, {
      props: {
        label: 'Password',
        value: 'secret123',
        onUpdate: vi.fn(),
        allowReveal: true,
      },
    });

    const input = screen.getByLabelText('Password');
    const toggleButton = screen.getByRole('button', { name: 'Show password' });

    expect(input).toHaveAttribute('type', 'password');

    await fireEvent.click(toggleButton);

    expect(input).toHaveAttribute('type', 'text');
    expect(screen.getByRole('button', { name: 'Hide password' })).toBeInTheDocument();
  });

  it('shows password strength when showStrength is true', () => {
    render(PasswordField, {
      props: {
        label: 'Password',
        value: 'Test123!',
        onUpdate: vi.fn(),
        showStrength: true,
      },
    });

    expect(screen.getByText('Password Strength:')).toBeInTheDocument();
  });

  it('calculates password strength correctly for strong password', () => {
    render(PasswordField, {
      props: {
        label: 'Password',
        value: 'StrongP@ssw0rd!',
        onUpdate: vi.fn(),
        showStrength: true,
      },
    });

    expect(screen.getByText('Strong')).toBeInTheDocument();
  });

  it('calculates password strength correctly for weak password', () => {
    render(PasswordField, {
      props: {
        label: 'Password',
        value: 'weak',
        onUpdate: vi.fn(),
        showStrength: true,
      },
    });

    expect(screen.getByText('Weak')).toBeInTheDocument();
  });

  it('shows password strength suggestions for weak password', () => {
    render(PasswordField, {
      props: {
        label: 'Password',
        value: 'weak',
        onUpdate: vi.fn(),
        showStrength: true,
      },
    });

    expect(screen.getByText('Suggestions:')).toBeInTheDocument();
    expect(screen.getByText('At least 8 characters')).toBeInTheDocument();
  });

  it('shows required indicator when required', () => {
    render(PasswordField, {
      props: {
        label: 'Password',
        value: '',
        onUpdate: vi.fn(),
        required: true,
      },
    });

    expect(screen.getByText('*')).toBeInTheDocument();
  });

  it('renders in disabled state', () => {
    render(PasswordField, {
      props: {
        label: 'Password',
        value: '',
        onUpdate: vi.fn(),
        disabled: true,
      },
    });

    const input = screen.getByLabelText('Password');
    expect(input).toBeDisabled();
  });

  it('disables toggle button when field is disabled', () => {
    render(PasswordField, {
      props: {
        label: 'Password',
        value: '',
        onUpdate: vi.fn(),
        disabled: true,
        allowReveal: true,
      },
    });

    const toggleButton = screen.getByRole('button', { name: 'Show password' });
    expect(toggleButton).toBeDisabled();
  });

  it('shows error message when provided', () => {
    render(PasswordField, {
      props: {
        label: 'Password',
        value: '',
        onUpdate: vi.fn(),
        error: 'Password is required',
      },
    });

    expect(screen.getByText('Password is required')).toBeInTheDocument();
  });

  it('applies error styling when error is present', () => {
    render(PasswordField, {
      props: {
        label: 'Password',
        value: '',
        onUpdate: vi.fn(),
        error: 'Password is required',
      },
    });

    const input = screen.getByLabelText('Password');
    expect(input).toHaveClass('input-error');
  });

  it('shows placeholder when provided', () => {
    render(PasswordField, {
      props: {
        label: 'Password',
        value: '',
        onUpdate: vi.fn(),
        placeholder: 'Enter your password',
      },
    });

    const input = screen.getByLabelText('Password');
    expect(input).toHaveAttribute('placeholder', 'Enter your password');
  });

  it('displays help text when provided', () => {
    render(PasswordField, {
      props: {
        label: 'Password',
        value: '',
        onUpdate: vi.fn(),
        helpText: 'Password must be strong',
      },
    });

    expect(screen.getByText('Password must be strong')).toBeInTheDocument();
  });

  it('applies custom className', () => {
    render(PasswordField, {
      props: {
        label: 'Password',
        value: '',
        onUpdate: vi.fn(),
        className: 'custom-class',
      },
    });

    const container = screen.getByText('Password').closest('.custom-class');
    expect(container).toBeInTheDocument();
  });

  it('uses correct autocomplete attribute', () => {
    render(PasswordField, {
      props: {
        label: 'Password',
        value: '',
        onUpdate: vi.fn(),
        autocomplete: 'new-password',
      },
    });

    const input = screen.getByLabelText('Password');
    expect(input).toHaveAttribute('autocomplete', 'new-password');
  });

  it('generates unique field IDs', () => {
    // Render two components simultaneously to verify unique IDs
    const container = document.createElement('div');
    document.body.appendChild(container);

    const div1 = document.createElement('div');
    const div2 = document.createElement('div');
    container.appendChild(div1);
    container.appendChild(div2);

    render(PasswordField, {
      target: div1,
      props: {
        label: 'Password 1',
        value: '',
        onUpdate: vi.fn(),
      },
    });

    render(PasswordField, {
      target: div2,
      props: {
        label: 'Password 2',
        value: '',
        onUpdate: vi.fn(),
      },
    });

    const input1 = screen.getByLabelText('Password 1');
    const input2 = screen.getByLabelText('Password 2');
    const id1 = input1.getAttribute('id');
    const id2 = input2.getAttribute('id');

    expect(id1).not.toBe(id2);
    expect(id1).toMatch(/^password-field-/);
    expect(id2).toMatch(/^password-field-/);

    document.body.removeChild(container);
  });

  describe('validation attributes', () => {
    it('marks the input invalid and links the error text', () => {
      render(PasswordField, {
        props: { label: 'Token', value: 'abc', onUpdate: vi.fn(), error: 'Token is wrong' },
      });

      const input = screen.getByLabelText('Token');
      const message = screen.getByText('Token is wrong');
      expect(input).toHaveAttribute('aria-invalid', 'true');
      expect(message.id).not.toBe('');
      expect(input.getAttribute('aria-describedby')?.split(' ')).toContain(message.id);
      expect(message).toHaveClass('text-[var(--text-error)]');
    });

    it('passes aria-describedby and aria-invalid to the input, not the wrapper', () => {
      const { container } = render(PasswordField, {
        props: {
          label: 'Token',
          value: '',
          onUpdate: vi.fn(),
          'aria-describedby': 'outside-hint',
          'aria-invalid': 'true',
        },
      });

      const input = screen.getByLabelText('Token');
      expect(input).toHaveAttribute('aria-describedby', 'outside-hint');
      expect(input).toHaveAttribute('aria-invalid', 'true');
      const wrapper = container.querySelector('.form-control');
      expect(wrapper).not.toHaveAttribute('aria-describedby');
      expect(wrapper).not.toHaveAttribute('aria-invalid');
    });

    it('joins a passed aria-describedby with the error id', () => {
      render(PasswordField, {
        props: {
          label: 'Token',
          value: '',
          onUpdate: vi.fn(),
          error: 'Token is wrong',
          'aria-describedby': 'outside-hint',
        },
      });

      const ids = screen.getByLabelText('Token').getAttribute('aria-describedby')?.split(' ');
      expect(ids).toContain('outside-hint');
      expect(ids).toContain(screen.getByText('Token is wrong').id);
    });

    it('renders the error outside .label-text-alt so its opacity does not lower the contrast', () => {
      render(PasswordField, {
        props: { label: 'Token', value: 'abc', onUpdate: vi.fn(), error: 'Token is wrong' },
      });

      const message = screen.getByText('Token is wrong');
      expect(message.closest('.label-text-alt')).toBeNull();
      expect(message.classList.contains('label-text-alt')).toBe(false);
    });

    it('cancels the flex gap while the alert region is empty and spaces the error normally', async () => {
      const { rerender } = render(PasswordField, {
        props: { label: 'Token', value: '', onUpdate: vi.fn() },
      });
      const region = screen.getByRole('alert');
      expect(region).toHaveClass('-mt-1');
      expect(region).not.toHaveClass('py-1');

      await rerender({ label: 'Token', value: '', onUpdate: vi.fn(), error: 'Token is wrong' });

      expect(region).not.toHaveClass('-mt-1');
      expect(region).toHaveClass('py-1');
    });

    it('reserveErrorSpace keeps two lines for the alert whether or not an error is shown', async () => {
      const { rerender } = render(PasswordField, {
        props: { label: 'Token', value: '', onUpdate: vi.fn(), reserveErrorSpace: true },
      });
      const region = screen.getByRole('alert');
      expect(region).toHaveClass('min-h-10', 'text-sm');
      expect(region).not.toHaveClass('-mt-1');

      await rerender({
        label: 'Token',
        value: '',
        onUpdate: vi.fn(),
        reserveErrorSpace: true,
        error: 'Token is wrong',
      });

      expect(screen.getAllByRole('alert')).toHaveLength(1);
      expect(region).toHaveClass('min-h-10', 'text-sm');
      expect(region).not.toHaveClass('-mt-1');
      expect(region).toHaveTextContent('Token is wrong');
    });

    it('keeps one alert region whose text changes when the error appears', async () => {
      const { rerender } = render(PasswordField, {
        props: { label: 'Token', value: '', onUpdate: vi.fn() },
      });
      const region = screen.getByRole('alert');
      expect(region.textContent.trim()).toBe('');
      expect(region).not.toHaveAttribute('aria-live');

      await rerender({ label: 'Token', value: '', onUpdate: vi.fn(), error: 'Token is wrong' });

      expect(screen.getByRole('alert')).toBe(region);
      expect(region).toHaveTextContent('Token is wrong');
    });

    it('links the input to the alert region only while an error is shown', async () => {
      const { rerender } = render(PasswordField, {
        props: { label: 'Token', value: '', onUpdate: vi.fn() },
      });
      expect(screen.getByLabelText('Token')).not.toHaveAttribute('aria-describedby');

      await rerender({ label: 'Token', value: '', onUpdate: vi.fn(), error: 'Token is wrong' });

      expect(screen.getByLabelText('Token').getAttribute('aria-describedby')).toBe(
        screen.getByRole('alert').id
      );
    });

    it('calls onblur with the value', async () => {
      const onblur = vi.fn();
      render(PasswordField, {
        props: { label: 'Token', value: 'abc', onUpdate: vi.fn(), onblur },
      });

      await fireEvent.blur(screen.getByLabelText('Token'));

      expect(onblur).toHaveBeenCalledWith('abc');
    });

    it('shows the error border when aria-invalid is set', () => {
      render(PasswordField, {
        props: { label: 'Token', value: '', onUpdate: vi.fn(), 'aria-invalid': 'true' },
      });

      expect(screen.getByLabelText('Token')).toHaveClass('input-error');
    });

    it.each([[false], ['false']] as const)(
      'shows no error border when aria-invalid is %s',
      ariaInvalid => {
        render(PasswordField, {
          props: { label: 'Token', value: '', onUpdate: vi.fn(), 'aria-invalid': ariaInvalid },
        });

        expect(screen.getByLabelText('Token')).not.toHaveClass('input-error');
      }
    );

    it('sets no validation attributes without an error', () => {
      render(PasswordField, { props: { label: 'Token', value: '', onUpdate: vi.fn() } });

      const input = screen.getByLabelText('Token');
      expect(input).not.toHaveAttribute('aria-invalid');
      expect(input).not.toHaveAttribute('aria-describedby');
      expect(input).not.toHaveClass('input-error');
    });
  });
});
