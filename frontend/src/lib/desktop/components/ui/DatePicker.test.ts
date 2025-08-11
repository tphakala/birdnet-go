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

    it('applies custom className', () => {
      const onChange = vi.fn();
      render(DatePicker, {
        value: '',
        onChange,
        className: 'custom-class',
      });

      const button = screen.getByLabelText('Select date');
      expect(button).toHaveClass('custom-class');
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
      const day10Button = screen.getByRole('button', { name: '3/10/2024' });
      expect(day10Button).toBeInTheDocument();
      expect(day10Button).not.toBeDisabled();
      expect(day10Button).toHaveClass('cursor-pointer');

      // Verify future dates are disabled (day 20 is after today - March 15)
      const day20Button = screen.getByRole('button', { name: '3/20/2024' });
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

      // Should render without crashing - invalid date shows "Invalid Date"
      expect(screen.getByText('Invalid Date')).toBeInTheDocument();
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
      expect(screen.getByRole('button', { name: '3/1/2024' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: '3/15/2024' })).toBeInTheDocument(); // Today
      expect(screen.getByRole('button', { name: '3/31/2024' })).toBeInTheDocument();

      // Verify calendar structure
      expect(screen.getAllByRole('button')).toHaveLength(35); // 31 days + prev/next month + today button + main button
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
});
