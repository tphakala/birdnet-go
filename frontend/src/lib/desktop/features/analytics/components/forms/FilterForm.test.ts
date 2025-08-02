import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import FilterForm from './FilterForm.svelte';

// Mock i18n translations
vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string, params?: Record<string, unknown>) => {
    const translations: Record<string, string> = {
      'analytics.filters.title': 'Filter Data',
      'analytics.filters.timePeriod': 'Time Period',
      'analytics.filters.from': 'From',
      'analytics.filters.to': 'To',
      'analytics.filters.reset': 'Reset',
      'analytics.filters.applyFilters': 'Apply Filters',
      'analytics.timePeriodOptions.allTime': 'All Time',
      'analytics.timePeriodOptions.today': 'Today',
      'analytics.timePeriodOptions.lastWeek': 'Last Week',
      'analytics.timePeriodOptions.lastMonth': 'Last Month',
      'analytics.timePeriodOptions.last90Days': 'Last 90 Days',
      'analytics.timePeriodOptions.lastYear': 'Last Year',
      'analytics.timePeriodOptions.customRange': 'Custom Range',
    };

    let translation = translations[key] ?? key;

    // Handle template variables like {{variable}}
    if (params && typeof translation === 'string') {
      Object.entries(params).forEach(([param, value]) => {
        translation = translation.replace(`{{${param}}}`, String(value));
      });
    }

    return translation;
  }),
}));

const defaultFilters = {
  timePeriod: 'all' as const,
  startDate: '',
  endDate: '',
};

describe('FilterForm', () => {
  it('renders with basic props', () => {
    render(FilterForm, {
      props: {
        filters: defaultFilters,
        onSubmit: vi.fn(),
        onReset: vi.fn(),
      },
    });

    expect(screen.getByText('Filter Data')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Apply Filters' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Reset' })).toBeInTheDocument();
  });

  it('displays time period options', () => {
    render(FilterForm, {
      props: {
        filters: defaultFilters,
        onSubmit: vi.fn(),
        onReset: vi.fn(),
      },
    });

    const timePeriodSelect = screen.getByRole('combobox');
    expect(timePeriodSelect).toBeInTheDocument();
  });

  it('shows custom date fields when custom time period is selected', () => {
    const customFilters = {
      ...defaultFilters,
      timePeriod: 'custom' as const,
    };

    render(FilterForm, {
      props: {
        filters: customFilters,
        onSubmit: vi.fn(),
        onReset: vi.fn(),
      },
    });

    expect(screen.getByLabelText('From')).toBeInTheDocument();
    expect(screen.getByLabelText('To')).toBeInTheDocument();
  });

  it('hides custom date fields when other time period is selected', () => {
    render(FilterForm, {
      props: {
        filters: defaultFilters,
        onSubmit: vi.fn(),
        onReset: vi.fn(),
      },
    });

    expect(screen.queryByLabelText('From')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('To')).not.toBeInTheDocument();
  });

  it('calls onSubmit when form is submitted', async () => {
    const onSubmit = vi.fn();

    render(FilterForm, {
      props: {
        filters: defaultFilters,
        onSubmit,
        onReset: vi.fn(),
      },
    });

    const form = screen.getByRole('button', { name: 'Apply Filters' }).closest('form');
    expect(form).toBeTruthy();
    await fireEvent.submit(form as HTMLFormElement);

    expect(onSubmit).toHaveBeenCalled();
  });

  it('calls onReset when reset button is clicked', async () => {
    const onReset = vi.fn();

    render(FilterForm, {
      props: {
        filters: defaultFilters,
        onSubmit: vi.fn(),
        onReset,
      },
    });

    const resetButton = screen.getByRole('button', { name: 'Reset' });
    await fireEvent.click(resetButton);

    expect(onReset).toHaveBeenCalled();
  });

  it('disables buttons when loading', () => {
    render(FilterForm, {
      props: {
        filters: defaultFilters,
        isLoading: true,
        onSubmit: vi.fn(),
        onReset: vi.fn(),
      },
    });

    expect(screen.getByRole('button', { name: 'Apply Filters' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Reset' })).toBeDisabled();
  });

  it('shows loading spinner when loading', () => {
    render(FilterForm, {
      props: {
        filters: defaultFilters,
        isLoading: true,
        onSubmit: vi.fn(),
        onReset: vi.fn(),
      },
    });

    expect(screen.getByText('Apply Filters')).toBeInTheDocument();
    const loadingSpinner = document.querySelector('.loading-spinner');
    expect(loadingSpinner).toBeInTheDocument();
  });

  it('prevents default form submission', async () => {
    const onSubmit = vi.fn();

    render(FilterForm, {
      props: {
        filters: defaultFilters,
        onSubmit,
        onReset: vi.fn(),
      },
    });

    const form = screen.getByRole('button', { name: 'Apply Filters' }).closest('form');
    const submitEvent = new Event('submit', { bubbles: true, cancelable: true });

    expect(form).toBeTruthy();
    await fireEvent(form as HTMLFormElement, submitEvent);

    expect(submitEvent.defaultPrevented).toBe(true);
    expect(onSubmit).toHaveBeenCalled();
  });

  it('handles time period change correctly', async () => {
    const filters = { ...defaultFilters };

    render(FilterForm, {
      props: {
        filters,
        onSubmit: vi.fn(),
        onReset: vi.fn(),
      },
    });

    const select = screen.getByRole('combobox');
    await fireEvent.change(select, { target: { value: 'week' } });

    // The component should update its bound value
    expect(select).toHaveValue('week');
  });

  it('renders with proper form structure', () => {
    render(FilterForm, {
      props: {
        filters: defaultFilters,
        onSubmit: vi.fn(),
        onReset: vi.fn(),
      },
    });

    const card = screen.getByText('Filter Data').closest('.card');
    expect(card).toHaveClass('bg-base-100', 'shadow-sm');

    const form = screen.getByRole('button', { name: 'Apply Filters' }).closest('form');
    expect(form).toHaveClass('space-y-4');
  });
});
