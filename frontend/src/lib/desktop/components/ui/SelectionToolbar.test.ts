import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderTyped, screen } from '../../../../test/render-helpers';
import SelectionToolbar from './SelectionToolbar.svelte';

// Override i18n for selection keys used by this component
vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string, params?: Record<string, unknown>) => {
    const translations: Record<string, string> = {
      'detections.selection.nSelected': '{count} selected',
      'detections.selection.allSelected': 'All {count} selected',
      'detections.selection.selectAllMatching': 'Select all {count} matching detections',
      'detections.selection.clear': 'Clear selection',
      'detections.selection.toolbarLabel': 'Bulk actions',
    };
    // eslint-disable-next-line security/detect-object-injection
    let result = translations[key] ?? key;
    if (params) {
      for (const [paramKey, paramValue] of Object.entries(params)) {
        // eslint-disable-next-line security/detect-non-literal-regexp
        result = result.replace(new RegExp(`\\{${paramKey}\\}`, 'g'), String(paramValue));
      }
    }
    return result;
  }),
  getLocale: vi.fn(() => 'en'),
  setLocale: vi.fn(),
  isValidLocale: vi.fn(() => true),
}));

describe('SelectionToolbar', () => {
  const defaultProps = {
    selectedCount: 5,
    totalCount: 100,
    allSelected: false,
    allOnPageSelected: false,
    pageSize: 25,
    onSelectAll: vi.fn(),
    onClear: vi.fn(),
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders selected count', () => {
    renderTyped(SelectionToolbar, { props: defaultProps });
    expect(screen.getByText('5 selected')).toBeInTheDocument();
  });

  it('calls onClear when clear button is clicked', async () => {
    const onClear = vi.fn();
    renderTyped(SelectionToolbar, { props: { ...defaultProps, onClear } });
    const clearButton = screen.getByLabelText('Clear selection');
    await clearButton.click();
    expect(onClear).toHaveBeenCalledOnce();
  });

  it('shows select-all banner when all on page selected but not all matching', () => {
    renderTyped(SelectionToolbar, {
      props: { ...defaultProps, allOnPageSelected: true, allSelected: false },
    });
    expect(screen.getByText('Select all 100 matching detections')).toBeInTheDocument();
  });

  it('hides select-all banner when all matching are already selected', () => {
    renderTyped(SelectionToolbar, {
      props: { ...defaultProps, allOnPageSelected: true, allSelected: true, selectedCount: 100 },
    });
    expect(
      screen.queryByRole('button', { name: 'Select all 100 matching detections' })
    ).not.toBeInTheDocument();
  });

  it('has toolbar role for accessibility', () => {
    renderTyped(SelectionToolbar, { props: defaultProps });
    expect(screen.getByRole('toolbar')).toBeInTheDocument();
  });
});
