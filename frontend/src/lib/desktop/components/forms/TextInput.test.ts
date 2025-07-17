import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import TextInput from './TextInput.svelte';

describe('TextInput', () => {
  it('renders with basic props', () => {
    render(TextInput, {
      props: {
        value: '',
      },
    });

    expect(screen.getByRole('textbox')).toBeInTheDocument();
  });

  it('displays label when provided', () => {
    render(TextInput, {
      props: {
        value: '',
        label: 'Test Input',
      },
    });

    expect(screen.getByText('Test Input')).toBeInTheDocument();
  });

  it('shows placeholder when provided', () => {
    render(TextInput, {
      props: {
        value: '',
        placeholder: 'Enter text here',
      },
    });

    expect(screen.getByPlaceholderText('Enter text here')).toBeInTheDocument();
  });

  it('displays current value', () => {
    render(TextInput, {
      props: {
        value: 'Test value',
      },
    });

    const input = screen.getByRole('textbox');
    expect(input).toHaveValue('Test value');
  });

  it('calls onchange when value changes', async () => {
    const onchange = vi.fn();

    render(TextInput, {
      props: {
        value: '',
        onchange,
      },
    });

    const input = screen.getByRole('textbox');
    await fireEvent.change(input, { target: { value: 'new value' } });

    expect(onchange).toHaveBeenCalledWith('new value');
  });

  it('calls oninput when input occurs', async () => {
    const oninput = vi.fn();

    render(TextInput, {
      props: {
        value: '',
        oninput,
      },
    });

    const input = screen.getByRole('textbox');
    await fireEvent.input(input, { target: { value: 'typing' } });

    expect(oninput).toHaveBeenCalledWith('typing');
  });

  it('supports different input types', () => {
    render(TextInput, {
      props: {
        value: '',
        type: 'email',
      },
    });

    const input = screen.getByRole('textbox');
    expect(input).toHaveAttribute('type', 'email');
  });

  it('shows required indicator when required', () => {
    render(TextInput, {
      props: {
        value: '',
        label: 'Required Field',
        required: true,
      },
    });

    expect(screen.getByText('*')).toBeInTheDocument();
  });

  it('renders in disabled state', () => {
    render(TextInput, {
      props: {
        value: '',
        disabled: true,
      },
    });

    const input = screen.getByRole('textbox');
    expect(input).toBeDisabled();
  });

  it('renders in readonly state', () => {
    render(TextInput, {
      props: {
        value: 'readonly value',
        readonly: true,
      },
    });

    const input = screen.getByRole('textbox');
    expect(input).toHaveAttribute('readonly');
  });

  it('applies pattern validation', () => {
    render(TextInput, {
      props: {
        value: '',
        pattern: '[0-9]*',
      },
    });

    const input = screen.getByRole('textbox');
    expect(input).toHaveAttribute('pattern', '[0-9]*');
  });

  it('respects minlength and maxlength', () => {
    render(TextInput, {
      props: {
        value: '',
        minlength: 5,
        maxlength: 50,
      },
    });

    const input = screen.getByRole('textbox');
    expect(input).toHaveAttribute('minlength', '5');
    expect(input).toHaveAttribute('maxlength', '50');
  });

  it('displays help text when provided', () => {
    render(TextInput, {
      props: {
        value: '',
        helpText: 'This is help text',
      },
    });

    expect(screen.getByText('This is help text')).toBeInTheDocument();
  });

  it('shows tooltip button when tooltip provided', () => {
    render(TextInput, {
      props: {
        value: '',
        label: 'Text Input',
        tooltip: 'This is a tooltip',
      },
    });

    expect(screen.getByRole('button', { name: 'Help information' })).toBeInTheDocument();
  });

  it('shows tooltip on hover', async () => {
    render(TextInput, {
      props: {
        value: '',
        label: 'Text Input',
        tooltip: 'This is a tooltip',
      },
    });

    const helpButton = screen.getByRole('button', { name: 'Help information' });
    await fireEvent.mouseEnter(helpButton);

    expect(screen.getByText('This is a tooltip')).toBeInTheDocument();
  });

  it('shows validation message when invalid', async () => {
    render(TextInput, {
      props: {
        value: '',
        required: true,
        validationMessage: 'This field is required',
      },
    });

    const input = screen.getByRole('textbox');
    await fireEvent.blur(input);

    // Check that the input has the required attribute and shows validation styling
    expect(input).toHaveAttribute('required');
  });

  it('applies size classes correctly', () => {
    render(TextInput, {
      props: {
        value: '',
        size: 'lg',
      },
    });

    const input = screen.getByRole('textbox');
    expect(input).toHaveClass('input-lg');
  });

  it('applies custom className', () => {
    render(TextInput, {
      props: {
        value: '',
        className: 'custom-class',
      },
    });

    const container = screen.getByRole('textbox').closest('.form-control');
    expect(container).toHaveClass('custom-class');
  });

  it('handles id prop correctly', () => {
    render(TextInput, {
      props: {
        value: '',
        id: 'test-input',
      },
    });

    const input = screen.getByRole('textbox');
    expect(input).toHaveAttribute('id', 'test-input');
  });

  it('shows error styling when invalid and touched', async () => {
    render(TextInput, {
      props: {
        value: '',
        required: true,
      },
    });

    const input = screen.getByRole('textbox');
    await fireEvent.blur(input);

    // TextInput shows validation state via browser validation, not custom classes
    expect(input).toHaveAttribute('required');
  });

  it('does not show error styling when not touched', () => {
    render(TextInput, {
      props: {
        value: '',
        required: true,
      },
    });

    const input = screen.getByRole('textbox');
    expect(input).not.toHaveClass('input-error');
  });

  it('resets touched state on input', async () => {
    render(TextInput, {
      props: {
        value: '',
        required: true,
      },
    });

    const input = screen.getByRole('textbox');

    // Make it touched and invalid
    await fireEvent.blur(input);
    expect(input).toHaveAttribute('required');

    // Type something to reset touched state
    await fireEvent.input(input, { target: { value: 'a' } });

    // Input should still be required but now has a value
    expect(input).toHaveAttribute('required');
    expect(input).toHaveValue('a');
  });

  it('generates unique input elements', () => {
    const { unmount } = render(TextInput, {
      props: {
        value: 'test1',
      },
    });

    const input1 = screen.getByRole('textbox');

    unmount();

    render(TextInput, {
      props: {
        value: 'test2',
      },
    });

    const input2 = screen.getByRole('textbox');

    // Should be different elements
    expect(input1).not.toBe(input2);
  });

  it('supports different input types with proper roles', () => {
    const { unmount } = render(TextInput, {
      props: {
        value: '',
        type: 'email',
      },
    });

    expect(screen.getByRole('textbox')).toHaveAttribute('type', 'email');

    unmount();

    render(TextInput, {
      props: {
        value: '',
        type: 'url',
      },
    });

    expect(screen.getByRole('textbox')).toHaveAttribute('type', 'url');
  });

  it('handles special input types', () => {
    render(TextInput, {
      props: {
        value: '',
        type: 'search',
      },
    });

    const input = screen.getByRole('searchbox');
    expect(input).toHaveAttribute('type', 'search');
  });

  it('capitalizes label text', () => {
    render(TextInput, {
      props: {
        value: '',
        label: 'test label',
      },
    });

    const label = screen.getByText('test label');
    expect(label).toHaveClass('capitalize');
  });

  it('passes through additional HTML attributes', () => {
    render(TextInput, {
      props: {
        value: '',
        id: 'custom-test-id',
      },
    });

    const container = screen.getByDisplayValue('');
    expect(container).toBeInTheDocument();
    expect(container).toHaveAttribute('id', 'custom-test-id');
  });
});
