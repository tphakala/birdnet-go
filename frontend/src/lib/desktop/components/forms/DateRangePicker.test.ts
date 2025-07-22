import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import DateRangePicker from './DateRangePicker.svelte';

// Test helper to avoid TypeScript casting repetition
function renderDateRangePicker(props: Record<string, unknown> = {}) {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return render(DateRangePicker as any, { props });
}

describe('DateRangePicker', () => {
  beforeEach(() => {
    // Set a fixed date for consistent testing
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2024-01-15Z'));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('renders with default labels', () => {
    renderDateRangePicker();

    expect(screen.getByLabelText('Start Date')).toBeInTheDocument();
    expect(screen.getByLabelText('End Date')).toBeInTheDocument();
  });

  it('renders with custom labels', () => {
    renderDateRangePicker({
      startLabel: 'From',
      endLabel: 'To',
    });

    expect(screen.getByLabelText('From')).toBeInTheDocument();
    expect(screen.getByLabelText('To')).toBeInTheDocument();
  });

  it('displays initial dates', () => {
    renderDateRangePicker({
      startDate: new Date('2024-01-01Z'),
      endDate: new Date('2024-01-31Z'),
    });

    const startInput = screen.getByLabelText('Start Date') as HTMLInputElement;
    const endInput = screen.getByLabelText('End Date') as HTMLInputElement;

    expect(startInput.value).toBe('2024-01-01');
    expect(endInput.value).toBe('2024-01-31');
  });

  it('handles date changes', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup({ delay: null });

    renderDateRangePicker({ onChange });

    const startInput = screen.getByLabelText('Start Date');
    const endInput = screen.getByLabelText('End Date');

    await user.type(startInput, '2024-01-10');

    expect(onChange).toHaveBeenCalledWith({
      startDate: new Date('2024-01-10Z'),
      endDate: null,
    });

    await user.type(endInput, '2024-01-20');

    expect(onChange).toHaveBeenCalledWith({
      startDate: new Date('2024-01-10Z'),
      endDate: new Date('2024-01-20Z'),
    });
  });

  it('validates date range', async () => {
    const user = userEvent.setup({ delay: null });

    renderDateRangePicker({
      startDate: new Date('2024-01-10Z'),
    });

    const endInput = screen.getByLabelText('End Date');

    // Type an end date before the start date
    await user.type(endInput, '2024-01-05');

    // Should show error when end date is before start date
    await waitFor(() => {
      expect(screen.getByText('End date cannot be before start date')).toBeInTheDocument();
    });
  });

  it('enforces min and max dates', () => {
    renderDateRangePicker({
      minDate: new Date('2024-01-01Z'),
      maxDate: new Date('2024-12-31Z'),
    });

    const startInput = screen.getByLabelText('Start Date') as HTMLInputElement;
    const endInput = screen.getByLabelText('End Date') as HTMLInputElement;

    expect(startInput.min).toBe('2024-01-01');
    expect(startInput.max).toBe('2024-12-31');
    expect(endInput.min).toBe('2024-01-01');
    expect(endInput.max).toBe('2024-12-31');
  });

  it('updates end date min based on start date', async () => {
    const user = userEvent.setup({ delay: null });

    renderDateRangePicker();

    const startInput = screen.getByLabelText('Start Date');
    const endInput = screen.getByLabelText('End Date') as HTMLInputElement;

    await user.type(startInput, '2024-01-15');

    expect(endInput.min).toBe('2024-01-15');
  });

  describe('Presets', () => {
    it('renders default presets', () => {
      renderDateRangePicker();

      expect(screen.getByText('Today')).toBeInTheDocument();
      expect(screen.getByText('Yesterday')).toBeInTheDocument();
      expect(screen.getByText('Last 7 days')).toBeInTheDocument();
      expect(screen.getByText('Last 30 days')).toBeInTheDocument();
      expect(screen.getByText('This month')).toBeInTheDocument();
      expect(screen.getByText('Last month')).toBeInTheDocument();
      expect(screen.getByText('This year')).toBeInTheDocument();
    });

    it('applies preset when clicked', async () => {
      const onChange = vi.fn();

      renderDateRangePicker({
        onChange,
      });

      await fireEvent.click(screen.getByText('Today'));

      // The component creates dates at midnight local time
      const today = new Date(2024, 0, 15); // January 15, 2024 at midnight
      expect(onChange).toHaveBeenCalledWith({
        startDate: today,
        endDate: today,
      });
    });

    it('highlights active preset', async () => {
      renderDateRangePicker();

      await fireEvent.click(screen.getByText('Today'));

      const todayButton = screen.getByText('Today');
      expect(todayButton).toHaveClass('btn-primary');
    });

    it('renders custom presets', () => {
      const customPresets = [
        {
          label: 'Last Week',
          getValue: () => ({
            startDate: new Date('2024-01-08Z'),
            endDate: new Date('2024-01-14Z'),
          }),
        },
      ];

      renderDateRangePicker({
        presets: customPresets,
      });

      expect(screen.getByText('Last Week')).toBeInTheDocument();
      expect(screen.queryByText('Today')).not.toBeInTheDocument();
    });

    it('hides presets when showPresets is false', () => {
      renderDateRangePicker({
        showPresets: false,
      });

      expect(screen.queryByText('Quick Select')).not.toBeInTheDocument();
      expect(screen.queryByText('Today')).not.toBeInTheDocument();
    });
  });

  it('shows clear button when dates are selected', async () => {
    renderDateRangePicker({
      startDate: new Date('2024-01-01Z'),
      endDate: new Date('2024-01-31Z'),
    });

    expect(screen.getByText('Clear')).toBeInTheDocument();
  });

  it('clears dates when clear button is clicked', async () => {
    const onChange = vi.fn();

    renderDateRangePicker({
      startDate: new Date('2024-01-01Z'),
      endDate: new Date('2024-01-31Z'),
      onChange,
    });

    await fireEvent.click(screen.getByText('Clear'));

    expect(onChange).toHaveBeenCalledWith({
      startDate: null,
      endDate: null,
    });
  });

  it('displays selected date range summary', () => {
    renderDateRangePicker({
      startDate: new Date('2024-01-01Z'),
      endDate: new Date('2024-01-31Z'),
    });

    expect(screen.getByText(/Selected:/)).toBeInTheDocument();
  });

  it('disables inputs when disabled prop is true', () => {
    renderDateRangePicker({ disabled: true });

    expect(screen.getByLabelText('Start Date')).toBeDisabled();
    expect(screen.getByLabelText('End Date')).toBeDisabled();
    expect(screen.getByText('Today')).toBeDisabled();
  });

  it('marks fields as required when required prop is true', () => {
    renderDateRangePicker({ required: true });

    const startInput = screen.getByLabelText('Start Date *');
    const endInput = screen.getByLabelText('End Date *');

    expect(startInput).toHaveAttribute('required');
    expect(endInput).toHaveAttribute('required');
  });

  it('calls individual date change handlers', async () => {
    const onStartChange = vi.fn();
    const onEndChange = vi.fn();
    const user = userEvent.setup({ delay: null });

    renderDateRangePicker({ onStartChange, onEndChange });

    const startInput = screen.getByLabelText('Start Date');
    const endInput = screen.getByLabelText('End Date');

    await user.type(startInput, '2024-01-10');
    expect(onStartChange).toHaveBeenCalledWith(new Date('2024-01-10Z'));

    await user.type(endInput, '2024-01-20');
    expect(onEndChange).toHaveBeenCalledWith(new Date('2024-01-20Z'));
  });

  it('applies custom className', () => {
    renderDateRangePicker({ className: 'custom-date-picker' });

    expect(document.querySelector('.date-range-picker.custom-date-picker')).toBeInTheDocument();
  });
});
