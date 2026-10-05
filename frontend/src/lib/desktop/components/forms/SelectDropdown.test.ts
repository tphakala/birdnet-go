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
      const { rerender } = selectTest.render({
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

    it('shows selectable options for multiple selection', async () => {
      selectTest.render({
        props: {
          options: basicOptions,
          multiple: true,
        },
      });

      await fireEvent.click(screen.getByRole('button'));

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
      expect(options[0]).toHaveClass('bg-[var(--color-base-200)]');

      // Second ArrowDown should highlight second option
      await user.keyboard('{ArrowDown}');
      expect(options[1]).toHaveClass('bg-[var(--color-base-200)]');
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
    await user.click(screen.getAllByRole('button')[0]);
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
    expect(document.activeElement).toBe(screen.getByRole('button'));
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
    await user.click(screen.getAllByRole('button')[0]);
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
    expect(screen.getByRole('listbox')).toBeInTheDocument();
  });

  it('Escape in the search box closes only the list and does not reach the document', async () => {
    const { user } = await openSearchable();
    const escapes = trackDocumentEscape();

    await user.keyboard('{Escape}');
    escapes.stop();

    await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
    expect(document.activeElement).toBe(screen.getByRole('button'));
    expect(escapes.seen).toEqual([]);
  });

  it('Escape on a focused option closes only the list and does not reach the document', async () => {
    const user = userEvent.setup();
    selectTest.render({ props: { options: fruit, multiple: true } });
    await user.click(screen.getByRole('button'));
    const option = (await screen.findAllByRole('option'))[0];
    option.focus();
    const escapes = trackDocumentEscape();

    await user.keyboard('{Escape}');
    escapes.stop();

    await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
    expect(document.activeElement).toBe(screen.getByRole('button'));
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
    await user.click(screen.getByRole('button', { name: /select/i }));
    await waitFor(() => expect(screen.getByRole('searchbox')).toHaveFocus());

    await user.keyboard('{Tab}');

    await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
    expect(document.activeElement).toBe(after);
  });

  it('Shift+Tab from a focused option closes the list and moves focus on from the trigger', async () => {
    const user = userEvent.setup();
    const { before } = renderBetweenButtons({ multiple: true });
    await user.click(screen.getByRole('button', { name: /select/i }));
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
    expect(document.activeElement).toBe(screen.getByRole('button'));
  });

  it('returns focus to the trigger when focus was on body at selection time', async () => {
    const { onChange, search } = await openSearchable();
    search.blur();
    expect(document.activeElement).toBe(document.body);

    // A click on a button that takes no focus (Safari) does not move focus to the option
    await fireEvent.click(screen.getAllByRole('option')[0]);

    expect(onChange).toHaveBeenCalledWith('apple');
    await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
    expect(document.activeElement).toBe(screen.getByRole('button'));
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
    const trigger = screen.getByRole('button');
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

    await user.click(screen.getByRole('button', { name: /select/i }));
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

    await user.click(screen.getByRole('button'));

    expect(await screen.findByRole('listbox', { name: 'Audio Device' })).toBeInTheDocument();
  });

  it('keeps the label prop as the listbox name and adds no aria-label without any label', async () => {
    const user = userEvent.setup();
    const labelled = selectTest.render({ props: { options: fruit, label: 'Fruit' } });
    await user.click(screen.getByRole('button'));
    const named = await screen.findByRole('listbox', { name: 'Fruit' });
    expect(named).toHaveAttribute('aria-labelledby');
    expect(named).not.toHaveAttribute('aria-label');
    labelled.unmount();

    selectTest.render({ props: { options: fruit } });
    await user.click(screen.getByRole('button'));
    const unnamed = await screen.findByRole('listbox');
    expect(unnamed).not.toHaveAttribute('aria-label');
  });

  it('links help text and the aria-describedby prop together on the trigger', () => {
    const both = selectTest.render({
      props: { options: fruit, id: 'both', helpText: 'Help', 'aria-describedby': 'extra-note' },
    });
    expect(screen.getByRole('button').getAttribute('aria-describedby')).toBe(
      'both-help extra-note'
    );
    both.unmount();

    const only = selectTest.render({
      props: { options: fruit, id: 'only', 'aria-describedby': 'extra-note' },
    });
    expect(screen.getByRole('button').getAttribute('aria-describedby')).toBe('extra-note');
    only.unmount();

    selectTest.render({ props: { options: fruit, id: 'none' } });
    expect(screen.getByRole('button')).not.toHaveAttribute('aria-describedby');
  });

  it('has no violations with the searchable list open and an option highlighted', async () => {
    const { user, search } = await openSearchable({ label: 'Fruit' });
    await user.keyboard('{ArrowDown}');
    expect(search).toHaveAttribute('aria-activedescendant');

    // The landmark rule is about whole pages, not an isolated component
    await expectNoA11yViolations(document.body, { rules: { region: { enabled: false } } });
  });
});
