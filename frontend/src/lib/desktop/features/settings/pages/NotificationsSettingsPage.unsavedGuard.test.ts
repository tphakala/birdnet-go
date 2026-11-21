import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import NotificationsSettingsPage from './NotificationsSettingsPage.svelte';
import { api } from '$lib/utils/api';
import { navigation } from '$lib/stores/navigation.svelte';
import { unsavedChanges, type NavigationTarget } from '$lib/stores/unsavedChanges.svelte';

const NTFY_PROVIDER = {
  type: 'shoutrrr',
  name: 'Garden ntfy',
  enabled: true,
  // A stored http scheme, so the probe's https recommendation switches the protocol.
  urls: ['ntfy://ntfy.example.com/birds?scheme=http'],
  filter: { types: ['detection'] },
};

let providers: unknown[] = [];

vi.mock('$lib/utils/api', async importOriginal => ({
  ...(await importOriginal<typeof import('$lib/utils/api')>()),
  api: {
    get: vi.fn(() => Promise.resolve({ push: { enabled: true, providers } })),
    post: vi.fn().mockResolvedValue({ recommended: 'https', https: true, http: false }),
    put: vi.fn().mockResolvedValue({}),
    patch: vi.fn().mockResolvedValue({}),
    delete: vi.fn().mockResolvedValue({}),
  },
}));

vi.mock('$lib/api/alerts', () => ({
  fetchAlertRules: vi.fn().mockResolvedValue([]),
  createAlertRule: vi.fn(),
  updateAlertRule: vi.fn(),
  toggleAlertRule: vi.fn(),
  deleteAlertRule: vi.fn(),
  testAlertRule: vi.fn(),
  resetAlertDefaults: vi.fn(),
  fetchAlertHistory: vi.fn().mockResolvedValue([]),
  clearAlertHistory: vi.fn(),
  fetchAlertSchema: vi.fn().mockResolvedValue(null),
  fetchAlertStatus: vi.fn().mockResolvedValue({ engine_active: false }),
  exportAlertRules: vi.fn(),
  importAlertRules: vi.fn(),
}));

const PAGE = ['', 'ui', 'settings', 'notifications'].join('/');
const CHANNELS = `${PAGE}?tab=channels`;
const OTHER_PAGE: NavigationTarget = { path: ['', 'ui', 'dashboard'].join('/'), search: '' };
const OTHER_TAB: NavigationTarget = { path: PAGE, search: '?tab=rules' };
const HERE: NavigationTarget = { path: PAGE, search: '?tab=channels' };

