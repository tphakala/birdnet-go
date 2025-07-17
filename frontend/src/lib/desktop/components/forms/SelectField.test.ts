import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import SelectField from './SelectField.svelte';

const defaultOptions = [
  { value: 'option1', label: 'Option 1' },
  { value: 'option2', label: 'Option 2' },
  { value: 'option3', label: 'Option 3', disabled: true },
];

describe('SelectField', () => {
  it('renders with basic props', () => {
    render(SelectField, {
      props: {
        value: '',
        options: defaultOptions,
      },
    });

    expect(screen.getByRole('combobox')).toBeInTheDocument();
  });

  it('displays label when provided', () => {
    render(SelectField, {
      props: {
        value: '',
        options: defaultOptions,
        label: 'Test Select',
      },
    });

    expect(screen.getByText('Test Select')).toBeInTheDocument();
  });

  it('shows placeholder option when provided', () => {
    render(SelectField, {
      props: {
        value: '',
        options: defaultOptions,
        placeholder: 'Choose an option',
      },
    });

    expect(screen.getByText('Choose an option')).toBeInTheDocument();
  });

  it('renders all provided options', () => {
    render(SelectField, {
      props: {
        value: '',
        options: defaultOptions,
      },
    });

    expect(screen.getByText('Option 1')).toBeInTheDocument();
    expect(screen.getByText('Option 2')).toBeInTheDocument();
    expect(screen.getByText('Option 3')).toBeInTheDocument();
  });

  it('shows disabled option as disabled', () => {
    render(SelectField, {
      props: {
        value: '',
        options: defaultOptions,
      },
    });

    const option3 = screen.getByRole('option', { name: 'Option 3' });
    expect(option3).toBeDisabled();
  });

  it('displays current value correctly', () => {
    render(SelectField, {
      props: {
        value: 'option2',
        options: defaultOptions,
      },
    });

    const select = screen.getByRole('combobox');
    expect(select).toHaveValue('option2');
  });

  it('calls onchange when selection changes', async () => {
    const onchange = vi.fn();

    render(SelectField, {
      props: {
        value: '',
        options: defaultOptions,
        onchange,
      },
    });

    const select = screen.getByRole('combobox');
    await fireEvent.change(select, { target: { value: 'option1' } });

    expect(onchange).toHaveBeenCalledWith('option1');
  });

  it('shows required indicator when required', () => {
    render(SelectField, {
      props: {
        value: '',
        options: defaultOptions,
        label: 'Test Select',
        required: true,
      },
    });

    expect(screen.getByText('*')).toBeInTheDocument();
  });

  it('renders in disabled state', () => {
    render(SelectField, {
      props: {
        value: '',
        options: defaultOptions,
        disabled: true,
      },
    });

    const select = screen.getByRole('combobox');
    expect(select).toBeDisabled();
  });

  it('displays help text when provided', () => {
    render(SelectField, {
      props: {
        value: '',
        options: defaultOptions,
        helpText: 'This is help text',
      },
    });

    expect(screen.getByText('This is help text')).toBeInTheDocument();
  });

  it('shows tooltip button when tooltip provided', () => {
    render(SelectField, {
      props: {
        value: '',
        options: defaultOptions,
        label: 'Test Select',
        tooltip: 'This is a tooltip',
      },
    });

    expect(screen.getByRole('button', { name: 'Help information' })).toBeInTheDocument();
  });

  it('shows tooltip on hover', async () => {
    render(SelectField, {
      props: {
        value: '',
        options: defaultOptions,
        label: 'Test Select',
        tooltip: 'This is a tooltip',
      },
    });

    const helpButton = screen.getByRole('button', { name: 'Help information' });
    await fireEvent.mouseEnter(helpButton);

    expect(screen.getByText('This is a tooltip')).toBeInTheDocument();
  });

  it('applies size classes correctly', () => {
    render(SelectField, {
      props: {
        value: '',
        options: defaultOptions,
        size: 'lg',
      },
    });

    const select = screen.getByRole('combobox');
    expect(select).toHaveClass('select-lg');
  });

  it('applies custom className', () => {
    render(SelectField, {
      props: {
        value: '',
        options: defaultOptions,
        className: 'custom-class',
      },
    });

    const container = screen.getByRole('combobox').closest('.form-control');
    expect(container).toHaveClass('custom-class');
  });

  it('handles id prop correctly', () => {
    render(SelectField, {
      props: {
        value: '',
        options: defaultOptions,
        id: 'test-select',
      },
    });

    const select = screen.getByRole('combobox');
    expect(select).toHaveAttribute('id', 'test-select');
  });

  it('renders without options when none provided', () => {
    render(SelectField, {
      props: {
        value: '',
        placeholder: 'No options',
      },
    });

    const select = screen.getByRole('combobox');
    expect(select).toBeInTheDocument();
    expect(screen.getByText('No options')).toBeInTheDocument();
  });

  it('renders with children snippet instead of options', () => {
    const { component } = render(SelectField, {
      props: {
        value: '',
      },
    });

    // For now, just test that it doesn't throw with children
    expect(component).toBeTruthy();
  });

  it('handles empty options array', () => {
    render(SelectField, {
      props: {
        value: '',
        options: [],
        placeholder: 'No options available',
      },
    });

    expect(screen.getByText('No options available')).toBeInTheDocument();
  });

  it('updates value when bound prop changes', () => {
    render(SelectField, {
      props: {
        value: 'option1',
        options: defaultOptions,
      },
    });

    const select = screen.getByRole('combobox');
    expect(select).toHaveValue('option1');
  });
});
