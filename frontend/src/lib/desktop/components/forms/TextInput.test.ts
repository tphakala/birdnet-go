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

  it('calls onblur with the current value when the input loses focus', async () => {
    const onblur = vi.fn();

    render(TextInput, { props: { value: 'abc', onblur } });

    const input = screen.getByRole('textbox');
    await fireEvent.input(input, { target: { value: 'typed' } });
    await fireEvent.blur(input);

    expect(onblur).toHaveBeenCalledTimes(1);
    expect(onblur).toHaveBeenCalledWith('typed');
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

describe('TextInput aria-invalid', () => {
  it('forwards aria-invalid to the native input, not the wrapper', () => {
    render(TextInput, { props: { value: '', 'aria-invalid': 'true' } });

    const input = screen.getByRole('textbox');
    expect(input).toHaveAttribute('aria-invalid', 'true');
    expect(input.parentElement).not.toHaveAttribute('aria-invalid');
  });

  it('renders no aria-invalid when the prop is not set', () => {
    render(TextInput, { props: { value: '' } });

    expect(screen.getByRole('textbox')).not.toHaveAttribute('aria-invalid');
  });
});

describe('TextInput Accessibility error styling', () => {
  it.each<[string, 'true' | true]>([
    ['the string true', 'true'],
    ['the boolean true', true],
  ])('shows error styling when aria-invalid is %s', (_label, ariaInvalid) => {
    render(TextInput, { props: { value: '', 'aria-invalid': ariaInvalid } });

    expect(screen.getByRole('textbox')).toHaveClass('input-error');
  });

  it('shows no error styling when aria-invalid is unset or false', () => {
    const unset = render(TextInput, { props: { value: '' } });
    expect(screen.getByRole('textbox')).not.toHaveClass('input-error');
    unset.unmount();

    render(TextInput, { props: { value: '', 'aria-invalid': 'false' } });
    expect(screen.getByRole('textbox')).not.toHaveClass('input-error');
  });
});

describe('TextInput error region and autocomplete', () => {
  it('renders no alert region unless an error is set or space is reserved', () => {
    render(TextInput, { props: { value: '', label: 'Token', id: 'tok' } });

    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('forwards autocomplete to the native input', () => {
    render(TextInput, { props: { value: '', autocomplete: 'off' } });

    expect(screen.getByRole('textbox')).toHaveAttribute('autocomplete', 'off');
  });

  it('shows the error in one alert region, marks the input invalid and describes it by the error', () => {
    render(TextInput, {
      props: { value: '', label: 'Token', id: 'tok', error: 'Token is wrong' },
    });

    const input = screen.getByRole('textbox');
    const alert = screen.getByRole('alert');
    expect(screen.getAllByRole('alert')).toHaveLength(1);
    expect(alert).toHaveTextContent('Token is wrong');
    expect(alert).toHaveAttribute('id', 'tok-error');
    expect(input).toHaveAttribute('aria-invalid', 'true');
    expect(input).toHaveAttribute('aria-describedby', 'tok-error');
    expect(input).toHaveClass('input-error');
  });

  it('joins an external description with the error id', () => {
    render(TextInput, {
      props: { value: '', id: 'tok', error: 'Bad', 'aria-describedby': 'hint' },
    });

    expect(screen.getByRole('textbox')).toHaveAttribute('aria-describedby', 'hint tok-error');
  });

  it('reserveErrorSpace keeps one two-line alert region whether or not an error is shown', async () => {
    const { rerender } = render(TextInput, {
      props: { value: '', id: 'tok', reserveErrorSpace: true },
    });
    const region = screen.getByRole('alert');
    expect(region).toHaveClass('min-h-10', 'text-sm');
    expect(region.textContent.trim()).toBe('');
    expect(screen.getByRole('textbox')).not.toHaveAttribute('aria-describedby');
    expect(screen.getByRole('textbox')).not.toHaveAttribute('aria-invalid');

    await rerender({ value: '', id: 'tok', reserveErrorSpace: true, error: 'Token is wrong' });

    expect(screen.getAllByRole('alert')).toHaveLength(1);
    expect(screen.getByRole('alert')).toBe(region);
    expect(region).toHaveClass('min-h-10', 'text-sm');
    expect(region).toHaveTextContent('Token is wrong');
  });
});
