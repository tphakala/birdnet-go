import { describe, it, expect, vi } from 'vitest';
import { createComponentTestFactory, screen } from '../../../test/render-helpers';
import DesktopHeader from './DesktopHeader.svelte';

// The notification store connects on import; keep it inert so the test owns no timers.
vi.mock('$lib/stores/sseNotifications', () => ({
  sseNotifications: {
    registerNotificationCallback: vi.fn(() => () => {}),
    connect: vi.fn(),
    disconnect: vi.fn(),
    getIsConnected: vi.fn().mockReturnValue(false),
  },
}));

describe('DesktopHeader sidebar toggle', () => {
  const quietProps = { showSearch: false, showAudioLevel: false, showNotifications: false };
  const headerTest = createComponentTestFactory(DesktopHeader);

  it('reports a closed drawer through aria-expanded', () => {
    headerTest.render({ ...quietProps, showSidebarToggle: true, sidebarOpen: false });
    const toggle = screen.getByRole('button', { name: /sidebar/i });
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
  });

  it('reports an open drawer through aria-expanded', () => {
    headerTest.render({ ...quietProps, showSidebarToggle: true, sidebarOpen: true });
    const toggle = screen.getByRole('button', { name: /sidebar/i });
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
  });
});
