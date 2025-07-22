import { describe, it, expect, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import FormField from './FormField.svelte';
import { required, email, minLength, range } from '$lib/utils/validators';
import type { ComponentProps } from 'svelte';

// Helper function to render FormField with proper typing
const renderFormField = (props: Partial<ComponentProps<FormField>>) => {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return render(FormField as any, { props });
};

describe('FormField', () => {
  describe('Text Input', () => {
    it('renders text input with label', () => {
      renderFormField({
        type: 'text',
        name: 'username',
        label: 'Username',
        placeholder: 'Enter username',
      });

      expect(screen.getByLabelText('Username')).toBeInTheDocument();
      expect(screen.getByPlaceholderText('Enter username')).toBeInTheDocument();
    });

    it('shows required indicator', () => {
      renderFormField({
        type: 'text',
        name: 'username',
        label: 'Username',
        required: true,
      });

      expect(screen.getByText('*')).toBeInTheDocument();
    });

    it('handles value changes', async () => {
      const onChange = vi.fn();
      const user = userEvent.setup();

      renderFormField({
        type: 'text',
        name: 'username',
        value: '',
        onChange,
      });

      const input = screen.getByRole('textbox');
      await user.type(input, 'testuser');

      expect(onChange).toHaveBeenLastCalledWith('testuser');
    });

    it('shows validation errors after blur', async () => {
      const user = userEvent.setup();

      renderFormField({
        type: 'text',
        name: 'username',
        label: 'Username',
        validators: [required(), minLength(5)],
      });

      const input = screen.getByLabelText('Username');

      await user.type(input, 'abc');
      await user.tab();

      await waitFor(() => {
        expect(screen.getByText('Must be at least 5 characters long')).toBeInTheDocument();
      });
    });

    it('shows help text', () => {
      renderFormField({
        type: 'text',
        name: 'username',
        helpText: 'Choose a unique username',
      });

      expect(screen.getByText('Choose a unique username')).toBeInTheDocument();
    });
  });

  describe('Email Input', () => {
    it('validates email format', async () => {
      const user = userEvent.setup();

      renderFormField({
        type: 'email',
        name: 'email',
        validators: [email()],
      });

      const input = screen.getByRole('textbox');

      await user.type(input, 'invalid-email');
      await user.tab();

      await waitFor(() => {
        expect(screen.getByText('Invalid email address')).toBeInTheDocument();
      });
    });
  });

  describe('Number Input', () => {
    it('renders number input with min/max', () => {
      renderFormField({
        type: 'number',
        name: 'age',
        label: 'Age',
        min: 0,
        max: 120,
      });

      const input = screen.getByLabelText('Age') as HTMLInputElement;
      expect(input.type).toBe('number');
      expect(input.min).toBe('0');
      expect(input.max).toBe('120');
    });

    it('validates number range', async () => {
      const user = userEvent.setup();

      renderFormField({
        type: 'number',
        name: 'age',
        validators: [range(18, 100)],
      });

      const input = screen.getByRole('spinbutton');

      await user.type(input, '15');
      await user.tab();

      await waitFor(() => {
        expect(screen.getByText('Must be between 18 and 100')).toBeInTheDocument();
      });
    });

    it('handles number value changes', async () => {
      const onChange = vi.fn();
      const user = userEvent.setup();

      renderFormField({
        type: 'number',
        name: 'quantity',
        value: 0,
        onChange,
      });

      const input = screen.getByRole('spinbutton');
      await user.clear(input);
      await user.type(input, '42');

      expect(onChange).toHaveBeenLastCalledWith(42);
    });
  });

  describe('Textarea', () => {
    it('renders textarea with custom rows', () => {
      renderFormField({
        type: 'textarea',
        name: 'description',
        label: 'Description',
        rows: 5,
      });

      const textarea = screen.getByLabelText('Description') as HTMLTextAreaElement;
      expect(textarea.rows).toBe(5);
    });
  });

  describe('Select', () => {
    const options = [
      { value: 'us', label: 'United States' },
      { value: 'uk', label: 'United Kingdom' },
      { value: 'ca', label: 'Canada' },
    ];

    it('renders select with options', () => {
      renderFormField({
        type: 'select',
        name: 'country',
        label: 'Country',
        options,
      });

      expect(screen.getByLabelText('Country')).toBeInTheDocument();
      expect(screen.getByText('United States')).toBeInTheDocument();
      expect(screen.getByText('United Kingdom')).toBeInTheDocument();
      expect(screen.getByText('Canada')).toBeInTheDocument();
    });

    it('handles select changes', async () => {
      const onChange = vi.fn();
      const user = userEvent.setup();

      renderFormField({
        type: 'select',
        name: 'country',
        value: '',
        options,
        onChange,
      });

      const select = screen.getByRole('combobox');
      await user.selectOptions(select, 'uk');

      expect(onChange).toHaveBeenCalledWith('uk');
    });

    it('handles multiple select', async () => {
      const onChange = vi.fn();
      const user = userEvent.setup();

      renderFormField({
        type: 'select',
        name: 'countries',
        value: [],
        options,
        multiple: true,
        onChange,
      });

      const select = screen.getByRole('listbox');
      await user.selectOptions(select, ['us', 'ca']);

      expect(onChange).toHaveBeenCalledWith(['us', 'ca']);
    });
  });

  describe('Checkbox', () => {
    it('renders checkbox with label', () => {
      renderFormField({
        type: 'checkbox',
        name: 'terms',
        placeholder: 'I agree to the terms',
      });

      expect(screen.getByText('I agree to the terms')).toBeInTheDocument();
      expect(screen.getByRole('checkbox')).toBeInTheDocument();
    });

    it('handles checkbox changes', async () => {
      const onChange = vi.fn();
      const user = userEvent.setup();

      renderFormField({
        type: 'checkbox',
        name: 'terms',
        value: false,
        onChange,
      });

      const checkbox = screen.getByRole('checkbox');
      await user.click(checkbox);

      expect(onChange).toHaveBeenCalledWith(true);
    });
  });

  describe('Range Input', () => {
    it('renders range with min/max labels', () => {
      renderFormField({
        type: 'range',
        name: 'volume',
        value: 50,
        min: 0,
        max: 100,
      });

      expect(screen.getByText('0')).toBeInTheDocument();
      expect(screen.getByText('50')).toBeInTheDocument();
      expect(screen.getByText('100')).toBeInTheDocument();
    });
  });

  describe('Disabled and Readonly States', () => {
    it('disables input when disabled prop is true', () => {
      renderFormField({
        type: 'text',
        name: 'username',
        disabled: true,
      });

      expect(screen.getByRole('textbox')).toBeDisabled();
    });

    it('makes input readonly when readonly prop is true', () => {
      renderFormField({
        type: 'text',
        name: 'username',
        readonly: true,
      });

      expect(screen.getByRole('textbox')).toHaveAttribute('readonly');
    });
  });

  describe('Custom Classes', () => {
    it('applies custom classes', () => {
      renderFormField({
        type: 'text',
        name: 'username',
        className: 'custom-form-control',
        inputClassName: 'custom-input',
        label: 'Username',
        labelClassName: 'custom-label',
      });

      expect(document.querySelector('.custom-form-control')).toBeInTheDocument();
      expect(document.querySelector('.custom-input')).toBeInTheDocument();
      expect(document.querySelector('.custom-label')).toBeInTheDocument();
    });
  });

  describe('Event Handlers', () => {
    it('calls onBlur handler', async () => {
      const onBlur = vi.fn();
      const user = userEvent.setup();

      renderFormField({
        type: 'text',
        name: 'username',
        onBlur,
      });

      const input = screen.getByRole('textbox');
      await user.click(input);
      await user.tab();

      expect(onBlur).toHaveBeenCalled();
    });

    it('calls onFocus handler', async () => {
      const onFocus = vi.fn();
      const user = userEvent.setup();

      renderFormField({
        type: 'text',
        name: 'username',
        onFocus,
      });

      const input = screen.getByRole('textbox');
      await user.click(input);

      expect(onFocus).toHaveBeenCalled();
    });

    it('calls onInput handler', async () => {
      const onInput = vi.fn();
      const user = userEvent.setup();

      renderFormField({
        type: 'text',
        name: 'username',
        onInput,
      });

      const input = screen.getByRole('textbox');
      await user.type(input, 'a');

      expect(onInput).toHaveBeenCalledWith('a');
    });
  });
});
