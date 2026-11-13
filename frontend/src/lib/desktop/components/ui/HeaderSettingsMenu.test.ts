import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, fireEvent, cleanup, within, waitFor } from '@testing-library/svelte';
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

  describe('focus after the Reset Dashboard confirmation closes', () => {
    const CANCEL_LABEL = t('common.buttons.cancel');

    async function openConfirmFrom(press: (item: HTMLElement) => Promise<void>) {
      const menu = await renderAndOpen({ securityEnabled: false });
      await press(menu.getByText(RESET_LABEL).closest('button') as HTMLButtonElement);
      return screen.getByRole('button', { name: MENU_LABEL });
    }

    it.each([
      [
        'keyboard activation',
        async (item: HTMLElement) => {
          item.focus();
          await fireEvent.click(item);
        },
      ],
      [
        'a press that does not focus the item (Safari)',
        async (item: HTMLElement) => {
          await fireEvent.pointerDown(item);
          await fireEvent.click(item);
        },
      ],
    ])('returns to the settings menu button when cancelled after %s', async (_name, press) => {
      const trigger = await openConfirmFrom(press);

      await fireEvent.click(await screen.findByRole('button', { name: CANCEL_LABEL }));

      await waitFor(() => expect(trigger).toHaveFocus());
    });

    it('returns to the settings menu button when dismissed with Escape', async () => {
      const trigger = await openConfirmFrom(async item => {
        item.focus();
        await fireEvent.click(item);
      });
      await screen.findByRole('button', { name: CANCEL_LABEL });

      await fireEvent.keyDown(document, { key: 'Escape' });

      await waitFor(() => expect(trigger).toHaveFocus());
    });
  });
});
