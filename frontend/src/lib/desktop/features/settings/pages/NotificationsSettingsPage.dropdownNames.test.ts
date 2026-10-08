import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import NotificationsSettingsPage from './NotificationsSettingsPage.svelte';

vi.mock('$lib/utils/api', async importOriginal => ({
  ...(await importOriginal<typeof import('$lib/utils/api')>()),
  api: {
    get: vi.fn().mockResolvedValue({
      push: {
        enabled: true,
        providers: [],
        minConfidenceThreshold: 0,
        speciesCooldownMinutes: 0,
      },
    }),
    post: vi.fn().mockResolvedValue({}),
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
  exportAlertRules: vi.fn(),
  importAlertRules: vi.fn(),
}));

describe('NotificationsSettingsPage protocol dropdown names', () => {
  beforeEach(() => {
    Element.prototype.scrollIntoView = vi.fn();
  });

  async function openProviderForm(user: ReturnType<typeof userEvent.setup>) {
    render(NotificationsSettingsPage);
    await user.click(
      await screen.findByRole('button', {
        name: /settings\.notifications\.push\.providers\.addButton/,
      })
    );
  }

  async function chooseService(user: ReturnType<typeof userEvent.setup>, service: string) {
    await user.click(
      await screen.findByRole('button', {
        name: 'settings.notifications.push.services.selectLabel',
      })
    );
    await user.click(await screen.findByRole('option', { name: service }));
  }

  it('names the ntfy protocol dropdown independently of the selected protocol', async () => {
    const user = userEvent.setup();
    await openProviderForm(user);
    await chooseService(user, 'ntfy');

    const server = await screen.findByLabelText(
      'settings.notifications.push.services.ntfy.server.label'
    );
    await fireEvent.change(server, { target: { value: 'ntfy.example.org' } });

    const protocol = await screen.findByRole('button', {
      name: 'settings.notifications.push.services.ntfy.protocol.label',
    });
    expect(protocol).toHaveTextContent('HTTPS');
  });

  it('names the gotify protocol dropdown independently of the selected protocol', async () => {
    const user = userEvent.setup();
    await openProviderForm(user);
    await chooseService(user, 'Gotify');

    const protocol = await screen.findByRole<HTMLButtonElement>('button', {
      name: 'settings.notifications.push.services.gotify.protocol.label',
    });
    expect(protocol).toHaveTextContent('HTTPS');
    // The visible text is the control's label, not a copy of it in aria-label
    expect(protocol).not.toHaveAttribute('aria-label');
    expect(protocol.labels).toHaveLength(1);
  });
});
