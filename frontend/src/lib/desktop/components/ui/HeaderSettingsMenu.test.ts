import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, fireEvent, cleanup, within } from '@testing-library/svelte';
import { t } from '$lib/i18n';
import HeaderSettingsMenu from './HeaderSettingsMenu.svelte';

/**
 * Guards the fix for issue #4112: dashboard customization (Edit + Reset) is an
 * admin-only affordance in the settings menu. This asserts the menu-level
 * gating; guests still get the neutral menu items (theme toggle, help links).
 * The server-side 401 on the settings PATCH and DashboardPage's isEditing guard
 * are the actual boundaries that stop a guest from customizing.
 */

// Text rendered for the menu items. The global i18n mock in src/test/setup.ts
// returns the key itself when it is absent from its translation table, so t()
// here yields the exact rendered text (expected labels and rendered spans both
// flow through the same mock).
const EDIT_LABEL = t('dashboard.editMode.editDashboard');
const RESET_LABEL = t('dashboard.editMode.resetDashboard');
const THEME_LABEL = t('navigation.theme');
const REPORT_BUG_LABEL = t('navigation.reportBug');
const MENU_LABEL = t('navigation.settingsMenu');

// Opens the dropdown and returns a query scope limited to the menu itself, so
// the (always-mounted) reset ConfirmModal, which reuses the reset label, cannot
// leak into these assertions.
async function renderAndOpen(props: { securityEnabled?: boolean; accessAllowed?: boolean }) {
  const { container } = render(HeaderSettingsMenu, { props });
  const toggle = screen.getByRole('button', { name: MENU_LABEL });
  await fireEvent.click(toggle);
  const menu = container.querySelector('#header-settings-menu');
  if (!menu) throw new Error('settings menu did not open');
  return within(menu as HTMLElement);
}

describe('HeaderSettingsMenu dashboard customization gating (#4112)', () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('hides Edit and Reset Dashboard from unauthenticated guests', async () => {
    const menu = await renderAndOpen({ securityEnabled: true, accessAllowed: false });

    expect(menu.queryByText(EDIT_LABEL)).toBeNull();
    expect(menu.queryByText(RESET_LABEL)).toBeNull();

    // Neutral, guest-safe items remain available.
    expect(menu.getByText(THEME_LABEL)).toBeInTheDocument();
    expect(menu.getByText(REPORT_BUG_LABEL)).toBeInTheDocument();
  });

  it('shows Edit and Reset Dashboard to authenticated users', async () => {
    const menu = await renderAndOpen({ securityEnabled: true, accessAllowed: true });

    expect(menu.getByText(EDIT_LABEL)).toBeInTheDocument();
    expect(menu.getByText(RESET_LABEL)).toBeInTheDocument();
  });

  it('shows Edit and Reset Dashboard when security is disabled', async () => {
    const menu = await renderAndOpen({ securityEnabled: false, accessAllowed: false });

    expect(menu.getByText(EDIT_LABEL)).toBeInTheDocument();
    expect(menu.getByText(RESET_LABEL)).toBeInTheDocument();
  });
});
