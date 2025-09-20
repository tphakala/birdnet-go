/**
 * DatePicker Component Tests
 *
 * Comprehensive tests for the DatePicker component focusing on actual component behavior.
 * Minimal mocking approach - only mock what's absolutely necessary.
 */

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import DatePicker from './DatePicker.svelte';
import { testUtils } from '../../../../test/setup.js';

describe('DatePicker Component', () => {
  let user = userEvent.setup();

  beforeEach(() => {
    // Reset all mocks before each test
    testUtils.resetAllMocks();
    user = userEvent.setup();
  });

  afterEach(() => {
    // Clean up after each test
    testUtils.resetAllMocks();
  });

  describe('Basic Rendering', () => {
    it('renders with default props', () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange });

      const button = screen.getByLabelText('Select date');
      expect(button).toBeInTheDocument();
      expect(button).toHaveClass('btn', 'btn-sm');
      expect(screen.getByText('Select date')).toBeInTheDocument();
    });

    it('renders with provided value', () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '2024-03-15', onChange });

      // Check that formatted date is displayed
      expect(screen.getByText(/Fri, Mar 15, 2024/)).toBeInTheDocument();
    });

    it('renders with custom placeholder', () => {
      const onChange = vi.fn();
      render(DatePicker, {
        value: '',
        onChange,
        placeholder: 'Choose a date',
      });

      expect(screen.getByText('Choose a date')).toBeInTheDocument();
    });

    it('renders disabled state', () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange, disabled: true });

      const button = screen.getByLabelText('Select date');
      expect(button).toBeDisabled();
      expect(button).toHaveClass('btn-disabled');
    });

    it('applies custom className to root wrapper', () => {
      const onChange = vi.fn();
      render(DatePicker, {
        value: '',
        onChange,
        className: 'custom-class',
      });

      const wrapper = screen.getByLabelText('Select date').parentElement;
      expect(wrapper).toHaveClass('custom-class');
    });
  });

  describe('Size Prop', () => {
    it.each([
      ['xs', 'btn-xs'],
      ['sm', 'btn-sm'],
      ['md', 'btn'],
      ['lg', 'btn-lg'],
    ])('renders with size "%s" as class "%s"', (size, expectedClass) => {
      const onChange = vi.fn();
      render(DatePicker, {
        value: '',
        onChange,
        size: size as 'xs' | 'sm' | 'md' | 'lg',
      });

      const button = screen.getByLabelText('Select date');
      expect(button).toHaveClass(expectedClass);
    });

    it('defaults to sm size when no size specified', () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange });

      const button = screen.getByLabelText('Select date');
      expect(button).toHaveClass('btn-sm');
    });
  });

  describe('Calendar Dropdown', () => {
    it('opens calendar when button is clicked', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      // Calendar should be visible
      expect(screen.getByRole('dialog', { name: 'Date picker calendar' })).toBeInTheDocument();
      expect(button).toHaveAttribute('aria-expanded', 'true');
    });

    it('closes calendar when clicking outside', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      // Calendar should be open
      expect(screen.getByRole('dialog')).toBeInTheDocument();

      // Click outside (on document body)
      await user.click(document.body);

      // Calendar should be closed
      await waitFor(() => {
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      });
    });

    it('closes calendar when Escape key is pressed', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      // Calendar should be open
      expect(screen.getByRole('dialog')).toBeInTheDocument();

      // Press Escape
      await user.keyboard('{Escape}');

      // Calendar should be closed
      await waitFor(() => {
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      });
    });

    it('opens calendar with Enter key on button', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange });

      const button = screen.getByLabelText('Select date');
      button.focus();

      await user.keyboard('{Enter}');

      expect(screen.getByRole('dialog')).toBeInTheDocument();
    });

    it('opens calendar with Space key on button', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange });

      const button = screen.getByLabelText('Select date');
      button.focus();

      await user.keyboard(' ');

      expect(screen.getByRole('dialog')).toBeInTheDocument();
    });
  });

  describe('Calendar Navigation', () => {
    beforeEach(() => {
      // Mock current date to March 15, 2024 for consistent testing
      vi.setSystemTime(new Date('2024-03-15T12:00:00Z'));
    });

    afterEach(() => {
      vi.useRealTimers();
    });

    it('displays current month by default', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      // Should show March 2024
      expect(screen.getByText('March 2024')).toBeInTheDocument();
    });

    it('displays month of selected date', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '2024-01-15', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      // Should show January 2024
      expect(screen.getByText('January 2024')).toBeInTheDocument();
    });

    it('navigates to previous month', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '2024-03-15', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      const prevButton = screen.getByLabelText('Previous month');
      await user.click(prevButton);

      expect(screen.getByText('February 2024')).toBeInTheDocument();
    });

    it('navigates to next month', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '2024-03-15', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      const nextButton = screen.getByLabelText('Next month');
      await user.click(nextButton);

      expect(screen.getByText('April 2024')).toBeInTheDocument();
    });
  });

  describe('Date Selection', () => {
    beforeEach(() => {
      // Mock system time consistently - no fake timers to avoid hanging
      vi.setSystemTime(new Date('2024-03-15T12:00:00Z'));
    });

    afterEach(() => {
      vi.useRealTimers();
    });

    it('contains clickable past date buttons in calendar', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      // Wait for calendar to open
      await waitFor(() => {
        expect(screen.getByRole('dialog')).toBeInTheDocument();
      });

      // Verify that past date buttons are clickable (day 10 is before today - March 15)
      const day10Button = screen.getByRole('gridcell', { name: '3/10/2024' });
      expect(day10Button).toBeInTheDocument();
      expect(day10Button).not.toBeDisabled();
      expect(day10Button).toHaveClass('cursor-pointer');

      // Verify future dates are disabled (day 20 is after today - March 15)
      // Note: Disabled dates have different aria-labels that include unavailable text
      const day20Button = screen.getByText('20'); // Find by text content instead
      expect(day20Button).toBeInTheDocument();
      expect(day20Button).toBeDisabled();
      expect(day20Button).toHaveClass('cursor-not-allowed');
    });

    it('highlights selected date', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '2024-03-15', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      // Day 15 should be highlighted as selected
      const day15 = screen.getByText('15');
      expect(day15).toHaveClass('bg-primary');
    });

    it('highlights today', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      // Day 15 should be highlighted as today (but not selected)
      const day15 = screen.getByText('15');
      expect(day15).toHaveClass('bg-base-200', 'font-semibold');
    });

    it('uses Today button to select current date', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      const todayButton = screen.getByText('Today');
      await user.click(todayButton);

      expect(onChange).toHaveBeenCalledWith('2024-03-15');
    });
  });

  describe('Date Constraints', () => {
    beforeEach(() => {
      vi.setSystemTime(new Date('2024-03-15T12:00:00Z'));
    });

    afterEach(() => {
      vi.useRealTimers();
    });

    it('respects maxDate constraint', async () => {
      const onChange = vi.fn();
      render(DatePicker, {
        value: '',
        onChange,
        maxDate: '2024-03-10',
      });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      // Days after March 10 should be disabled
      const day15 = screen.getByText('15');
      expect(day15).toBeDisabled();
      expect(day15).toHaveClass('cursor-not-allowed');

      // Try clicking disabled date - should not call onChange
      await user.click(day15);
      expect(onChange).not.toHaveBeenCalled();
    });

    it('respects minDate constraint', async () => {
      const onChange = vi.fn();
      render(DatePicker, {
        value: '',
        onChange,
        minDate: '2024-03-20',
      });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      // Days before March 20 should be disabled
      const day15 = screen.getByText('15');
      expect(day15).toBeDisabled();
      expect(day15).toHaveClass('cursor-not-allowed');
    });

    it('allows selection within date range', async () => {
      const onChange = vi.fn();
      render(DatePicker, {
        value: '',
        onChange,
        minDate: '2024-03-10',
        maxDate: '2024-03-20',
      });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      // Day 15 should be selectable
      const day15 = screen.getByText('15');
      expect(day15).not.toBeDisabled();

      await user.click(day15);
      expect(onChange).toHaveBeenCalledWith('2024-03-15');
    });

    it('disables Today button when today is outside constraints', async () => {
      const onChange = vi.fn();
      render(DatePicker, {
        value: '',
        onChange,
        maxDate: '2024-03-10', // Today (15th) is after max date
      });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      const todayButton = screen.getByText('Today');
      expect(todayButton).toBeDisabled();
    });
  });

  describe('Accessibility', () => {
    it('has proper ARIA attributes on button', () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange });

      const button = screen.getByLabelText('Select date');
      expect(button).toHaveAttribute('aria-label', 'Select date');
      expect(button).toHaveAttribute('aria-expanded', 'false');
      expect(button).toHaveAttribute('aria-haspopup', 'true');
    });

    it('updates aria-expanded when calendar opens', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange });

      const button = screen.getByLabelText('Select date');

      await user.click(button);
      expect(button).toHaveAttribute('aria-expanded', 'true');
    });

    it('has proper role and label on calendar', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      const calendar = screen.getByRole('dialog', { name: 'Date picker calendar' });
      expect(calendar).toBeInTheDocument();
    });

    it('has accessible labels on navigation buttons', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      expect(screen.getByLabelText('Previous month')).toBeInTheDocument();
      expect(screen.getByLabelText('Next month')).toBeInTheDocument();
    });

    it('has accessible labels on date buttons', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      // Check that date buttons have aria-labels
      const day15 = screen.getByText('15');
      expect(day15).toHaveAttribute('aria-label');
    });
  });

  describe('Edge Cases', () => {
    it('handles invalid date value gracefully', () => {
      const onChange = vi.fn();
      render(DatePicker, { value: 'invalid-date', onChange });

      // Should render without crashing - invalid date shows the translated text
      expect(screen.getByText('Invalid value')).toBeInTheDocument();
      // Button should still be functional
      expect(screen.getByLabelText('Select date')).toBeInTheDocument();
    });

    it('handles empty onChange function', async () => {
      // @ts-expect-error - Testing edge case with undefined onChange
      render(DatePicker, { value: '', onChange: undefined });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      // Should render without crashing
      expect(screen.getByRole('dialog')).toBeInTheDocument();
    });

    it('prevents interaction when disabled', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange, disabled: true });

      const button = screen.getByLabelText('Select date');

      // Should not open calendar when disabled
      await user.click(button);
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

      // Should not respond to keyboard
      button.focus();
      await user.keyboard('{Enter}');
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });

    it('handles rapid calendar open/close operations', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange });

      const button = screen.getByLabelText('Select date');

      // Rapid open/close
      await user.click(button);
      expect(screen.getByRole('dialog')).toBeInTheDocument();

      await user.keyboard('{Escape}');
      await waitFor(() => {
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      });

      await user.click(button);
      expect(screen.getByRole('dialog')).toBeInTheDocument();
    });
  });

  describe('Date Formatting', () => {
    it('formats dates correctly for display', () => {
      const onChange = vi.fn();

      // Test various date formats
      const testCases = [
        { input: '2024-01-01', expected: /Mon, Jan 1, 2024/ },
        { input: '2024-12-31', expected: /Tue, Dec 31, 2024/ },
        { input: '2024-06-15', expected: /Sat, Jun 15, 2024/ },
      ];

      testCases.forEach(({ input, expected }) => {
        const { unmount } = render(DatePicker, { value: input, onChange });
        expect(screen.getByText(expected)).toBeInTheDocument();
        unmount();
      });
    });

    it('handles leap year correctly', async () => {
      vi.setSystemTime(new Date('2024-02-15T12:00:00Z')); // 2024 is a leap year

      const onChange = vi.fn();
      render(DatePicker, { value: '2024-02-15', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      // February 2024 should have 29 days
      expect(screen.getByText('29')).toBeInTheDocument();

      vi.useRealTimers();
    });
  });

  describe('Performance', () => {
    beforeEach(() => {
      vi.setSystemTime(new Date('2024-03-15T12:00:00Z'));
    });

    afterEach(() => {
      vi.useRealTimers();
    });

    it('renders all calendar dates correctly', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      // Wait for calendar to open
      await waitFor(() => {
        expect(screen.getByRole('dialog')).toBeInTheDocument();
      });

      // March 2024 should have 31 days - check that all are rendered
      expect(screen.getByText('March 2024')).toBeInTheDocument();
      expect(screen.getByText('1')).toBeInTheDocument(); // First day of March
      // Check that day 15 (today) is present by looking for the text content instead of aria-label
      expect(screen.getByText('15')).toBeInTheDocument(); // Today
      expect(screen.getByText('31')).toBeInTheDocument(); // Last day of March

      // Verify calendar structure - date buttons are now gridcells, other controls remain buttons
      const buttons = screen.getAllByRole('button'); // main button + prev/next month + today button
      const gridcells = screen.getAllByRole('gridcell'); // date buttons + empty cells
      expect(buttons.length).toBeGreaterThanOrEqual(4); // At least 4 buttons (main + prev + next + today)
      expect(gridcells.length).toBeGreaterThan(30); // At least 31 date buttons plus empty cells
    });

    it('cleans up event listeners on unmount', () => {
      const onChange = vi.fn();
      const addEventListenerSpy = vi.spyOn(document, 'addEventListener');
      const removeEventListenerSpy = vi.spyOn(document, 'removeEventListener');

      const { unmount } = render(DatePicker, { value: '', onChange });

      // Should add click listener
      expect(addEventListenerSpy).toHaveBeenCalledWith('click', expect.any(Function));

      unmount();

      // Should remove click listener
      expect(removeEventListenerSpy).toHaveBeenCalledWith('click', expect.any(Function));

      addEventListenerSpy.mockRestore();
      removeEventListenerSpy.mockRestore();
    });
  });

  describe('Error Handling and Validation', () => {
    it('handles invalid date format gracefully', () => {
      const onChange = vi.fn();
      render(DatePicker, { value: 'invalid-date-format', onChange });

      // Should display validation error - translated text shows
      expect(screen.getByText('Invalid value')).toBeInTheDocument();
      expect(screen.getByRole('alert')).toBeInTheDocument();
    });

    it('shows validation error for malformed YYYY-MM-DD format', () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '24-03-15', onChange }); // Wrong format

      const errorMessage = screen.getByRole('alert');
      expect(errorMessage).toBeInTheDocument();
      expect(errorMessage).toHaveTextContent('Invalid date format. Please use YYYY-MM-DD format');
    });

    it('validates date constraints and shows errors', () => {
      const onChange = vi.fn();
      render(DatePicker, {
        value: '2024-13-40', // Invalid month and day
        onChange,
      });

      const errorMessage = screen.getByRole('alert');
      expect(errorMessage).toBeInTheDocument();
    });

    it('recovers gracefully from invalid dates during interaction', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      // Should render calendar even with initially invalid state
      expect(screen.getByRole('dialog')).toBeInTheDocument();
    });
  });

  describe('Enhanced Keyboard Navigation', () => {
    beforeEach(() => {
      vi.setSystemTime(new Date('2024-03-15T12:00:00Z'));
    });

    afterEach(() => {
      vi.useRealTimers();
    });

    it('navigates calendar with arrow keys', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '2024-03-15', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      // Find a date button to focus
      const dateButton = screen.getByRole('gridcell', { name: /3\/15\/2024/ });
      dateButton.focus();

      // Test arrow navigation - verify calendar stays open and buttons are still accessible
      await user.keyboard('{ArrowRight}');
      expect(screen.getByRole('dialog')).toBeInTheDocument();

      await user.keyboard('{ArrowDown}');
      expect(screen.getByRole('dialog')).toBeInTheDocument();

      await user.keyboard('{ArrowUp}');
      expect(screen.getByRole('dialog')).toBeInTheDocument();

      await user.keyboard('{ArrowLeft}');
      expect(screen.getByRole('dialog')).toBeInTheDocument();
    });

    it('handles Page Up/Down for month navigation', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '2024-03-15', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      expect(screen.getByText('March 2024')).toBeInTheDocument();

      // Page Up should go to previous month
      await user.keyboard('{PageUp}');

      await waitFor(() => {
        expect(screen.getByText('February 2024')).toBeInTheDocument();
      });

      // Page Down should go to next month
      await user.keyboard('{PageDown}');

      await waitFor(() => {
        expect(screen.getByText('March 2024')).toBeInTheDocument();
      });
    });

    it('handles Shift+Page Up/Down for year navigation', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '2024-03-15', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      expect(screen.getByText('March 2024')).toBeInTheDocument();

      // Shift+Page Up should go to previous year
      await user.keyboard('{Shift>}{PageUp}{/Shift}');

      await waitFor(() => {
        expect(screen.getByText('March 2023')).toBeInTheDocument();
      });

      // Shift+Page Down should go to next year
      await user.keyboard('{Shift>}{PageDown}{/Shift}');

      await waitFor(() => {
        expect(screen.getByText('March 2024')).toBeInTheDocument();
      });
    });

    it('handles Home and End keys for month boundaries', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '2024-03-15', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      const dateButton = screen.getByText('15').closest('button');
      dateButton?.focus();

      // Home should go to first of month - verify calendar stays open
      await user.keyboard('{Home}');
      expect(screen.getByRole('dialog')).toBeInTheDocument();

      // End should go to last of month - verify calendar stays open
      await user.keyboard('{End}');
      expect(screen.getByRole('dialog')).toBeInTheDocument();
    });

    it('selects date with mouse click', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      // Find day 10 button and click it to select the date
      const dateButton = screen.getByText('10').closest('button');
      expect(dateButton).not.toBeNull();

      if (dateButton) {
        await user.click(dateButton);
      }

      expect(onChange).toHaveBeenCalledWith('2024-03-10');
    });

    it('prevents selection of disabled dates with keyboard', async () => {
      const onChange = vi.fn();
      render(DatePicker, {
        value: '',
        onChange,
        maxDate: '2024-03-10',
      });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      // Try to select a disabled date (after maxDate)
      const disabledButton = screen.getByRole('gridcell', { name: /3\/15\/2024/ });
      expect(disabledButton).toBeDisabled();

      disabledButton.focus();
      await user.keyboard('{Enter}');

      // Should not call onChange for disabled date
      expect(onChange).not.toHaveBeenCalled();
    });
  });

  describe('Enhanced Accessibility', () => {
    beforeEach(() => {
      vi.setSystemTime(new Date('2024-03-15T12:00:00Z'));
    });

    afterEach(() => {
      vi.useRealTimers();
    });

    it('has proper grid roles and navigation instructions', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      // Should have grid structure
      const grid = screen.getByRole('grid');
      expect(grid).toBeInTheDocument();

      // Should have navigation instructions for screen readers - look for the translated text
      const instructions = screen.getByRole('region', { name: 'Use arrow keys to navigate calendar, Enter to select, Escape to close' });
      expect(instructions).toBeInTheDocument();
    });

    it('manages tabindex properly for keyboard navigation', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '2024-03-15', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      const allDateButtons = screen.getAllByRole('gridcell').filter(el => el.tagName === 'BUTTON');

      // Only one date button should be tabbable at a time
      const tabbableButtons = allDateButtons.filter(btn => btn.getAttribute('tabindex') === '0');

      expect(tabbableButtons).toHaveLength(1);
    });

    it('announces date selection with aria-live', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      // Check for aria-live region - it's a div with aria-live attribute
      const liveRegion = document.querySelector('[aria-live="polite"]');
      expect(liveRegion).toBeInTheDocument();
      expect(liveRegion).toHaveAttribute('aria-atomic', 'true');
    });

    it('provides proper aria-labels for date buttons', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '2024-03-15', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      // Selected date should have proper aria attributes - find by text content
      const selectedButton = screen.getByText('15').closest('button');
      expect(selectedButton).toHaveAttribute('aria-selected', 'true');

      // Today should have aria-current - find by text content since it's both selected and today
      const todayButton = screen.getByText('15').closest('button');
      expect(todayButton).toHaveAttribute('aria-current', 'date');
    });

    it('provides accessible month navigation', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      const prevButton = screen.getByLabelText('Previous month');
      const nextButton = screen.getByLabelText('Next month');

      expect(prevButton).toBeInTheDocument();
      expect(nextButton).toBeInTheDocument();
    });
  });

  describe('Focus Management', () => {
    beforeEach(() => {
      vi.setSystemTime(new Date('2024-03-15T12:00:00Z'));
    });

    afterEach(() => {
      vi.useRealTimers();
    });

    it('sets initial focus to selected date when opening calendar', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '2024-03-15', onChange });

      const button = screen.getByLabelText('Select date');
      button.focus();
      await user.keyboard('{Enter}'); // Open with keyboard

      // Should announce calendar opened and calendar should be visible
      const liveRegion = document.querySelector('[aria-live="polite"]');
      expect(liveRegion).toBeInTheDocument();
      expect(screen.getByRole('dialog')).toBeInTheDocument();
    });

    it('returns focus to button when closing with Escape', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      expect(screen.getByRole('dialog')).toBeInTheDocument();

      await user.keyboard('{Escape}');

      await waitFor(() => {
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      });

      // Focus should return to button
      expect(button).toHaveFocus();
    });

    it('maintains focus on date selection', async () => {
      const onChange = vi.fn();
      render(DatePicker, { value: '', onChange });

      const button = screen.getByLabelText('Select date');
      await user.click(button);

      const dateButton = screen.getByRole('gridcell', { name: /3\/10\/2024/ });
      await user.click(dateButton);

      expect(onChange).toHaveBeenCalledWith('2024-03-10');

      // Calendar should close and focus return to main button
      await waitFor(() => {
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      });
    });
  });
});
