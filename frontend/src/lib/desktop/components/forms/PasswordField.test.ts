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
    // Note: Uses 'change' event instead of 'input' to match Svelte's event handling
    // PasswordField component binds to 'change' event for proper validation timing
    await fireEvent.change(input, { target: { value: 'newpassword' } });

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
});
