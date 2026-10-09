import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  createComponentTestFactory,
  screen,
  fireEvent,
  waitFor,
} from '../../../../test/render-helpers';
import userEvent from '@testing-library/user-event';
import SelectDropdown from './SelectDropdown.svelte';
import type { SelectOption } from './SelectDropdown.types';
import { expectNoA11yViolations } from '$lib/utils/axe-utils';
import {
  OPTION_SELECTED_BG_CLASS,
  OPTION_HIGHLIGHT_BG_CLASS,
  OPTION_HIGHLIGHT_OUTLINE_CLASS,
} from './SelectDropdown.styles';

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

      const button = screen.getByRole('combobox');
      await fireEvent.click(button);

      expect(screen.getByText('Apple')).toBeInTheDocument();
      expect(screen.getByText('Banana')).toBeInTheDocument();
    });

    it('closes dropdown on escape', async () => {
      const user = userEvent.setup();

      selectTest.render({
        props: { options: basicOptions },
      });

      const button = screen.getByRole('combobox');
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

      const button = screen.getByRole('combobox');
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

      await fireEvent.click(screen.getByRole('combobox'));
      await fireEvent.click(screen.getByText('Banana'));

      expect(onChange).toHaveBeenCalledWith('banana');
      expect(screen.getByRole('combobox')).toHaveTextContent('Banana');
    });

    it('displays initial value', () => {
      selectTest.render({
        props: {
          options: basicOptions,
          value: 'cherry',
        },
      });

      expect(screen.getByRole('combobox')).toHaveTextContent('Cherry');
    });

    it('updates display when value changes', async () => {
      const { rerender } = selectTest.render({
        props: {
          options: basicOptions,
          value: 'apple',
        },
      });

      expect(screen.getByRole('combobox')).toHaveTextContent('Apple');

      await rerender({ value: 'banana' });

      expect(screen.getByRole('combobox')).toHaveTextContent('Banana');
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

      await fireEvent.click(screen.getByRole('combobox'));
      await fireEvent.click(screen.getByText('Apple'));
      await fireEvent.click(screen.getByText('Banana'));

      expect(onChange).toHaveBeenCalledWith(['apple']);
      expect(onChange).toHaveBeenCalledWith(['apple', 'banana']);
      expect(screen.getByRole('combobox')).toHaveTextContent('2 selected');
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

      await fireEvent.click(screen.getByRole('combobox'));
      await fireEvent.click(screen.getByText('Apple'));

      expect(onChange).toHaveBeenCalledWith(['banana']);
    });

    it('shows selectable options for multiple selection', async () => {
      selectTest.render({
        props: {
          options: basicOptions,
          multiple: true,
        },
      });

      await fireEvent.click(screen.getByRole('combobox'));

      const options = screen.getAllByRole('option');
      expect(options).toHaveLength(basicOptions.length);
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

      await fireEvent.click(screen.getByRole('combobox'));
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

      await fireEvent.click(screen.getByRole('combobox'));

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

      await fireEvent.click(screen.getByRole('combobox'));

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

      await fireEvent.click(screen.getByRole('combobox'));

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

      await fireEvent.click(screen.getByRole('combobox'));

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

    it('keeps focus on the trigger after the focused clear control clears the value', async () => {
      selectTest.render({
        props: { options: basicOptions, value: 'apple', clearable: true },
      });
      const clear = screen.getByLabelText('Clear selection');
      clear.focus();

      await fireEvent.click(clear);

      await waitFor(() => expect(screen.queryByLabelText('Clear selection')).toBeNull());
      expect(screen.getByRole('combobox')).toHaveFocus();
    });

    it('moves focus to the trigger when a clear click leaves focus on body', async () => {
      selectTest.render({
        props: { options: basicOptions, value: 'apple', clearable: true },
      });
      const active = document.activeElement;
      if (active instanceof HTMLElement) active.blur();

      await fireEvent.click(screen.getByLabelText('Clear selection'));

      expect(screen.getByRole('combobox')).toHaveFocus();
    });

    it('keeps focus where an onClear callback moved it', async () => {
      const target = document.createElement('input');
      document.body.append(target);
      selectTest.render({
        props: {
          options: basicOptions,
          value: 'apple',
          clearable: true,
          onClear: () => target.focus(),
        },
      });
      screen.getByLabelText('Clear selection').focus();

      await fireEvent.click(screen.getByLabelText('Clear selection'));

      expect(target).toHaveFocus();
      target.remove();
    });

    it('leaves focus on another control when the clear click did not take it', async () => {
      const other = document.createElement('input');
      document.body.append(other);
      selectTest.render({
        props: { options: basicOptions, value: 'apple', clearable: true },
      });
      other.focus();

      await fireEvent.click(screen.getByLabelText('Clear selection'));

      expect(other).toHaveFocus();
      other.remove();
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

      await fireEvent.click(screen.getByRole('combobox'));

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

      await fireEvent.click(screen.getByRole('combobox'));

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

      await fireEvent.click(screen.getByRole('combobox'));

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

      await fireEvent.click(screen.getByRole('combobox'));

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

      const button = screen.getByRole('combobox');

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
      expect(options[0]).toHaveClass(...OPTION_HIGHLIGHT_OUTLINE_CLASS.split(' '));
      expect(options[0]).toHaveClass(
        'outline-2',
        '-outline-offset-2',
        'outline-[var(--color-base-content)]'
      );
      expect(options[0]).toHaveClass(OPTION_HIGHLIGHT_BG_CLASS);

      // Second ArrowDown should highlight second option
      await user.keyboard('{ArrowDown}');
      expect(options[1]).toHaveClass(...OPTION_HIGHLIGHT_OUTLINE_CLASS.split(' '));
      // The highlight moves rather than accumulates
      expect(options[0]).not.toHaveClass('outline-2');
    });

    it('shows the selected option in base-content text with a base-content check', async () => {
      selectTest.render({
        props: {
          options: basicOptions,
          value: 'banana',
        },
      });

      await fireEvent.click(screen.getByRole('combobox'));

      const selected = screen
        .getAllByRole('option')
        .find(option => option.getAttribute('aria-selected') === 'true');
      expect(selected).toBeDefined();
      expect(selected?.className).toContain('text-[var(--color-base-content)]');
      const primaryClasses = (selected?.className ?? '')
        .split(/\s+/)
        .filter(token => token.includes('--color-primary'));
      expect(primaryClasses).toEqual([OPTION_SELECTED_BG_CLASS]);
      const check = selected?.querySelector('svg');
      expect(check).not.toBeNull();
      expect(check?.getAttribute('class') ?? '').not.toContain('--color-primary');
    });

    it('outlines the selected option when it is highlighted and keeps its tint', async () => {
      const user = userEvent.setup();

      selectTest.render({
        props: {
          options: basicOptions,
          value: 'apple',
        },
      });

      const button = screen.getByRole('combobox');
      button.focus();
      await user.keyboard('{ArrowDown}');
      await waitFor(() => {
        expect(screen.getByRole('listbox')).toBeInTheDocument();
      });
      await user.keyboard('{ArrowDown}');

      const options = screen.getAllByRole('option');
      expect(options[0]).toHaveClass(...OPTION_SELECTED_BG_CLASS.split(' '));
      expect(options[0]).toHaveClass('outline-2');
      expect(options[0]).not.toHaveClass(...OPTION_HIGHLIGHT_BG_CLASS.split(' '));
    });

    it('gives options a focus-visible outline instead of hiding focus', async () => {
      selectTest.render({
        props: {
          options: basicOptions,
        },
      });

      await fireEvent.click(screen.getByRole('combobox'));

      for (const option of screen.getAllByRole('option')) {
        expect(option.className).toContain('focus-visible:outline-2');
        expect(option.className).toContain('focus-visible:outline-[var(--color-base-content)]');
        expect(option.className).not.toContain('focus:outline-hidden');
      }
    });

    it('opens with Enter or Space', async () => {
      const user = userEvent.setup();

      selectTest.render({
        props: {
          options: basicOptions,
        },
      });

      const button = screen.getByRole('combobox');
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

      const button = screen.getByRole('combobox');
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

      await fireEvent.click(screen.getByRole('combobox'));

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

    expect(document.querySelector('.custom-select')).toBeInTheDocument();
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

describe('SelectDropdown Accessibility', () => {
  const selectTest = createComponentTestFactory(SelectDropdown);

  const fruit: SelectOption[] = [
    { value: 'apple', label: 'Apple' },
    { value: 'banana', label: 'Banana' },
    { value: 'cherry', label: 'Cherry' },
  ];

  async function openSearchable(props: Record<string, unknown> = {}) {
    const user = userEvent.setup();
    const onChange = vi.fn();
    selectTest.render({ props: { options: fruit, searchable: true, onChange, ...props } });
    await user.click(screen.getAllByRole('combobox')[0]);
    const search = await screen.findByRole('searchbox');
    await waitFor(() => expect(search).toHaveFocus());
    return { user, onChange, search };
  }

  /** Records Escape keydowns that reach the document, like Modal's listener. */
  function trackDocumentEscape() {
    const seen: string[] = [];
    const listener = (event: KeyboardEvent) => {
      if (event.key === 'Escape') seen.push(event.key);
    };
    document.addEventListener('keydown', listener);
    return { seen, stop: () => document.removeEventListener('keydown', listener) };
  }

  it('moves the highlight with arrow keys in the search box and selects with Enter', async () => {
    const { user, onChange, search } = await openSearchable();

    await user.keyboard('{ArrowDown}{ArrowDown}');

    const options = screen.getAllByRole('option');
    expect(options[1].id).not.toBe('');
    expect(search).toHaveAttribute('aria-activedescendant', options[1].id);

    await user.keyboard('{Enter}');

    expect(onChange).toHaveBeenCalledWith('banana');
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
    expect(document.activeElement).toBe(screen.getByRole('combobox'));
  });

  it('moves the highlight up with ArrowUp in the search box', async () => {
    const { user, search } = await openSearchable();

    await user.keyboard('{ArrowDown}{ArrowDown}{ArrowUp}');

    const options = screen.getAllByRole('option');
    expect(search).toHaveAttribute('aria-activedescendant', options[0].id);
  });

  it('highlights the last option when ArrowUp is pressed with nothing highlighted', async () => {
    const { user, search } = await openSearchable();

    await user.keyboard('{ArrowUp}');

    const options = screen.getAllByRole('option');
    expect(search).toHaveAttribute('aria-activedescendant', options[options.length - 1].id);
  });

  it('keeps the first option highlighted when ArrowUp is pressed on it', async () => {
    const { user, search } = await openSearchable();

    await user.keyboard('{ArrowDown}{ArrowUp}{ArrowUp}');

    const options = screen.getAllByRole('option');
    expect(search).toHaveAttribute('aria-activedescendant', options[0].id);
  });

  describe('with interleaved groups', () => {
    const interleaved: SelectOption[] = [
      { value: 'a1', label: 'A1', group: 'A' },
      { value: 'b1', label: 'B1', group: 'B' },
      { value: 'a2', label: 'A2', group: 'A' },
    ];

    it('walks the options in the order they are rendered', async () => {
      const { user, search } = await openSearchable({ options: interleaved, groupBy: true });
      const rendered = screen.getAllByRole('option');
      expect(rendered.map(o => o.textContent.trim())).toEqual(['A1', 'A2', 'B1']);
      rendered.forEach((option, index) =>
        expect(option.id.endsWith(`-option-${index}`)).toBe(true)
      );

      for (const [index, label] of ['A1', 'A2', 'B1'].entries()) {
        await user.keyboard('{ArrowDown}');
        const active = document.getElementById(search.getAttribute('aria-activedescendant') ?? '');
        expect(active?.textContent.trim()).toBe(label);
        expect(active).toBe(rendered[index]);
      }
    });

    it('selects the option that is highlighted on screen with Enter', async () => {
      const { user, onChange } = await openSearchable({ options: interleaved, groupBy: true });

      await user.keyboard('{ArrowDown}{ArrowDown}{Enter}');

      expect(onChange).toHaveBeenCalledWith('a2');
    });
  });

  it('drops aria-activedescendant when the options shrink below the highlighted index', async () => {
    const user = userEvent.setup();
    const { rerender } = selectTest.render({ props: { options: fruit, searchable: true } });
    await user.click(screen.getAllByRole('combobox')[0]);
    const search = await screen.findByRole('searchbox');
    await waitFor(() => expect(search).toHaveFocus());
    await user.keyboard('{ArrowDown}{ArrowDown}{ArrowDown}');
    expect(search).toHaveAttribute('aria-activedescendant');

    await rerender({ options: fruit.slice(0, 2), searchable: true });

    expect(search).not.toHaveAttribute('aria-activedescendant');

    // ArrowUp from beyond the list lands on the last remaining option, not the one above it
    await user.keyboard('{ArrowUp}');
    expect(search).toHaveAttribute('aria-activedescendant', screen.getAllByRole('option')[1].id);
  });

  it('sets no aria-activedescendant when ArrowDown is pressed with no matching options', async () => {
    const { user, search } = await openSearchable();

    await user.keyboard('zzz');
    await user.keyboard('{ArrowDown}');

    expect(search).not.toHaveAttribute('aria-activedescendant');
  });

  it('does nothing on Enter in the search box when no option is highlighted', async () => {
    const { onChange } = await openSearchable();

    // fireEvent returns false when the event was cancelled, which is what stops a form submit
    const notCancelled = await fireEvent.keyDown(screen.getByRole('searchbox'), { key: 'Enter' });

    expect(notCancelled).toBe(false);
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByRole('listbox')).toBeInTheDocument();
  });

  it('typing a space in the search box types a space instead of selecting', async () => {
    const { user, onChange, search } = await openSearchable();

    await user.keyboard('{ArrowDown}');
    await user.keyboard('a b');

    expect(search).toHaveValue('a b');
    expect(onChange).not.toHaveBeenCalled();
    // No fruit matches "a b", so the open list shows its empty state and still has no option
    expect(search).toBeInTheDocument();
    expect(screen.queryAllByRole('option')).toHaveLength(0);
    expect(screen.getByRole('status')).toHaveTextContent('No options found');
  });

  it('Escape in the search box closes only the list and does not reach the document', async () => {
    const { user } = await openSearchable();
    const escapes = trackDocumentEscape();

    await user.keyboard('{Escape}');
    escapes.stop();

    await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
    expect(document.activeElement).toBe(screen.getByRole('combobox'));
    expect(escapes.seen).toEqual([]);
  });

  it('Escape on a focused option closes only the list and does not reach the document', async () => {
    const user = userEvent.setup();
    selectTest.render({ props: { options: fruit, multiple: true } });
    await user.click(screen.getByRole('combobox'));
    const option = (await screen.findAllByRole('option'))[0];
    option.focus();
    const escapes = trackDocumentEscape();

    await user.keyboard('{Escape}');
    escapes.stop();

    await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
    expect(document.activeElement).toBe(screen.getByRole('combobox'));
    expect(escapes.seen).toEqual([]);
  });

  const addedButtons: HTMLButtonElement[] = [];

  afterEach(() => {
    addedButtons.splice(0).forEach(button => button.remove());
  });

  /** Appends a plain button to the document body; removed after each test. */
  function addButton(text: string): HTMLButtonElement {
    const button = document.createElement('button');
    button.textContent = text;
    document.body.append(button);
    addedButtons.push(button);
    return button;
  }

  /** Renders the dropdown between two plain buttons so Tab has somewhere to land. */
  function renderBetweenButtons(props: Record<string, unknown>) {
    const before = addButton('Before');
    selectTest.render({ props: { options: fruit, ...props } });
    const after = addButton('After');
    return { before, after };
  }

  it('Tab in the search box closes the list and moves focus on from the trigger', async () => {
    const user = userEvent.setup();
    const { after } = renderBetweenButtons({ searchable: true });
    await user.click(screen.getByRole('combobox'));
    await waitFor(() => expect(screen.getByRole('searchbox')).toHaveFocus());

    await user.keyboard('{Tab}');

    await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
    expect(document.activeElement).toBe(after);
  });

  it('Shift+Tab from a focused option closes the list and moves focus on from the trigger', async () => {
    const user = userEvent.setup();
    const { before } = renderBetweenButtons({ multiple: true });
    await user.click(screen.getByRole('combobox'));
    const option = (await screen.findAllByRole('option'))[0];
    option.focus();

    await user.keyboard('{Shift>}{Tab}{/Shift}');

    await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
    expect(document.activeElement).toBe(before);
  });

  it('returns focus to the trigger after an option is clicked', async () => {
    const { user } = await openSearchable();

    const option = screen.getAllByRole('option')[0];
    option.focus();
    await user.click(option);

    await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
    expect(document.activeElement).toBe(screen.getByRole('combobox'));
  });

  it('returns focus to the trigger when focus was on body at selection time', async () => {
    const { onChange, search } = await openSearchable();
    search.blur();
    expect(document.activeElement).toBe(document.body);

    // A click on a button that takes no focus (Safari) does not move focus to the option
    await fireEvent.click(screen.getAllByRole('option')[0]);

    expect(onChange).toHaveBeenCalledWith('apple');
    await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
    expect(document.activeElement).toBe(screen.getByRole('combobox'));
  });

  it('keeps focus in the search box when a multiple-select option is clicked without taking focus', async () => {
    const { onChange, search } = await openSearchable({ multiple: true });
    search.blur();
    expect(document.activeElement).toBe(document.body);

    await fireEvent.click(screen.getAllByRole('option')[0]);

    expect(onChange).toHaveBeenCalledWith(['apple']);
    expect(screen.getByRole('listbox')).toBeInTheDocument();
    expect(document.activeElement).toBe(search);
  });

  it('keeps focus on the trigger when a multiple-select option without search is clicked without taking focus', async () => {
    const user = userEvent.setup();
    selectTest.render({ props: { options: fruit, multiple: true } });
    const trigger = screen.getByRole('combobox');
    await user.click(trigger);
    await screen.findByRole('listbox');
    trigger.blur();
    expect(document.activeElement).toBe(document.body);

    await fireEvent.click(screen.getAllByRole('option')[0]);

    expect(screen.getByRole('listbox')).toBeInTheDocument();
    expect(document.activeElement).toBe(trigger);
  });

  it('does not move focus on an outside click', async () => {
    const user = userEvent.setup();
    selectTest.render({ props: { options: fruit } });
    const outside = addButton('Outside');

    await user.click(screen.getByRole('combobox'));
    await screen.findByRole('listbox');

    await user.click(outside);

    await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
    expect(document.activeElement).toBe(outside);
  });

  it('names the open listbox from the label associated with the trigger', async () => {
    const user = userEvent.setup();
    const { container } = selectTest.render({ props: { options: fruit, id: 'device-field' } });
    const externalLabel = document.createElement('label');
    externalLabel.htmlFor = 'device-field';
    externalLabel.textContent = 'Audio Device';
    container.prepend(externalLabel);

    await user.click(screen.getByRole('combobox'));

    expect(await screen.findByRole('listbox', { name: 'Audio Device' })).toBeInTheDocument();
  });

  it('keeps the label prop as the listbox name and adds no aria-label without any label', async () => {
    const user = userEvent.setup();
    const labelled = selectTest.render({ props: { options: fruit, label: 'Fruit' } });
    await user.click(screen.getByRole('combobox'));
    const named = await screen.findByRole('listbox', { name: 'Fruit' });
    expect(named).toHaveAttribute('aria-labelledby');
    expect(named).not.toHaveAttribute('aria-label');
    labelled.unmount();

    selectTest.render({ props: { options: fruit } });
    await user.click(screen.getByRole('combobox'));
    const unnamed = await screen.findByRole('listbox');
    expect(unnamed).not.toHaveAttribute('aria-label');
  });

  it('names the trigger and the open listbox from the aria-label prop, not the selected value', async () => {
    const user = userEvent.setup();
    selectTest.render({ props: { options: fruit, value: 'banana', 'aria-label': 'Fruit picker' } });

    const trigger = screen.getByRole('combobox', { name: 'Fruit picker' });
    expect(trigger).toHaveTextContent('Banana');

    await user.click(trigger);

    expect(await screen.findByRole('listbox', { name: 'Fruit picker' })).toBeInTheDocument();
  });

  it('lets the aria-label prop win over a label element associated by id', async () => {
    const user = userEvent.setup();
    const { container } = selectTest.render({
      props: { options: fruit, id: 'fruit-field', 'aria-label': 'Fruit picker' },
    });
    const externalLabel = document.createElement('label');
    externalLabel.htmlFor = 'fruit-field';
    externalLabel.textContent = 'Outer label';
    container.prepend(externalLabel);

    await user.click(screen.getByRole('combobox', { name: 'Fruit picker' }));

    expect(await screen.findByRole('listbox', { name: 'Fruit picker' })).toBeInTheDocument();
  });

  describe('selected value', () => {
    // A combobox exposes its displayed text as its value, so the trigger must not also list the
    // value in aria-describedby: a screen reader would announce it twice (measured in Chromium:
    // a button with role combobox and an aria-describedby on its value span reports the same
    // text as both value and description).
    const namingPaths: Array<[string, Record<string, unknown>, boolean]> = [
      ['the label prop', { label: 'Fruit' }, false],
      ['the aria-label prop', { 'aria-label': 'Fruit' }, false],
      ['a label element associated by id', { id: 'fruit-field' }, true],
    ];

    it.each(namingPaths)(
      'keeps the field name as the name and shows the selected value as the trigger text with %s',
      async (_path, props, withExternalLabel) => {
        const user = userEvent.setup();
        const { container } = selectTest.render({
          props: { options: fruit, value: 'apple', ...props },
        });
        if (withExternalLabel) {
          const externalLabel = document.createElement('label');
          externalLabel.htmlFor = 'fruit-field';
          externalLabel.textContent = 'Fruit';
          container.prepend(externalLabel);
        }

        const trigger = screen.getByRole('combobox', { name: 'Fruit' });
        expect(trigger).toHaveTextContent('Apple');
        expect(trigger).toHaveAccessibleDescription('');

        await user.click(trigger);
        await user.click(await screen.findByRole('option', { name: /Cherry/ }));

        const updated = screen.getByRole('combobox', { name: 'Fruit' });
        expect(updated).toHaveTextContent('Cherry');
        expect(updated).toHaveAccessibleDescription('');
      }
    );

    it('shows a multiple selection as the displayed count and an empty one as the placeholder', async () => {
      const user = userEvent.setup();
      selectTest.render({
        props: {
          options: fruit,
          multiple: true,
          value: ['apple'],
          placeholder: 'Pick fruit',
          label: 'Fruit',
        },
      });

      const trigger = screen.getByRole('combobox', { name: 'Fruit' });
      expect(trigger).toHaveTextContent('1 selected');

      await user.click(trigger);
      await user.click(await screen.findByRole('option', { name: /Banana/ }));
      expect(trigger).toHaveTextContent('2 selected');

      selectTest.render({ props: { options: fruit, placeholder: 'Pick fruit', label: 'Other' } });
      expect(screen.getByRole('combobox', { name: 'Other' })).toHaveTextContent('Pick fruit');
    });

    it('describes the trigger with the help text and the caller ids only, not the value', () => {
      const both = selectTest.render({
        props: { options: fruit, id: 'both', helpText: 'Help', 'aria-describedby': 'extra-note' },
      });
      expect(screen.getByRole('combobox').getAttribute('aria-describedby')).toBe(
        'both-help extra-note'
      );
      both.unmount();

      const only = selectTest.render({
        props: { options: fruit, id: 'only', 'aria-describedby': 'extra-note' },
      });
      expect(screen.getByRole('combobox').getAttribute('aria-describedby')).toBe('extra-note');
      only.unmount();

      const help = selectTest.render({
        props: { options: fruit, id: 'help', label: 'Fruit', helpText: 'Help' },
      });
      expect(screen.getByRole('combobox')).toHaveAccessibleDescription('Help');
      help.unmount();

      selectTest.render({ props: { options: fruit, id: 'none' } });
      expect(screen.getByRole('combobox')).not.toHaveAttribute('aria-describedby');
    });
  });

  it('renders no aria-label on the trigger when the label prop is used', () => {
    selectTest.render({ props: { options: fruit, label: 'Fruit', 'aria-label': 'Ignored' } });

    const trigger = screen.getByRole('combobox', { name: 'Fruit' });
    expect(trigger).not.toHaveAttribute('aria-label');
  });

  it('has no violations with the searchable list open and an option highlighted', async () => {
    const { user, search } = await openSearchable({ label: 'Fruit' });
    await user.keyboard('{ArrowDown}');
    expect(search).toHaveAttribute('aria-activedescendant');

    // The landmark rule is about whole pages, not an isolated component
    await expectNoA11yViolations(document.body, { rules: { region: { enabled: false } } });
  });

  describe('combobox exposure', () => {
    const OUTLINE = OPTION_HIGHLIGHT_OUTLINE_CLASS.split(' ');

    /** The option that carries the keyboard highlight outline, or undefined when none does. */
    function outlinedOption() {
      return screen.queryAllByRole('option').find(option => option.classList.contains(OUTLINE[0]));
    }

    async function openWithArrowDown(props: Record<string, unknown> = {}) {
      const user = userEvent.setup();
      const rendered = selectTest.render({ props: { options: fruit, label: 'Fruit', ...props } });
      const trigger = screen.getByRole('combobox');
      trigger.focus();
      await user.keyboard('{ArrowDown}');
      await screen.findByRole('listbox');
      return { user, trigger, ...rendered };
    }

    it('makes the trigger a combobox that keeps aria-haspopup listbox', () => {
      selectTest.render({ props: { options: fruit, label: 'Fruit' } });

      const trigger = screen.getByRole('combobox', { name: 'Fruit' });
      expect(trigger.tagName).toBe('BUTTON');
      expect(trigger).toHaveAttribute('aria-haspopup', 'listbox');
      expect(trigger).toHaveAttribute('aria-expanded', 'false');
      expect(trigger).not.toHaveAttribute('aria-controls');
      expect(trigger).not.toHaveAttribute('aria-activedescendant');
    });

    it('points the trigger at the open listbox with aria-controls', async () => {
      const { user, trigger } = await openWithArrowDown();

      expect(trigger).toHaveAttribute('aria-expanded', 'true');
      expect(trigger).toHaveAttribute('aria-controls', screen.getByRole('listbox').id);

      await user.keyboard('{Escape}');

      expect(trigger).toHaveAttribute('aria-expanded', 'false');
      expect(trigger).not.toHaveAttribute('aria-controls');
    });

    it('exposes the highlighted option on the trigger with aria-activedescendant', async () => {
      const { user, trigger } = await openWithArrowDown();
      expect(trigger).not.toHaveAttribute('aria-activedescendant');

      for (const [key, label] of [
        ['{ArrowDown}', 'Apple'],
        ['{ArrowDown}', 'Banana'],
        ['{ArrowDown}', 'Cherry'],
        ['{ArrowUp}', 'Banana'],
      ] as const) {
        await user.keyboard(key);
        const outlined = outlinedOption();
        expect(outlined?.textContent.trim()).toBe(label);
        expect(trigger).toHaveAttribute('aria-activedescendant', outlined?.id);
      }
    });

    it('walks the trigger active descendant in rendered order with interleaved groups', async () => {
      const interleaved: SelectOption[] = [
        { value: 'a1', label: 'A1', group: 'A' },
        { value: 'b1', label: 'B1', group: 'B' },
        { value: 'a2', label: 'A2', group: 'A' },
      ];
      const { user, trigger } = await openWithArrowDown({ options: interleaved, groupBy: true });

      for (const label of ['A1', 'A2', 'B1']) {
        await user.keyboard('{ArrowDown}');
        const active = document.getElementById(trigger.getAttribute('aria-activedescendant') ?? '');
        expect(active?.textContent.trim()).toBe(label);
        expect(active).toBe(outlinedOption());
      }
    });

    it('drops the trigger aria-activedescendant when the options shrink below the highlight', async () => {
      const { user, trigger, rerender } = await openWithArrowDown();
      await user.keyboard('{ArrowDown}{ArrowDown}{ArrowDown}');
      expect(trigger).toHaveAttribute('aria-activedescendant');

      await rerender({ options: fruit.slice(0, 2), label: 'Fruit' });

      expect(trigger).not.toHaveAttribute('aria-activedescendant');
    });

    it('clears the trigger aria-activedescendant on close and does not restore it on reopen', async () => {
      const { user, trigger } = await openWithArrowDown();
      await user.keyboard('{ArrowDown}{ArrowDown}');
      expect(trigger).toHaveAttribute('aria-activedescendant');

      await user.keyboard('{Escape}');
      expect(trigger).not.toHaveAttribute('aria-activedescendant');

      await user.keyboard('{Enter}');
      await screen.findByRole('listbox');
      expect(trigger).not.toHaveAttribute('aria-activedescendant');
    });

    it('drops aria-activedescendant and aria-controls from the trigger after a trigger click closes the list', async () => {
      const { user, trigger } = await openWithArrowDown();
      await user.keyboard('{ArrowDown}{ArrowDown}');
      expect(trigger).toHaveAttribute('aria-activedescendant');

      // Whichever way the list closes, a closed trigger names no option and no listbox
      await user.click(trigger);

      await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
      expect(trigger).toHaveAttribute('aria-expanded', 'false');
      expect(trigger).not.toHaveAttribute('aria-activedescendant');
      expect(trigger).not.toHaveAttribute('aria-controls');
    });

    it('gives the searchable trigger and search box the same active descendant', async () => {
      const { user, search } = await openSearchable({ label: 'Fruit' });
      const trigger = screen.getByRole('combobox');

      await user.keyboard('{ArrowDown}');

      const active = search.getAttribute('aria-activedescendant');
      expect(active).toBeTruthy();
      expect(trigger).toHaveAttribute('aria-activedescendant', active);
      expect(trigger).toHaveAttribute('aria-controls', search.getAttribute('aria-controls'));
    });

    it('has no violations with the non-searchable list open and an option highlighted', async () => {
      const { user, trigger } = await openWithArrowDown();
      await user.keyboard('{ArrowDown}');
      expect(trigger).toHaveAttribute('aria-activedescendant');

      await expectNoA11yViolations(document.body, { rules: { region: { enabled: false } } });
    });

    it('exposes a required dropdown as required and leaves an optional one unmarked', () => {
      const required = selectTest.render({
        props: { options: fruit, label: 'Fruit', required: true },
      });
      expect(screen.getByRole('combobox', { name: /Fruit/ })).toHaveAttribute(
        'aria-required',
        'true'
      );
      required.unmount();

      selectTest.render({ props: { options: fruit, label: 'Fruit' } });
      expect(screen.getByRole('combobox', { name: 'Fruit' })).not.toHaveAttribute('aria-required');
    });

    it('has no violations for a required dropdown', async () => {
      selectTest.render({ props: { options: fruit, label: 'Fruit', required: true } });
      expect(screen.getByRole('combobox')).toHaveAttribute('aria-required', 'true');

      await expectNoA11yViolations(document.body, { rules: { region: { enabled: false } } });
    });

    it('keeps an empty listbox for the combobox to control and announces the empty state outside it', async () => {
      const user = userEvent.setup();
      selectTest.render({ props: { options: [], label: 'Fruit' } });

      const trigger = screen.getByRole('combobox', { name: 'Fruit' });
      await user.click(trigger);

      // The trigger's aria-controls must name a listbox, whether or not it has options
      const listbox = screen.getByRole('listbox', { name: 'Fruit' });
      expect(trigger).toHaveAttribute('aria-controls', listbox.id);
      expect(listbox).toBeEmptyDOMElement();
      // The empty-state text is a sibling of the listbox, not a child of it
      const status = screen.getByRole('status');
      expect(status).toHaveTextContent('No options found');
      expect(listbox).not.toContainElement(status);
    });

    it('has no violations with the list open and empty', async () => {
      const user = userEvent.setup();
      selectTest.render({ props: { options: [], label: 'Fruit' } });
      await user.click(screen.getByRole('combobox', { name: 'Fruit' }));
      expect(await screen.findByText('No options found')).toBeInTheDocument();

      await expectNoA11yViolations(document.body, { rules: { region: { enabled: false } } });
    });

    it('keeps the search box and the trigger pointing at the listbox when no option matches', async () => {
      const { user, search } = await openSearchable({ label: 'Fruit' });

      await user.keyboard('zzz');

      const listbox = screen.getByRole('listbox', { name: 'Fruit' });
      expect(listbox).toBeEmptyDOMElement();
      expect(search).toHaveAttribute('aria-controls', listbox.id);
      expect(screen.getByRole('combobox')).toHaveAttribute('aria-controls', listbox.id);
      expect(screen.getByRole('status')).toHaveTextContent('No options found');
    });

    it('fills the same status element when the list becomes empty, so the change is announced', async () => {
      const { user } = await openSearchable({ label: 'Fruit' });

      // The live region is present, and empty, while there are options
      const status = screen.getByRole('status');
      expect(status).toBeEmptyDOMElement();
      expect(screen.getAllByRole('option')).toHaveLength(fruit.length);

      await user.keyboard('zzz');
      expect(screen.getByRole('status')).toBe(status);
      expect(status).toHaveTextContent('No options found');

      await user.clear(screen.getByRole('searchbox'));
      expect(screen.getByRole('status')).toBe(status);
      expect(status).toBeEmptyDOMElement();
      expect(screen.getAllByRole('option')).toHaveLength(fruit.length);
    });

    it('has no violations with the list closed', async () => {
      selectTest.render({ props: { options: fruit, label: 'Fruit', value: 'apple' } });
      expect(screen.getByRole('combobox', { name: 'Fruit' })).toHaveAttribute(
        'aria-expanded',
        'false'
      );

      await expectNoA11yViolations(document.body, { rules: { region: { enabled: false } } });
    });
  });

  describe('closing and focus', () => {
    const OUTLINE_CLASS = OPTION_HIGHLIGHT_OUTLINE_CLASS.split(' ')[0];

    /** The option that carries the keyboard highlight outline, or undefined when none does. */
    function outlinedOption() {
      return screen
        .queryAllByRole('option')
        .find(option => option.classList.contains(OUTLINE_CLASS));
    }

    /** Opens the non-searchable list with Enter and highlights the second option. */
    async function openAndHighlight(props: Record<string, unknown> = {}) {
      const user = userEvent.setup();
      const onChange = vi.fn();
      selectTest.render({ props: { options: fruit, label: 'Fruit', onChange, ...props } });
      const trigger = screen.getByRole('combobox');
      trigger.focus();
      await user.keyboard('{Enter}');
      await screen.findByRole('listbox');
      await user.keyboard('{ArrowDown}{ArrowDown}');
      expect(outlinedOption()?.textContent.trim()).toBe('Banana');
      return { user, onChange, trigger };
    }

    const dialogs: HTMLElement[] = [];

    afterEach(() => {
      dialogs.splice(0).forEach(dialog => dialog.remove());
    });

    /** Renders the dropdown inside a role="dialog" container that also holds plain content. */
    function renderInDialog(props: Record<string, unknown> = {}) {
      const dialog = document.createElement('div');
      dialog.setAttribute('role', 'dialog');
      dialog.setAttribute('aria-modal', 'true');
      document.body.append(dialog);
      dialogs.push(dialog);
      const text = document.createElement('p');
      text.textContent = 'Dialog text';
      const otherButton = document.createElement('button');
      otherButton.textContent = 'Other control';
      dialog.append(text, otherButton);
      // `target` is a Svelte mount option the render helper passes through
      const rendered = selectTest.render({
        props: { options: fruit, label: 'Fruit', ...props },
        target: dialog,
      });
      return { dialog, text, otherButton, ...rendered };
    }

    it('clears the search text and highlight when the trigger closes the list', async () => {
      const { user } = await openSearchable({ label: 'Fruit' });
      const trigger = screen.getByRole('combobox');
      await user.keyboard('ban');
      expect(screen.getAllByRole('option')).toHaveLength(1);
      await user.keyboard('{ArrowDown}');
      expect(trigger).toHaveAttribute('aria-activedescendant');

      await user.click(trigger);
      await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
      await user.click(trigger);

      const search = await screen.findByRole('searchbox');
      expect(search).toHaveValue('');
      expect(screen.getAllByRole('option')).toHaveLength(fruit.length);
      expect(search).not.toHaveAttribute('aria-activedescendant');
      expect(trigger).not.toHaveAttribute('aria-activedescendant');
    });

    it('drops the highlight when a trigger click closes the non-searchable list', async () => {
      const { user, trigger } = await openAndHighlight();

      await user.click(trigger);
      await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
      await user.keyboard('{Enter}');
      await screen.findByRole('listbox');

      expect(outlinedOption()).toBeUndefined();
      expect(trigger).not.toHaveAttribute('aria-activedescendant');
    });

    it('returns focus to the trigger when a trigger click closes the list with focus on body', async () => {
      const user = userEvent.setup();
      selectTest.render({ props: { options: fruit } });
      const trigger = screen.getByRole('combobox');
      await user.click(trigger);
      await screen.findByRole('listbox');
      trigger.blur();
      expect(document.activeElement).toBe(document.body);

      // fireEvent does not move focus, like a click on a button in Safari
      await fireEvent.click(trigger);

      await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
      expect(document.activeElement).toBe(trigger);
    });

    it('returns focus to the trigger after a background click inside a dialog leaves it on body', async () => {
      const user = userEvent.setup();
      const { text } = renderInDialog();
      const trigger = screen.getByRole('combobox');
      await user.click(trigger);
      await screen.findByRole('listbox');
      trigger.blur();
      expect(document.activeElement).toBe(document.body);

      await fireEvent.click(text);

      await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
      expect(document.activeElement).toBe(trigger);
    });

    it('returns focus to the trigger after a click on the dialog backdrop leaves it on body', async () => {
      const user = userEvent.setup();
      const { dialog } = renderInDialog();
      const trigger = screen.getByRole('combobox');
      await user.click(trigger);
      await screen.findByRole('listbox');
      trigger.blur();

      await fireEvent.click(dialog);

      await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
      expect(document.activeElement).toBe(trigger);
    });

    it('does not pull focus back when a click inside a dialog lands on another control', async () => {
      const user = userEvent.setup();
      const { otherButton } = renderInDialog();
      const trigger = screen.getByRole('combobox');
      await user.click(trigger);
      await screen.findByRole('listbox');
      trigger.blur();

      await fireEvent.click(otherButton);

      await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
      expect(document.activeElement).toBe(document.body);
    });

    it('does not pull focus back when a click lands inside a different dialog', async () => {
      const user = userEvent.setup();
      renderInDialog();
      const trigger = screen.getByRole('combobox');
      // Another dialog (a login or confirmation modal) owns focus while it is open
      const foreign = document.createElement('div');
      foreign.setAttribute('role', 'dialog');
      foreign.setAttribute('aria-modal', 'true');
      const foreignText = document.createElement('p');
      foreignText.textContent = 'Foreign dialog text';
      foreign.append(foreignText);
      document.body.append(foreign);
      dialogs.push(foreign);
      await user.click(trigger);
      await screen.findByRole('listbox');
      trigger.blur();

      await fireEvent.click(foreignText);

      await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
      expect(document.activeElement).toBe(document.body);
    });

    it('restores focus from body without scrolling the trigger into view', async () => {
      const user = userEvent.setup();
      const { text } = renderInDialog();
      const trigger = screen.getByRole('combobox');
      await user.click(trigger);
      await screen.findByRole('listbox');
      trigger.blur();
      const focus = vi.spyOn(trigger, 'focus');

      // The click was somewhere else in the dialog, so the dialog must not scroll back to the trigger
      await fireEvent.click(text);

      await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
      expect(focus).toHaveBeenCalledExactlyOnceWith({ preventScroll: true });
    });

    it('scrolls the trigger into view when focus moves back from the open list', async () => {
      const { user } = await openSearchable({ label: 'Fruit' });
      const focus = vi.spyOn(screen.getByRole('combobox'), 'focus');

      await user.keyboard('{Escape}');

      await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
      expect(focus).toHaveBeenCalledExactlyOnceWith({ preventScroll: false });
    });

    it('leaves focus on body after a background click outside any dialog', async () => {
      const user = userEvent.setup();
      selectTest.render({ props: { options: fruit } });
      const trigger = screen.getByRole('combobox');
      await user.click(trigger);
      await screen.findByRole('listbox');
      trigger.blur();

      await fireEvent.click(document.body);

      await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
      expect(document.activeElement).toBe(document.body);
    });

    it('Escape on the trigger closes only the list and does not reach the document', async () => {
      const { user, trigger } = await openAndHighlight();
      const escapes = trackDocumentEscape();

      await user.keyboard('{Escape}');
      escapes.stop();

      await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
      expect(document.activeElement).toBe(trigger);
      expect(escapes.seen).toEqual([]);
    });

    describe.each([
      ['Enter', 'Enter'],
      ['Space', ' '],
    ])('%s on the open trigger', (_name, key) => {
      it('is cancelled and closes the list when nothing is highlighted', async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();
        selectTest.render({ props: { options: fruit, label: 'Fruit', onChange } });
        const trigger = screen.getByRole('combobox');
        await user.click(trigger);
        await screen.findByRole('listbox');

        // A cancelled keydown means the browser fires no click, so the list closes once, here
        expect(await fireEvent.keyDown(trigger, { key })).toBe(false);

        await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
        expect(onChange).not.toHaveBeenCalled();
      });

      it('leaves the list closed after a full key press with nothing highlighted', async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();
        selectTest.render({ props: { options: fruit, label: 'Fruit', onChange } });
        const trigger = screen.getByRole('combobox');
        trigger.focus();
        await user.keyboard('{ArrowDown}');
        await screen.findByRole('listbox');

        await user.keyboard(key === ' ' ? ' ' : '{Enter}');

        await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
        expect(onChange).not.toHaveBeenCalled();
      });

      it('ignores the repeats of a held key instead of toggling the list', async () => {
        const user = userEvent.setup();
        selectTest.render({ props: { options: fruit, label: 'Fruit' } });
        const trigger = screen.getByRole('combobox');
        trigger.focus();

        // The first keydown of the press opens the list; the held key then repeats
        await fireEvent.keyDown(trigger, { key });
        await screen.findByRole('listbox');
        // One repeat, not two: a second would toggle the list back and hide a missing guard
        expect(await fireEvent.keyDown(trigger, { key, repeat: true })).toBe(false);

        expect(trigger).toHaveAttribute('aria-expanded', 'true');
        expect(screen.getByRole('listbox')).toBeInTheDocument();
        await user.keyboard('{Escape}');
        await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());

        // A repeat that arrives while the list is closed does not open it
        await fireEvent.keyDown(trigger, { key, repeat: true });
        expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
      });
    });

    describe.each([
      ['Enter', '{Enter}'],
      ['Space', ' '],
    ])('%s on the open trigger with an option highlighted', (_name, press) => {
      it('keeps the list open when the highlighted option is disabled', async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();
        const withDisabled: SelectOption[] = [
          { value: 'apple', label: 'Apple' },
          { value: 'banana', label: 'Banana', disabled: true },
          { value: 'cherry', label: 'Cherry' },
        ];
        selectTest.render({ props: { options: withDisabled, label: 'Fruit', onChange } });
        screen.getByRole('combobox').focus();
        await user.keyboard('{ArrowDown}{ArrowDown}{ArrowDown}');
        expect(outlinedOption()?.textContent.trim()).toBe('Banana');

        await user.keyboard(press);

        expect(screen.getByRole('listbox')).toBeInTheDocument();
        expect(onChange).not.toHaveBeenCalled();
      });

      it.each([
        ['first', '{ArrowDown}{ArrowDown}', 'apple'],
        ['last', '{ArrowDown}{ArrowUp}', 'cherry'],
      ])(
        'selects the %s option and closes the single-select list',
        async (_which, moves, value) => {
          const user = userEvent.setup();
          const onChange = vi.fn();
          selectTest.render({ props: { options: fruit, label: 'Fruit', onChange } });
          const trigger = screen.getByRole('combobox');
          trigger.focus();
          await user.keyboard(moves);
          expect(outlinedOption()).toBeDefined();

          await user.keyboard(press);

          expect(onChange).toHaveBeenCalledExactlyOnceWith(value);
          await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
          expect(document.activeElement).toBe(trigger);
        }
      );

      it('toggles the highlighted option and keeps the multiple-select list open', async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();
        selectTest.render({ props: { options: fruit, label: 'Fruit', multiple: true, onChange } });
        screen.getByRole('combobox').focus();
        await user.keyboard('{ArrowDown}{ArrowDown}');

        await user.keyboard(press);
        expect(onChange).toHaveBeenLastCalledWith(['apple']);
        await user.keyboard(press);
        expect(onChange).toHaveBeenLastCalledWith([]);

        expect(screen.getByRole('listbox')).toBeInTheDocument();
      });

      it('ignores the repeats of a held key on a highlighted multiple-select option', async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();
        selectTest.render({ props: { options: fruit, label: 'Fruit', multiple: true, onChange } });
        const trigger = screen.getByRole('combobox');
        trigger.focus();
        await user.keyboard('{ArrowDown}{ArrowDown}');
        await user.keyboard(press);
        expect(onChange).toHaveBeenCalledExactlyOnceWith(['apple']);
        // Odd count of repeats: an unguarded handler would toggle the option off again
        const key = press === ' ' ? ' ' : 'Enter';

        await fireEvent.keyDown(trigger, { key, repeat: true });

        expect(onChange).toHaveBeenCalledTimes(1);
        expect(screen.getByRole('listbox')).toBeInTheDocument();
      });

      it('closes without selecting when the options shrank below the highlight', async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();
        const { rerender } = selectTest.render({
          props: { options: fruit, label: 'Fruit', onChange },
        });
        screen.getByRole('combobox').focus();
        await user.keyboard('{ArrowDown}{ArrowDown}{ArrowDown}{ArrowDown}');
        await rerender({ options: fruit.slice(0, 1), label: 'Fruit', onChange });

        await user.keyboard(press);

        await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
        expect(onChange).not.toHaveBeenCalled();
      });
    });

    type User = ReturnType<typeof userEvent.setup>;

    it.each([
      ['a trigger click', (user: User, trigger: HTMLElement) => user.click(trigger)],
      ['Escape', (user: User) => user.keyboard('{Escape}')],
      ['Tab', (user: User) => user.keyboard('{Tab}')],
      ['an outside click', (user: User) => user.click(addButton('Outside'))],
      ['selecting an option', (user: User) => user.click(screen.getAllByRole('option')[2])],
    ])(
      'starts the next opening without a highlight after closing with %s',
      async (_name, close) => {
        const { user, trigger } = await openAndHighlight();

        await close(user, trigger);
        await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
        trigger.focus();
        await user.keyboard('{Enter}');
        await screen.findByRole('listbox');

        expect(outlinedOption()).toBeUndefined();
        expect(trigger).not.toHaveAttribute('aria-activedescendant');
      }
    );
  });
});
