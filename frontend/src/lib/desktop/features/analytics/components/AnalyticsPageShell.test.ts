import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { chartsForGroup } from '../registry/charts';
import type { ChartGroup } from '../registry/types';
import AnalyticsPageShell from './AnalyticsPageShell.svelte';

vi.mock('$lib/i18n', () => ({ t: (k: string) => k }));

// Use vi.hoisted so these mocks are reachable in both the vi.mock factory and
// the test body. Vitest hoists vi.mock() calls above imports, so any variables
// referenced in the factory must themselves be hoisted.
const {
  mockSyncFromUrl,
  mockCleanup,
  mockInit,
  mockEnsureSpecies,
  mockEnableAutoSelect,
  mockRelease,
} = vi.hoisted(() => {
  const mockSyncFromUrl = vi.fn();
  const mockCleanup = vi.fn();
  const mockInit = vi.fn(() => mockCleanup);
  const mockEnsureSpecies = vi.fn();
  const mockRelease = vi.fn();
  const mockEnableAutoSelect = vi.fn(() => mockRelease);
  return {
    mockSyncFromUrl,
    mockCleanup,
    mockInit,
    mockEnsureSpecies,
    mockEnableAutoSelect,
    mockRelease,
  };
});

vi.mock('../registry/analyticsControls.svelte', () => ({
  analyticsControls: {
    params: {
      range: 'month',
      start: '',
      end: '',
      species: [],
      source: '',
      startDate: new Date(),
      endDate: new Date(),
    },
    availableSpecies: [],
    loadingSpecies: false,
    availableSources: [],
    loadingSources: false,
    applyParams: vi.fn(),
    ensureSpecies: mockEnsureSpecies,
    enableAutoSelect: mockEnableAutoSelect,
    ensureSources: vi.fn(),
    syncFromUrl: mockSyncFromUrl,
    init: mockInit,
  },
}));

function makeBody() {
  return createRawSnippet(() => ({ render: () => `<p data-testid="body">hi</p>` }));
}

const defaultProps = {
  titleKey: 'analytics.hub.tabs.trends' as const,
  group: 'trends' as const,
};

describe('AnalyticsPageShell', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // Restore mockInit to return mockCleanup after clearAllMocks resets it.
    mockInit.mockReturnValue(mockCleanup);
    mockEnableAutoSelect.mockReturnValue(mockRelease);
    cleanup();
  });

  it('renders the section with aria-label set to the title key and renders the slotted body', () => {
    render(AnalyticsPageShell, {
      props: { ...defaultProps, children: makeBody() },
    });
    expect(screen.getByRole('region', { name: 'analytics.hub.tabs.trends' })).toBeInTheDocument();
    expect(screen.getByTestId('body')).toBeInTheDocument();
  });

  describe('mount effect - deep-link fix', () => {
    it('calls syncFromUrl on mount', () => {
      render(AnalyticsPageShell, {
        props: { ...defaultProps, children: makeBody() },
      });
      expect(mockSyncFromUrl).toHaveBeenCalledOnce();
    });

    it('calls init on mount', () => {
      render(AnalyticsPageShell, {
        props: { ...defaultProps, children: makeBody() },
      });
      expect(mockInit).toHaveBeenCalledOnce();
    });

    it('calls syncFromUrl before init so URL filters are applied before the listener registers', () => {
      render(AnalyticsPageShell, {
        props: { ...defaultProps, children: makeBody() },
      });
      // invocationCallOrder values are globally incrementing; a lower value means
      // the function was called earlier in the test run.
      const syncOrder = mockSyncFromUrl.mock.invocationCallOrder[0];
      const initOrder = mockInit.mock.invocationCallOrder[0];
      expect(syncOrder).toBeGreaterThan(0);
      expect(initOrder).toBeGreaterThan(0);
      expect(syncOrder).toBeLessThan(initOrder);
    });

    it("calls init's returned cleanup on unmount", () => {
      const { unmount } = render(AnalyticsPageShell, {
        props: { ...defaultProps, children: makeBody() },
      });
      expect(mockCleanup).not.toHaveBeenCalled();
      unmount();
      expect(mockCleanup).toHaveBeenCalledOnce();
    });
  });

  describe('species list and auto-select wiring', () => {
    // Every ChartGroup value; the registry decides which ones support a species filter.
    const groups: ChartGroup[] = [
      'overview',
      'patterns',
      'trends',
      'biodiversity',
      'quality',
      'nocturnal',
    ];
    const speciesGroups = groups.filter(g => chartsForGroup(g).some(c => c.supports.species));

    function renderGroup(group: ChartGroup): () => void {
      vi.clearAllMocks();
      mockInit.mockReturnValue(mockCleanup);
      mockEnableAutoSelect.mockReturnValue(mockRelease);
      const { unmount } = render(AnalyticsPageShell, {
        props: { ...defaultProps, group, children: makeBody() },
      });
      return unmount;
    }

    it('loads the species list only on species-filter tabs', () => {
      expect(speciesGroups.length).toBeGreaterThan(0);
      const loaded: ChartGroup[] = [];
      for (const g of groups) {
        const unmount = renderGroup(g);
        if (mockEnsureSpecies.mock.calls.length > 0) loaded.push(g);
        unmount();
      }
      expect(loaded).toEqual(speciesGroups);
    });

    it('requests auto-select only on species-filter tabs', () => {
      for (const g of ['trends', 'patterns', 'nocturnal'] as const) {
        const unmount = renderGroup(g);
        expect(mockEnableAutoSelect).toHaveBeenCalledOnce();
        unmount();
      }
      for (const g of ['biodiversity', 'quality', 'overview'] as const) {
        const unmount = renderGroup(g);
        expect(mockEnableAutoSelect).not.toHaveBeenCalled();
        unmount();
      }
    });

    it('releases the auto-select holder on unmount', () => {
      const unmount = renderGroup('trends');
      expect(mockRelease).not.toHaveBeenCalled();
      unmount();
      expect(mockRelease).toHaveBeenCalledOnce();
    });
  });
});
