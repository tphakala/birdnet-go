import { describe, it, expect, vi, beforeEach } from 'vitest';
import { createComponentTestFactory, screen, fireEvent } from '../../../test/render-helpers';
import NavFlatItem from './NavFlatItem.svelte';
import { Search } from '@lucide/svelte';

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

  it('exposes the label as the accessible name without a redundant aria-label when expanded', () => {
    itemTest.render({ ...defaultProps, isCollapsed: false });
    // Visible <span>{label}</span> provides the accessible name; aria-label is
    // intentionally omitted when expanded to avoid a duplicate announcement.
    const button = screen.getByRole('button', { name: 'Search' });
    expect(button).not.toHaveAttribute('aria-label');
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

  it('keeps accessible name when collapsed (icon-only)', () => {
    itemTest.render({ ...defaultProps, isCollapsed: true, label: 'Search' });
    const button = screen.getByRole('button');
    expect(button).toHaveAttribute('aria-label', 'Search');
    // No visible text label rendered
    expect(screen.queryByText('Search')).not.toBeInTheDocument();
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

  // -------------------------------------------------------------------------
  // Keyboard focus tooltip (a11y)
  // -------------------------------------------------------------------------

  it('calls showTooltip on focus when collapsed', async () => {
    const showTooltip = vi.fn();
    const hideTooltip = vi.fn();
    itemTest.render({
      ...defaultProps,
      isCollapsed: true,
      showTooltip,
      hideTooltip,
      label: 'Search',
    });
    const button = screen.getByRole('button');
    await fireEvent.focus(button);
    expect(showTooltip).toHaveBeenCalledTimes(1);
    expect(showTooltip.mock.calls[0][1]).toBe('Search');
  });

  it('does NOT call showTooltip on focus when expanded', async () => {
    const showTooltip = vi.fn();
    itemTest.render({
      ...defaultProps,
      isCollapsed: false,
      showTooltip,
      label: 'Search',
    });
    const button = screen.getByRole('button');
    await fireEvent.focus(button);
    expect(showTooltip).not.toHaveBeenCalled();
  });

  it('calls hideTooltip on blur in collapsed mode', async () => {
    const hideTooltip = vi.fn();
    itemTest.render({
      ...defaultProps,
      isCollapsed: true,
      hideTooltip,
    });
    const button = screen.getByRole('button');
    await fireEvent.blur(button);
    expect(hideTooltip).toHaveBeenCalledTimes(1);
  });
});
