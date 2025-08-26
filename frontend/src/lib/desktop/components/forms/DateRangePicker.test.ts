import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  createComponentTestFactory,
  screen,
  fireEvent,
  waitFor,
} from '../../../../test/render-helpers';
import userEvent from '@testing-library/user-event';
import { parseLocalDateString } from '$lib/utils/date';
import DateRangePicker from './DateRangePicker.svelte';

// Mock i18n translations
vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string, params?: Record<string, unknown>) => {
    const translations: Record<string, string> = {
      // Form labels
      'forms.dateRange.labels.startDate': 'Start Date',
      'forms.dateRange.labels.endDate': 'End Date',
      'forms.dateRange.labels.quickSelect': 'Quick Select',
      'forms.dateRange.labels.selected': 'Selected',
      // Presets
      'forms.dateRange.presets.today': 'Today',
      'forms.dateRange.presets.yesterday': 'Yesterday',
      'forms.dateRange.presets.last7Days': 'Last 7 days',
      'forms.dateRange.presets.last30Days': 'Last 30 days',
      'forms.dateRange.presets.thisMonth': 'This month',
      'forms.dateRange.presets.lastMonth': 'Last month',
      'forms.dateRange.presets.thisYear': 'This year',
      'forms.dateRange.presets.clear': 'Clear',
      // Common buttons
      'common.buttons.clear': 'Clear',
      // Messages and validation
      'forms.dateRange.messages.endBeforeStart': 'End date cannot be before start date',
      'forms.dateRange.messages.selectedRange': 'Selected date range: {{start}} to {{end}}',
      'forms.dateRange.messages.selectedPrefix': 'Selected:',
      'forms.dateRange.validation.endBeforeStart': 'End date cannot be before start date',
      'forms.dateRange.errors.endBeforeStart': 'End date cannot be before start date',
      'forms.dateRange.errors.startAfterEnd': 'Start date cannot be after end date',
      'forms.dateRange.errors.invalidStartDate': 'Invalid start date',
      'forms.dateRange.errors.invalidEndDate': 'Invalid end date',
      'validation.dateRange.endBeforeStart': 'End date cannot be before start date',
      // Custom test translations
      'Last Week': 'Last Week',
    };
    // eslint-disable-next-line security/detect-object-injection
    let translation = translations[key] || key;

    // Handle template variables
    if (params && typeof translation === 'string') {
      Object.entries(params).forEach(([param, value]) => {
        translation = translation.replace(`{{${param}}}`, String(value));
      });
    }

    return translation;
  }),
}));

