import { describe, it, expect, vi, afterEach } from 'vitest';
import { cleanup, fireEvent } from '@testing-library/svelte';
import { renderTyped, screen } from '../../../test/render-helpers';
import RootLayout from './RootLayout.svelte';

// The notification store connects on import; keep it inert so the test owns no timers.
vi.mock('$lib/stores/sseNotifications', () => ({
  sseNotifications: {
    registerNotificationCallback: vi.fn(() => () => {}),
    connect: vi.fn(),
    disconnect: vi.fn(),
    getIsConnected: vi.fn().mockReturnValue(false),
  },
}));

// The global toast store mock has no `toasts` export, which the real container reads.
vi.mock('$lib/desktop/components/ui/ToastContainer.svelte', () => ({ default: () => {} }));

describe('RootLayout drawer', () => {
  afterEach(() => {
    cleanup();
  });

  function renderLayout(onNavigate = vi.fn()) {
    const { container } = renderTyped(RootLayout, { props: { onNavigate } });
    const checkbox = container.querySelector<HTMLInputElement>('#my-drawer');
    if (!checkbox) throw new Error('drawer checkbox missing');
    const toggle = screen.getByRole('button', { name: 'navigation.toggleSidebar' });
    return { container, checkbox, toggle, onNavigate };
  }

  it('keeps the drawer checkbox out of the tab order and the accessibility tree', () => {
    const { checkbox } = renderLayout();
    expect(checkbox).toHaveAttribute('tabindex', '-1');
    expect(checkbox).toHaveAttribute('aria-hidden', 'true');
  });

  it('the header toggle opens and closes the drawer and reports it through aria-expanded', async () => {
    const { checkbox, toggle } = renderLayout();
    expect(checkbox.checked).toBe(false);
    expect(toggle).toHaveAttribute('aria-expanded', 'false');

    await fireEvent.click(toggle);
    expect(checkbox.checked).toBe(true);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');

    await fireEvent.click(toggle);
    expect(checkbox.checked).toBe(false);
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
  });

  it('closing the drawer from the sidebar updates the header toggle', async () => {
    const { container, checkbox, toggle, onNavigate } = renderLayout();

    await fireEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    const overlay = container.querySelector('label.drawer-overlay');
    if (!overlay) throw new Error('drawer overlay missing');
    await fireEvent.click(overlay);
    expect(checkbox.checked).toBe(false);
    expect(toggle).toHaveAttribute('aria-expanded', 'false');

    await fireEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    // Two buttons carry this name: the logo first, then the navigation item.
    const dashboardButtons = screen.getAllByRole('button', { name: 'navigation.dashboard' });
    await fireEvent.click(dashboardButtons[dashboardButtons.length - 1]);
    expect(checkbox.checked).toBe(false);
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(onNavigate).toHaveBeenCalledWith('/ui/dashboard');
  });
});
