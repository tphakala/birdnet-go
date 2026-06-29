import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/svelte';
import { CloudSun } from '@lucide/svelte';
import AnalyticsComingSoon from './AnalyticsComingSoon.svelte';

vi.mock('$lib/i18n', () => ({ t: (k: string) => k }));

// Use vi.hoisted so these mocks are reachable in both the vi.mock factory and
// the test body (vi.mock() factories are hoisted above imports by Vitest).
const { mockSyncFromUrl, mockCleanup, mockInit } = vi.hoisted(() => {
  const mockSyncFromUrl = vi.fn();
  const mockCleanup = vi.fn();
  const mockInit = vi.fn(() => mockCleanup);
  return { mockSyncFromUrl, mockCleanup, mockInit };
});

vi.mock('../registry/analyticsControls.svelte', () => ({
  analyticsControls: {
    syncFromUrl: mockSyncFromUrl,
    init: mockInit,
  },
}));

const defaultProps = {
  titleKey: 'analytics.hub.tabs.weather' as const,
  icon: CloudSun,
  descriptionKey: 'analytics.comingSoon.weather.description' as const,
  featureKeys: [
    'analytics.comingSoon.weather.feature1' as const,
    'analytics.comingSoon.weather.feature2' as const,
  ],
};

describe('AnalyticsComingSoon', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // Restore mockInit to return mockCleanup after clearAllMocks resets it.
    mockInit.mockReturnValue(mockCleanup);
    cleanup();
  });

  it('renders the title key and feature keys', () => {
    render(AnalyticsComingSoon, { props: defaultProps });
    expect(screen.getByText('analytics.hub.tabs.weather')).toBeInTheDocument();
    expect(screen.getByText('analytics.comingSoon.weather.feature1')).toBeInTheDocument();
  });

  describe('mount effect - deep-link fix (#1275)', () => {
    it('calls syncFromUrl on mount', () => {
      render(AnalyticsComingSoon, { props: defaultProps });
      expect(mockSyncFromUrl).toHaveBeenCalledOnce();
    });

    it('calls init on mount', () => {
      render(AnalyticsComingSoon, { props: defaultProps });
      expect(mockInit).toHaveBeenCalledOnce();
    });

    it('calls syncFromUrl before init so URL filters are applied before the listener registers', () => {
      render(AnalyticsComingSoon, { props: defaultProps });
      const syncOrder = mockSyncFromUrl.mock.invocationCallOrder[0];
      const initOrder = mockInit.mock.invocationCallOrder[0];
      expect(syncOrder).toBeGreaterThan(0);
      expect(initOrder).toBeGreaterThan(0);
      expect(syncOrder).toBeLessThan(initOrder);
    });

    it("calls init's returned cleanup on unmount", () => {
      const { unmount } = render(AnalyticsComingSoon, { props: defaultProps });
      expect(mockCleanup).not.toHaveBeenCalled();
      unmount();
      expect(mockCleanup).toHaveBeenCalledOnce();
    });
  });
});
