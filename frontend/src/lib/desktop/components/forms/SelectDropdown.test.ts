import { describe, it, expect, vi, beforeEach } from 'vitest';
import {
  createComponentTestFactory,
  screen,
  fireEvent,
  waitFor,
} from '../../../../test/render-helpers';
import userEvent from '@testing-library/user-event';
import SelectDropdown from './SelectDropdown.svelte';
import type { SelectOption } from './SelectDropdown.types';

// Mock scrollIntoView which is not available in jsdom
beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn();
});

describe('SelectDropdown', () => {
  const selectTest = createComponentTestFactory(SelectDropdown);

  const basicOptions: SelectOption[] = [
    { value: 'apple', label: 'Apple' },
    { value: 'banana', label: 'Banana' },
    { value: 'cherry', label: 'Cherry' },
    { value: 'date', label: 'Date' },
  ];

  const groupedOptions: SelectOption[] = [
    { value: 'apple', label: 'Apple', group: 'Fruits' },
    { value: 'banana', label: 'Banana', group: 'Fruits' },
    { value: 'carrot', label: 'Carrot', group: 'Vegetables' },
    { value: 'broccoli', label: 'Broccoli', group: 'Vegetables' },
  ];

  const optionsWithDetails: SelectOption[] = [
    { value: 'apple', label: 'Apple', description: 'Sweet red fruit', icon: '🍎' },
    { value: 'banana', label: 'Banana', description: 'Yellow tropical fruit', icon: '🍌' },
    { value: 'cherry', label: 'Cherry', description: 'Small stone fruit', icon: '🍒' },
  ];

  describe('Basic Functionality', () => {
    it('renders with placeholder', () => {
       
      selectTest.render({
        props: {
          options: basicOptions,
          placeholder: 'Choose a fruit',
        },
      });

      expect(screen.getByText('Choose a fruit')).toBeInTheDocument();
    });

    it('renders with label', () => {
       
      selectTest.render({
        props: {
          options: basicOptions,
          label: 'Select Fruit',
        },
      });

      expect(screen.getByText('Select Fruit')).toBeInTheDocument();
    });

    it('shows required indicator', () => {
       
      selectTest.render({
        props: {
          options: basicOptions,
          label: 'Select Fruit',
          required: true,
        },
      });

      expect(screen.getByText('*')).toBeInTheDocument();
    });

    it('opens dropdown on click', async () => {
       
      selectTest.render({
        props: { options: basicOptions },
      });

      const button = screen.getByRole('button');
      await fireEvent.click(button);

      expect(screen.getByText('Apple')).toBeInTheDocument();
      expect(screen.getByText('Banana')).toBeInTheDocument();
    });

    it('closes dropdown on escape', async () => {
      const user = userEvent.setup();

       
      selectTest.render({
        props: { options: basicOptions },
      });

      const button = screen.getByRole('button');
      await fireEvent.click(button);

      expect(screen.getByText('Apple')).toBeInTheDocument();

      // Focus on the button before pressing escape
      button.focus();
      await user.keyboard('{Escape}');

      // Wait for the dropdown to close
      await waitFor(() => {
        expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
      });
    });

    it('closes dropdown on outside click', async () => {
       
      selectTest.render({
        props: { options: basicOptions },
      });

      const button = screen.getByRole('button');
      await fireEvent.click(button);

      expect(screen.getByText('Apple')).toBeInTheDocument();

      await fireEvent.click(document.body);

      await waitFor(() => {
        expect(screen.queryByText('Apple')).not.toBeInTheDocument();
      });
    });
  });

  describe('Single Selection', () => {
    it('selects option on click', async () => {
      const onChange = vi.fn();

       
      selectTest.render({
        props: {
          options: basicOptions,
          onChange,
        },
      });

      await fireEvent.click(screen.getByRole('button'));
      await fireEvent.click(screen.getByText('Banana'));

      expect(onChange).toHaveBeenCalledWith('banana');
      expect(screen.getByRole('button')).toHaveTextContent('Banana');
    });

    it('displays initial value', () => {
       
      selectTest.render({
        props: {
          options: basicOptions,
          value: 'cherry',
        },
      });

      expect(screen.getByRole('button')).toHaveTextContent('Cherry');
    });

    it('updates display when value changes', async () => {
      const {
        rerender,
      } =  
        selectTest.render({
          props: {
            options: basicOptions,
            value: 'apple',
          },
        });

      expect(screen.getByRole('button')).toHaveTextContent('Apple');

      await rerender({ value: 'banana' });

      expect(screen.getByRole('button')).toHaveTextContent('Banana');
    });
  });

  describe('Multiple Selection', () => {
    it('allows multiple selections', async () => {
      const onChange = vi.fn();

       
      selectTest.render({
        props: {
          options: basicOptions,
          multiple: true,
          onChange,
        },
      });

      await fireEvent.click(screen.getByRole('button'));
      await fireEvent.click(screen.getByText('Apple'));
      await fireEvent.click(screen.getByText('Banana'));

      expect(onChange).toHaveBeenCalledWith(['apple']);
      expect(onChange).toHaveBeenCalledWith(['apple', 'banana']);
      expect(screen.getByRole('button')).toHaveTextContent('2 selected');
    });

    it('deselects on second click', async () => {
      const onChange = vi.fn();

       
      selectTest.render({
        props: {
          options: basicOptions,
          multiple: true,
          value: ['apple', 'banana'],
          onChange,
        },
      });

      await fireEvent.click(screen.getByRole('button'));
      await fireEvent.click(screen.getByText('Apple'));

      expect(onChange).toHaveBeenCalledWith(['banana']);
    });

    it('shows checkboxes for multiple selection', async () => {
       
      selectTest.render({
        props: {
          options: basicOptions,
          multiple: true,
        },
      });

      await fireEvent.click(screen.getByRole('button'));

      const checkboxes = screen.getAllByRole('checkbox');
      expect(checkboxes).toHaveLength(basicOptions.length);
    });

    it('respects maxSelections', async () => {
      const onChange = vi.fn();

       
      selectTest.render({
        props: {
          options: basicOptions,
          multiple: true,
          maxSelections: 2,
          value: ['apple', 'banana'],
          onChange,
        },
      });

      await fireEvent.click(screen.getByRole('button'));
      await fireEvent.click(screen.getByText('Cherry'));

      expect(onChange).not.toHaveBeenCalled();
      expect(screen.getByText('2 / 2 selected')).toBeInTheDocument();
    });
  });

  describe('Search Functionality', () => {
    it('shows search input when searchable', async () => {
       
      selectTest.render({
        props: {
          options: basicOptions,
          searchable: true,
        },
      });

      await fireEvent.click(screen.getByRole('button'));

      expect(screen.getByPlaceholderText('Search...')).toBeInTheDocument();
    });

    it('filters options based on search', async () => {
      const user = userEvent.setup();

       
      selectTest.render({
        props: {
          options: basicOptions,
          searchable: true,
        },
      });

      await fireEvent.click(screen.getByRole('button'));

      const searchInput = screen.getByPlaceholderText('Search...');
      await user.type(searchInput, 'app');

      expect(screen.getByText('Apple')).toBeInTheDocument();
      expect(screen.queryByText('Banana')).not.toBeInTheDocument();
    });

    it('shows no options message when filtered empty', async () => {
      const user = userEvent.setup();

       
      selectTest.render({
        props: {
          options: basicOptions,
          searchable: true,
        },
      });

      await fireEvent.click(screen.getByRole('button'));

      const searchInput = screen.getByPlaceholderText('Search...');
      await user.type(searchInput, 'xyz');

      expect(screen.getByText('No options found')).toBeInTheDocument();
    });

    it('calls onSearch callback', async () => {
      const onSearch = vi.fn();
      const user = userEvent.setup();

       
      selectTest.render({
        props: {
          options: basicOptions,
          searchable: true,
          onSearch,
        },
      });

      await fireEvent.click(screen.getByRole('button'));

      const searchInput = screen.getByPlaceholderText('Search...');
      await user.type(searchInput, 'test');

      expect(onSearch).toHaveBeenCalledWith('test');
    });
  });

  describe('Clear Functionality', () => {
    it('shows clear button when clearable and has value', async () => {
       
      selectTest.render({
        props: {
          options: basicOptions,
          value: 'apple',
          clearable: true,
        },
      });

      expect(screen.getByLabelText('Clear selection')).toBeInTheDocument();
    });

    it('clears selection on clear button click', async () => {
      const onChange = vi.fn();
      const onClear = vi.fn();

       
      selectTest.render({
        props: {
          options: basicOptions,
          value: 'apple',
          clearable: true,
          onChange,
          onClear,
        },
      });

      await fireEvent.click(screen.getByLabelText('Clear selection'));

      expect(onChange).toHaveBeenCalledWith('');
      expect(onClear).toHaveBeenCalled();
    });

    it('clears multiple selections', async () => {
      const onChange = vi.fn();

       
      selectTest.render({
        props: {
          options: basicOptions,
          value: ['apple', 'banana'],
          multiple: true,
          clearable: true,
          onChange,
        },
      });

      await fireEvent.click(screen.getByLabelText('Clear selection'));

      expect(onChange).toHaveBeenCalledWith([]);
    });
  });

  describe('Grouped Options', () => {
    it('displays group headers', async () => {
       
      selectTest.render({
        props: {
          options: groupedOptions,
          groupBy: true,
        },
      });

      await fireEvent.click(screen.getByRole('button'));

      expect(screen.getByText('Fruits')).toBeInTheDocument();
      expect(screen.getByText('Vegetables')).toBeInTheDocument();
    });

    it('can disable grouping', async () => {
       
      selectTest.render({
        props: {
          options: groupedOptions,
          groupBy: false,
        },
      });

      await fireEvent.click(screen.getByRole('button'));

      expect(screen.queryByText('Fruits')).not.toBeInTheDocument();
      expect(screen.queryByText('Vegetables')).not.toBeInTheDocument();
    });
  });

  describe('Options with Details', () => {
    it('displays icons and descriptions', async () => {
       
      selectTest.render({
        props: {
          options: optionsWithDetails,
        },
      });

      await fireEvent.click(screen.getByRole('button'));

      expect(screen.getByText('🍎')).toBeInTheDocument();
      expect(screen.getByText('Sweet red fruit')).toBeInTheDocument();
    });

    it('searches in descriptions', async () => {
      const user = userEvent.setup();

       
      selectTest.render({
        props: {
          options: optionsWithDetails,
          searchable: true,
        },
      });

      await fireEvent.click(screen.getByRole('button'));

      const searchInput = screen.getByPlaceholderText('Search...');
      await user.type(searchInput, 'tropical');

      expect(screen.getByText('Banana')).toBeInTheDocument();
      expect(screen.queryByText('Apple')).not.toBeInTheDocument();
    });
  });

  describe('Keyboard Navigation', () => {
    it('navigates with arrow keys', async () => {
      const user = userEvent.setup();

       
      selectTest.render({
        props: {
          options: basicOptions,
        },
      });

      const button = screen.getByRole('button');

      // Open dropdown with keyboard
      button.focus();
      await user.keyboard('{ArrowDown}');

      // Dropdown should be open
      await waitFor(() => {
        expect(screen.getByRole('listbox')).toBeInTheDocument();
      });

      // Navigate with arrow keys
      const options = screen.getAllByRole('option');

      // First ArrowDown should highlight first option
      await user.keyboard('{ArrowDown}');
      expect(options[0]).toHaveClass('bg-base-200');

      // Second ArrowDown should highlight second option
      await user.keyboard('{ArrowDown}');
      expect(options[1]).toHaveClass('bg-base-200');
    });

    it('opens with Enter or Space', async () => {
      const user = userEvent.setup();

       
      selectTest.render({
        props: {
          options: basicOptions,
        },
      });

      const button = screen.getByRole('button');
      button.focus();

      await user.keyboard('{Enter}');

      expect(screen.getByText('Apple')).toBeInTheDocument();
    });
  });

  describe('Disabled State', () => {
    it('disables the dropdown', () => {
       
      selectTest.render({
        props: {
          options: basicOptions,
          disabled: true,
        },
      });

      const button = screen.getByRole('button');
      expect(button).toBeDisabled();
    });

    it('shows disabled options', async () => {
      const optionsWithDisabled: SelectOption[] = [
        { value: 'apple', label: 'Apple' },
        { value: 'banana', label: 'Banana', disabled: true },
        { value: 'cherry', label: 'Cherry' },
      ];

       
      selectTest.render({
        props: {
          options: optionsWithDisabled,
        },
      });

      await fireEvent.click(screen.getByRole('button'));

      const bananaOption = screen.getByText('Banana').closest('button');
      expect(bananaOption).toHaveClass('opacity-50');
      expect(bananaOption).toBeDisabled();
    });
  });

  it('applies custom classes', () => {
     
    selectTest.render({
      props: {
        options: basicOptions,
        className: 'custom-select',
        dropdownClassName: 'custom-dropdown',
      },
    });

    expect(document.querySelector('.select-dropdown.custom-select')).toBeInTheDocument();
  });

  it('shows help text', () => {
     
    selectTest.render({
      props: {
        options: basicOptions,
        helpText: 'Choose your favorite fruit',
      },
    });

    expect(screen.getByText('Choose your favorite fruit')).toBeInTheDocument();
  });
});