// Create typed test factory for DateRangePicker
const dateRangeTest = createComponentTestFactory(DateRangePicker);

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
    dateRangeTest.render({});

    expect(screen.getByLabelText('Start Date')).toBeInTheDocument();
    expect(screen.getByLabelText('End Date')).toBeInTheDocument();
  });

  it('renders with custom labels', () => {
    dateRangeTest.render({
      startLabel: 'From',
      endLabel: 'To',
    });

    expect(screen.getByLabelText('From')).toBeInTheDocument();
    expect(screen.getByLabelText('To')).toBeInTheDocument();
  });

  it('displays initial dates', () => {
    dateRangeTest.render({
      startDate: parseLocalDateString('2024-01-01'),
      endDate: parseLocalDateString('2024-01-31'),
    });

    const startInput = screen.getByLabelText('Start Date') as HTMLInputElement;
    const endInput = screen.getByLabelText('End Date') as HTMLInputElement;

    expect(startInput.value).toBe('2024-01-01');
    expect(endInput.value).toBe('2024-01-31');
  });

  it('handles date changes', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup({ delay: null });

    dateRangeTest.render({ onChange });

    const startInput = screen.getByLabelText('Start Date');
    const endInput = screen.getByLabelText('End Date');

    await user.type(startInput, '2024-01-10');

    expect(onChange).toHaveBeenCalledWith({
      startDate: parseLocalDateString('2024-01-10'),
      endDate: null,
    });

    await user.type(endInput, '2024-01-20');

    expect(onChange).toHaveBeenCalledWith({
      startDate: parseLocalDateString('2024-01-10'),
      endDate: parseLocalDateString('2024-01-20'),
    });
  });

  it('validates date range', async () => {
    const user = userEvent.setup({ delay: null });

    dateRangeTest.render({
      startDate: parseLocalDateString('2024-01-10'),
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
    dateRangeTest.render({
      minDate: parseLocalDateString('2024-01-01'),
      maxDate: parseLocalDateString('2024-12-31'),
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

    dateRangeTest.render({});

    const startInput = screen.getByLabelText('Start Date');
    const endInput = screen.getByLabelText('End Date') as HTMLInputElement;

    await user.type(startInput, '2024-01-15');

    expect(endInput.min).toBe('2024-01-15');
  });

  describe('Presets', () => {
    it('renders default presets', () => {
      dateRangeTest.render({});

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

      dateRangeTest.render({
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
      dateRangeTest.render({});

      await fireEvent.click(screen.getByText('Today'));

      const todayButton = screen.getByText('Today');
      expect(todayButton).toHaveClass('btn-primary');
    });

    it('renders custom presets', () => {
      const customPresets = [
        {
          key: 'Last Week',
          getValue: () => ({
            startDate: parseLocalDateString('2024-01-08'),
            endDate: parseLocalDateString('2024-01-14'),
          }),
        },
      ];

      dateRangeTest.render({
        presets: customPresets,
      });

      expect(screen.getByText('Last Week')).toBeInTheDocument();
      expect(screen.queryByText('Today')).not.toBeInTheDocument();
    });

    it('hides presets when showPresets is false', () => {
      dateRangeTest.render({
        showPresets: false,
      });

      expect(screen.queryByText('Quick Select')).not.toBeInTheDocument();
      expect(screen.queryByText('Today')).not.toBeInTheDocument();
    });
  });

  it('shows clear button when dates are selected', async () => {
    dateRangeTest.render({
      startDate: parseLocalDateString('2024-01-01'),
      endDate: parseLocalDateString('2024-01-31'),
    });

    expect(screen.getByText('Clear')).toBeInTheDocument();
  });

  it('clears dates when clear button is clicked', async () => {
    const onChange = vi.fn();

    dateRangeTest.render({
      startDate: parseLocalDateString('2024-01-01'),
      endDate: parseLocalDateString('2024-01-31'),
      onChange,
    });

    await fireEvent.click(screen.getByText('Clear'));

    expect(onChange).toHaveBeenCalledWith({
      startDate: null,
      endDate: null,
    });
  });

  it('displays selected date range summary', () => {
    dateRangeTest.render({
      startDate: parseLocalDateString('2024-01-01'),
      endDate: parseLocalDateString('2024-01-31'),
    });

    expect(screen.getByText('Selected')).toBeInTheDocument();
  });

  it('disables inputs when disabled prop is true', () => {
    dateRangeTest.render({ disabled: true });

    expect(screen.getByLabelText('Start Date')).toBeDisabled();
    expect(screen.getByLabelText('End Date')).toBeDisabled();
    expect(screen.getByText('Today')).toBeDisabled();
  });

  it('marks fields as required when required prop is true', () => {
    dateRangeTest.render({ required: true });

    const startInput = screen.getByLabelText('Start Date *');
    const endInput = screen.getByLabelText('End Date *');

    expect(startInput).toHaveAttribute('required');
    expect(endInput).toHaveAttribute('required');
  });

  it('calls individual date change handlers', async () => {
    const onStartChange = vi.fn();
    const onEndChange = vi.fn();
    const user = userEvent.setup({ delay: null });

    dateRangeTest.render({ onStartChange, onEndChange });

    const startInput = screen.getByLabelText('Start Date');
    const endInput = screen.getByLabelText('End Date');

    await user.type(startInput, '2024-01-10');
    expect(onStartChange).toHaveBeenCalledWith(parseLocalDateString('2024-01-10'));

    await user.type(endInput, '2024-01-20');
    expect(onEndChange).toHaveBeenCalledWith(parseLocalDateString('2024-01-20'));
  });

  it('applies custom className', () => {
    dateRangeTest.render({ className: 'custom-date-picker' });

    expect(document.querySelector('.date-range-picker.custom-date-picker')).toBeInTheDocument();
  });
});