describe('NotificationsSettingsPage unsaved-changes guard', () => {
  const originalLocation = window.location;

  beforeEach(() => {
    Object.defineProperty(window, 'location', { value: document.location, configurable: true });
    navigation.redirect(CHANNELS);
    providers = [];
    vi.mocked(api.post).mockClear();
  });

  afterEach(() => {
    Object.defineProperty(window, 'location', { value: originalLocation, configurable: true });
  });

  async function renderLoaded() {
    const view = render(NotificationsSettingsPage);
    await screen.findByLabelText('settings.notifications.push.enable');
    return view;
  }

  it('does not block a pristine page', async () => {
    await renderLoaded();

    expect(unsavedChanges.blocks(OTHER_PAGE, HERE)).toBe(false);
  });

  it('blocks a path change, but not a tab switch, after a template edit', async () => {
    const user = userEvent.setup();
    await renderLoaded();

    await user.type(
      await screen.findByLabelText('settings.notifications.templates.titleLabel'),
      'x'
    );

    expect(unsavedChanges.blocks(OTHER_PAGE, HERE)).toBe(true);
    expect(unsavedChanges.blocks(OTHER_TAB, HERE)).toBe(false);
  });

  it('blocks after a push setting changes', async () => {
    const user = userEvent.setup();
    await renderLoaded();

    await user.click(screen.getByLabelText('settings.notifications.push.enable'));

    expect(unsavedChanges.blocks(OTHER_PAGE, HERE)).toBe(true);
  });

  it('does not block an opened, untouched provider form, and blocks once it is typed in', async () => {
    const user = userEvent.setup();
    await renderLoaded();

    await user.click(
      screen.getByRole('button', { name: /settings\.notifications\.push\.providers\.addButton/ })
    );
    await screen.findByLabelText('settings.notifications.push.form.name.label');
    expect(unsavedChanges.blocks(OTHER_PAGE, HERE)).toBe(false);

    await user.type(screen.getByLabelText('settings.notifications.push.form.name.label'), 'Mine');
    await user.tab();

    expect(unsavedChanges.blocks(OTHER_PAGE, HERE)).toBe(true);
  });

  it('does not block an untouched ntfy provider form after Test Connection switches its protocol', async () => {
    providers = [NTFY_PROVIDER];
    const user = userEvent.setup();
    await renderLoaded();

    await user.click(
      await screen.findByRole('button', {
        name: 'settings.notifications.push.providers.editButton',
      })
    );
    await user.click(
      await screen.findByRole('button', {
        name: 'settings.notifications.push.services.ntfy.testConnection',
      })
    );

    await waitFor(() => expect(api.post).toHaveBeenCalled());
    await screen.findByText(/settings\.notifications\.push\.services\.ntfy\./, {
      selector: 'span.text-xs',
    });
    expect(unsavedChanges.blocks(OTHER_PAGE, HERE)).toBe(false);
  });

  it('keeps blocking an edited ntfy provider form after Test Connection', async () => {
    providers = [NTFY_PROVIDER];
    const user = userEvent.setup();
    await renderLoaded();
    await user.click(
      await screen.findByRole('button', {
        name: 'settings.notifications.push.providers.editButton',
      })
    );
    await user.type(
      await screen.findByLabelText('settings.notifications.push.form.name.label'),
      'x'
    );
    await user.tab();
    expect(unsavedChanges.blocks(OTHER_PAGE, HERE)).toBe(true);

    await user.click(
      await screen.findByRole('button', {
        name: 'settings.notifications.push.services.ntfy.testConnection',
      })
    );

    await waitFor(() => expect(api.post).toHaveBeenCalled());
    await screen.findByText(/settings\.notifications\.push\.services\.ntfy\./, {
      selector: 'span.text-xs',
    });
    expect(unsavedChanges.blocks(OTHER_PAGE, HERE)).toBe(true);
  });

  it('does not block once the user undoes their own edit, whatever protocol the probe chose', async () => {
    providers = [NTFY_PROVIDER];
    const user = userEvent.setup();
    await renderLoaded();
    await user.click(
      await screen.findByRole('button', {
        name: 'settings.notifications.push.providers.editButton',
      })
    );
    const name = await screen.findByLabelText('settings.notifications.push.form.name.label');
    await user.type(name, 'x');
    await user.tab();
    await user.click(
      await screen.findByRole('button', {
        name: 'settings.notifications.push.services.ntfy.testConnection',
      })
    );
    await waitFor(() => expect(api.post).toHaveBeenCalled());
    await screen.findByText(/settings\.notifications\.push\.services\.ntfy\./, {
      selector: 'span.text-xs',
    });

    // Undo the name edit: the only difference left is the protocol the probe wrote itself.
    await user.clear(name);
    await user.type(name, NTFY_PROVIDER.name);
    await user.tab();

    expect(unsavedChanges.blocks(OTHER_PAGE, HERE)).toBe(false);
  });

  it('keeps guarding text typed into the template while its save is pending', async () => {
    let finishSave: () => void = () => {};
    vi.mocked(api.patch).mockReturnValueOnce(
      new Promise(resolve => {
        finishSave = () => resolve({});
      })
    );
    const user = userEvent.setup();
    await renderLoaded();
    const title = await screen.findByLabelText('settings.notifications.templates.titleLabel');
    await user.type(title, 'one');
    await user.click(
      screen.getByRole('button', { name: /settings\.notifications\.templates\.saveButtonUnsaved/ })
    );

    await user.type(title, 'two');
    finishSave();
    await waitFor(() =>
      expect(screen.queryByText('settings.notifications.templates.saveSuccess')).not.toBeNull()
    );

    // 'two' went out after the submitted text, so it is still unsaved.
    expect(unsavedChanges.blocks(OTHER_PAGE, HERE)).toBe(true);
  });

  it('keeps guarding a provider toggled while the push settings save is pending', async () => {
    providers = [globalThis.structuredClone(NTFY_PROVIDER)];
    let finishSave: () => void = () => {};
    vi.mocked(api.patch).mockReturnValueOnce(
      new Promise(resolve => {
        finishSave = () => resolve({});
      })
    );
    const user = userEvent.setup();
    await renderLoaded();
    const toggle = await screen.findByLabelText(
      'settings.notifications.push.providers.enableToggle'
    );
    await user.click(toggle);
    await user.click(
      screen.getByRole('button', { name: /settings\.notifications\.templates\.saveButtonUnsaved/ })
    );

    // Toggled back while the save of the off state is still in flight.
    await user.click(toggle);
    finishSave();
    await waitFor(() =>
      expect(screen.queryByText('settings.notifications.templates.saveSuccess')).not.toBeNull()
    );

    // The server now holds the off state; the on state shown is not saved.
    expect(unsavedChanges.blocks(OTHER_PAGE, HERE)).toBe(true);
  });
});
