import { describe, it, expect, vi, beforeEach } from 'vitest';
import { createComponentTestFactory, fireEvent, screen } from '../../../test/render-helpers';
import CollapsibleNavSection from './CollapsibleNavSection.svelte';
import { Settings } from '@lucide/svelte';

vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string) => key),
  getLocale: vi.fn(() => 'en'),
}));

describe('CollapsibleNavSection', () => {
  const sectionTest = createComponentTestFactory(CollapsibleNavSection);

  const SECTION_ID = 'system';
  const ROUTE_KEY = 'systemOverview';

  const baseProps = {
    icon: Settings,
    label: 'System',
    items: [
      {
        icon: Settings,
        label: 'Overview',
        url: '/ui/system',
        routeKey: ROUTE_KEY,
      },
    ],
    isCollapsed: false,
    expanded: false,
    routeActive: false,
    routeCache: { [ROUTE_KEY]: false } as Record<string, boolean>,
    onToggleExpanded: vi.fn(),
    onNavigate: vi.fn(),
    showTooltip: vi.fn(),
    hideTooltip: vi.fn(),
    activeFlyout: null as string | null,
    sectionId: SECTION_ID,
    onToggleFlyout: vi.fn(),
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  // -------------------------------------------------------------------------
  // aria-current: expanded mode
  // -------------------------------------------------------------------------

  it('active subitem has aria-current="page" in expanded mode', () => {
    sectionTest.render({
      ...baseProps,
      isCollapsed: false,
      expanded: true,
      routeCache: { [ROUTE_KEY]: true },
    });

    const button = screen.getByRole('button', { name: 'Overview' });
    expect(button).toHaveAttribute('aria-current', 'page');
  });

  it('inactive subitem has no aria-current in expanded mode', () => {
    sectionTest.render({
      ...baseProps,
      isCollapsed: false,
      expanded: true,
      routeCache: { [ROUTE_KEY]: false },
    });

    const button = screen.getByRole('button', { name: 'Overview' });
    expect(button).not.toHaveAttribute('aria-current');
  });

  // -------------------------------------------------------------------------
  // aria-current: flyout mode
  // -------------------------------------------------------------------------

  it('active subitem has aria-current="page" in flyout mode', () => {
    sectionTest.render({
      ...baseProps,
      isCollapsed: true,
      activeFlyout: SECTION_ID,
      routeCache: { [ROUTE_KEY]: true },
    });

    // The trigger button shows the section label; the subitem button shows the item label.
    const subitemButton = screen.getByRole('button', { name: 'Overview' });
    expect(subitemButton).toHaveAttribute('aria-current', 'page');
  });

  it('inactive subitem has no aria-current in flyout mode', () => {
    sectionTest.render({
      ...baseProps,
      isCollapsed: true,
      activeFlyout: SECTION_ID,
      routeCache: { [ROUTE_KEY]: false },
    });

    const subitemButton = screen.getByRole('button', { name: 'Overview' });
    expect(subitemButton).not.toHaveAttribute('aria-current');
  });

  // -------------------------------------------------------------------------
  // Escape-to-close (D7)
  // -------------------------------------------------------------------------

  it('pressing Escape while flyout is open calls onToggleFlyout(sectionId)', async () => {
    const onToggleFlyout = vi.fn();
    sectionTest.render({
      ...baseProps,
      isCollapsed: true,
      activeFlyout: SECTION_ID,
      onToggleFlyout,
    });

    await fireEvent.keyDown(window, { key: 'Escape' });

    expect(onToggleFlyout).toHaveBeenCalledWith(SECTION_ID);
    expect(onToggleFlyout).toHaveBeenCalledTimes(1);
  });

  it('pressing Escape while flyout is closed does NOT call onToggleFlyout', async () => {
    const onToggleFlyout = vi.fn();
    sectionTest.render({
      ...baseProps,
      isCollapsed: true,
      activeFlyout: null,
      onToggleFlyout,
    });

    await fireEvent.keyDown(window, { key: 'Escape' });

    expect(onToggleFlyout).not.toHaveBeenCalled();
  });

  it('pressing a non-Escape key while flyout is open does NOT call onToggleFlyout', async () => {
    const onToggleFlyout = vi.fn();
    sectionTest.render({
      ...baseProps,
      isCollapsed: true,
      activeFlyout: SECTION_ID,
      onToggleFlyout,
    });

    await fireEvent.keyDown(window, { key: 'Tab' });

    expect(onToggleFlyout).not.toHaveBeenCalled();
  });
});
