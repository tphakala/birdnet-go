import { describe, it, expect, vi, beforeEach } from 'vitest';
import { createComponentTestFactory, screen, fireEvent } from '../../../test/render-helpers';
import NavFlatItem from './NavFlatItem.svelte';
import { Search } from '@lucide/svelte';

vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string) => key),
  getLocale: vi.fn(() => 'en'),
}));

describe('NavFlatItem', () => {
  const itemTest = createComponentTestFactory(NavFlatItem);

  const defaultProps = {
    icon: Search,
    label: 'Search',
    url: '/ui/search',
    active: false,
    isCollapsed: false,
    onNavigate: vi.fn(),
    showTooltip: vi.fn(),
    hideTooltip: vi.fn(),
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders the label text when expanded', () => {
    itemTest.render({ ...defaultProps, isCollapsed: false });
    expect(screen.getByText('Search')).toBeInTheDocument();
  });

  it('renders an icon element', () => {
    itemTest.render({ ...defaultProps });
    const button = screen.getByRole('button');
    expect(button).toBeInTheDocument();
    // Icon renders as SVG inside the button
    expect(button.querySelector('svg')).toBeInTheDocument();
  });

  it('aria-label is always present (defaults to label)', () => {
    itemTest.render({ ...defaultProps, isCollapsed: false });
    const button = screen.getByRole('button');
    expect(button).toHaveAttribute('aria-label', 'Search');
  });

  it('aria-label is present in collapsed icon-only mode', () => {
    itemTest.render({ ...defaultProps, isCollapsed: true });
    const button = screen.getByRole('button');
    expect(button).toHaveAttribute('aria-label', 'Search');
  });

  it('uses custom ariaLabel when provided', () => {
    itemTest.render({ ...defaultProps, ariaLabel: 'Search the database' });
    const button = screen.getByRole('button');
    expect(button).toHaveAttribute('aria-label', 'Search the database');
  });

  it('sets aria-current="page" when active', () => {
    itemTest.render({ ...defaultProps, active: true });
    const button = screen.getByRole('button');
    expect(button).toHaveAttribute('aria-current', 'page');
  });

  it('does not set aria-current when not active', () => {
    itemTest.render({ ...defaultProps, active: false });
    const button = screen.getByRole('button');
    expect(button).not.toHaveAttribute('aria-current');
  });

  it('calls onNavigate with url when clicked', async () => {
    const onNavigate = vi.fn();
    itemTest.render({ ...defaultProps, onNavigate, url: '/ui/search' });
    await fireEvent.click(screen.getByRole('button'));
    expect(onNavigate).toHaveBeenCalledWith('/ui/search');
  });

  it('shows comingSoon badge when expanded and comingSoon=true', () => {
    itemTest.render({ ...defaultProps, isCollapsed: false, comingSoon: true, label: 'Weather' });
    // The badge text is the i18n key (mocked to return the key)
    expect(screen.getByText('analytics.comingSoon.badge')).toBeInTheDocument();
    expect(screen.getByText('analytics.comingSoon.badge')).toHaveClass('badge');
  });

  it('does not show comingSoon badge when comingSoon=false', () => {
    itemTest.render({ ...defaultProps, isCollapsed: false, comingSoon: false });
    expect(screen.queryByText('analytics.comingSoon.badge')).not.toBeInTheDocument();
  });

  it('hides text label and badge in collapsed mode', () => {
    itemTest.render({
      ...defaultProps,
      isCollapsed: true,
      comingSoon: true,
      label: 'Weather',
    });
    // Text span should not be visible (not rendered)
    expect(screen.queryByText('Weather')).not.toBeInTheDocument();
    // Badge should also be absent in collapsed mode
    expect(screen.queryByText('analytics.comingSoon.badge')).not.toBeInTheDocument();
  });

  it('keeps accessible name when collapsed (icon-only)', () => {
    itemTest.render({ ...defaultProps, isCollapsed: true, label: 'Search' });
    const button = screen.getByRole('button');
    expect(button).toHaveAttribute('aria-label', 'Search');
    // No visible text label rendered
    expect(screen.queryByText('Search')).not.toBeInTheDocument();
  });

  it('suffixes aria-label with coming-soon text when collapsed and comingSoon=true', () => {
    itemTest.render({ ...defaultProps, isCollapsed: true, comingSoon: true, label: 'Weather' });
    const button = screen.getByRole('button');
    // aria-label should include the badge text suffix
    expect(button.getAttribute('aria-label')).toContain('Weather');
    expect(button.getAttribute('aria-label')).toContain('analytics.comingSoon.badge');
  });

  it('suffixes aria-label with coming-soon text when expanded and comingSoon=true', () => {
    itemTest.render({ ...defaultProps, isCollapsed: false, comingSoon: true, label: 'Weather' });
    const button = screen.getByRole('button');
    // Screen readers must hear the coming-soon state in expanded mode too
    expect(button.getAttribute('aria-label')).toContain('Weather');
    expect(button.getAttribute('aria-label')).toContain('analytics.comingSoon.badge');
  });

  it('does NOT have role="menuitem"', () => {
    itemTest.render({ ...defaultProps });
    const button = screen.getByRole('button');
    expect(button).not.toHaveAttribute('role', 'menuitem');
  });

  it('has focus-visible ring classes on the button', () => {
    itemTest.render({ ...defaultProps });
    const button = screen.getByRole('button');
    expect(button.className).toContain('focus-visible:ring-2');
  });
});
