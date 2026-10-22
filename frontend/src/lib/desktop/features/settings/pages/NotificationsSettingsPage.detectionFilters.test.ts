import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import NotificationsSettingsPage from './NotificationsSettingsPage.svelte';
import { api, ApiError } from '$lib/utils/api';
import { t } from '$lib/i18n';
import { navigation } from '$lib/stores/navigation.svelte';
import { setBasePath, resetBasePath } from '$lib/utils/urlHelpers';
import { fetchAlertRules } from '$lib/api/alerts';
import type { AlertStatus } from '$lib/api/alerts';

interface PushFixture {
  minConfidenceThreshold: number;
  speciesCooldownMinutes: number;
}

// Per-test inputs read by the mocks below, so no test overrides a mock.
let pushFixture: PushFixture = { minConfidenceThreshold: 0, speciesCooldownMinutes: 0 };
let statusResult: () => Promise<AlertStatus> = () => Promise.resolve({ engine_active: false });

vi.mock('$lib/utils/api', async importOriginal => ({
  ...(await importOriginal<typeof import('$lib/utils/api')>()),
  api: {
    get: vi.fn(() =>
      Promise.resolve({
        push: { enabled: true, providers: [], ...pushFixture },
      })
    ),
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
  fetchAlertStatus: vi.fn(() => statusResult()),
  exportAlertRules: vi.fn(),
  importAlertRules: vi.fn(),
}));

const NOTICE_KEY = 'settings.notifications.push.rulesNotice.message';
const PREVIOUS_FILTERS_KEY = 'settings.notifications.push.rulesNotice.previousFilters';
const OPEN_RULES_KEY = 'settings.notifications.push.rulesNotice.openRules';
const MIN_CONFIDENCE_LABEL = 'settings.notifications.push.filters.minConfidence.label';
const ENABLED_DESCRIPTION = 'settings.notifications.push.enabledDescription';

function conflict(): ApiError {
  return new ApiError('Alert rules require the enhanced (v2) database', 409, new Response());
}

describe('NotificationsSettingsPage detection filters under the alert engine', () => {
  const originalLocation = window.location;

  beforeEach(() => {
    // Shared test setup stubs window.location; the Rules link needs JSDOM's
    // history-aware location to switch tabs.
    Object.defineProperty(window, 'location', { value: document.location, configurable: true });
    navigation.redirect('/ui/settings/notifications?tab=channels');
    pushFixture = { minConfidenceThreshold: 0, speciesCooldownMinutes: 0 };
    statusResult = () => Promise.resolve({ engine_active: false });
    vi.mocked(t).mockClear();
    vi.mocked(fetchAlertRules).mockClear();
    vi.mocked(api.patch).mockClear();
  });

  afterEach(() => {
    Object.defineProperty(window, 'location', { value: originalLocation, configurable: true });
    resetBasePath();
  });

  it('shows the rules notice and link instead of the filters when the engine is active', async () => {
    statusResult = () => Promise.resolve({ engine_active: true });
    render(NotificationsSettingsPage);

    const notice = await screen.findByRole('note');
    expect(notice).toHaveTextContent(NOTICE_KEY);
    expect(notice).not.toHaveTextContent(PREVIOUS_FILTERS_KEY);
    const link = within(notice).getByRole('link', { name: OPEN_RULES_KEY });
    expect(link.getAttribute('href')).toMatch(/\/ui\/settings\/notifications\?tab=rules$/);
    expect(screen.queryByLabelText(MIN_CONFIDENCE_LABEL)).not.toBeInTheDocument();
  });

  it('names the ignored filter values, as a percentage and minutes, when both are set', async () => {
    statusResult = () => Promise.resolve({ engine_active: true });
    pushFixture = { minConfidenceThreshold: 0.05, speciesCooldownMinutes: 5 };
    render(NotificationsSettingsPage);

    const notice = await screen.findByRole('note');
    expect(notice).toHaveTextContent(PREVIOUS_FILTERS_KEY);
    expect(vi.mocked(t)).toHaveBeenCalledWith(PREVIOUS_FILTERS_KEY, {
      confidence: 5,
      minutes: 5,
    });
  });

  it('names the ignored values when only Minimum Confidence is set, as a whole percent', async () => {
    statusResult = () => Promise.resolve({ engine_active: true });
    // 0.07 * 100 is 7.000000000000001 in floating point, so this pins the rounding.
    pushFixture = { minConfidenceThreshold: 0.07, speciesCooldownMinutes: 0 };
    render(NotificationsSettingsPage);

    const notice = await screen.findByRole('note');
    expect(notice).toHaveTextContent(PREVIOUS_FILTERS_KEY);
    expect(vi.mocked(t)).toHaveBeenCalledWith(
      PREVIOUS_FILTERS_KEY,
      expect.objectContaining({ confidence: 7 })
    );
  });

  it('names the ignored values when only Species Cooldown is set', async () => {
    statusResult = () => Promise.resolve({ engine_active: true });
    pushFixture = { minConfidenceThreshold: 0, speciesCooldownMinutes: 5 };
    render(NotificationsSettingsPage);

    const notice = await screen.findByRole('note');
    expect(notice).toHaveTextContent(PREVIOUS_FILTERS_KEY);
  });

  it('keeps the hidden filter values when the Channels tab is saved', async () => {
    statusResult = () => Promise.resolve({ engine_active: true });
    pushFixture = { minConfidenceThreshold: 0.05, speciesCooldownMinutes: 5 };
    const user = userEvent.setup();
    render(NotificationsSettingsPage);
    await screen.findByRole('note');

    // Toggling push makes the push settings dirty so the save button saves them.
    await user.click(screen.getByLabelText('settings.notifications.push.enable'));
    await user.click(
      screen.getByRole('button', { name: /settings\.notifications\.templates\.saveButtonUnsaved/ })
    );

    expect(vi.mocked(api.patch)).toHaveBeenCalledWith('/api/v2/settings/notification', {
      push: expect.objectContaining({ minConfidenceThreshold: 0.05, speciesCooldownMinutes: 5 }),
    });
  });

  it('shows neither the filters nor the notice while the engine status is pending', async () => {
    statusResult = () => new Promise<AlertStatus>(() => {});
    render(NotificationsSettingsPage);

    // Rendered only after the settings load with push enabled.
    expect(await screen.findByText(ENABLED_DESCRIPTION)).toBeInTheDocument();
    expect(screen.queryByLabelText(MIN_CONFIDENCE_LABEL)).not.toBeInTheDocument();
    expect(screen.queryByRole('note')).not.toBeInTheDocument();
    // The Rules tab must not wait on the status call.
    await waitFor(() => expect(vi.mocked(fetchAlertRules)).toHaveBeenCalled());
  });

  it('keeps the filter inputs and the v2 state when the status request fails for another reason', async () => {
    statusResult = () => Promise.reject(new Error('network down'));
    render(NotificationsSettingsPage);

    expect(await screen.findByLabelText(MIN_CONFIDENCE_LABEL)).toBeInTheDocument();
    expect(screen.queryByRole('note')).not.toBeInTheDocument();
    // loadRules returns early once v2Available is false, so a call proves it stayed true.
    await waitFor(() => expect(vi.mocked(fetchAlertRules)).toHaveBeenCalled());
  });

  it.each([
    ['the engine is inactive', () => Promise.resolve({ engine_active: false })],
    ['the alert API answers 409', () => Promise.reject(conflict())],
  ])('keeps the filter inputs and shows no notice when %s', async (_name, result) => {
    statusResult = result;
    render(NotificationsSettingsPage);

    expect(await screen.findByLabelText(MIN_CONFIDENCE_LABEL)).toBeInTheDocument();
    expect(screen.queryByText(NOTICE_KEY)).not.toBeInTheDocument();
  });

  it('opens the Rules tab and moves focus to it from the notice link', async () => {
    statusResult = () => Promise.resolve({ engine_active: true });
    const user = userEvent.setup();
    render(NotificationsSettingsPage);

    const notice = await screen.findByRole('note');
    await user.click(within(notice).getByRole('link', { name: OPEN_RULES_KEY }));

    const rulesTab = document.getElementById('settings-tab-rules');
    expect(rulesTab).toHaveAttribute('aria-selected', 'true');
    expect(rulesTab).toHaveFocus();
  });

  it('links to the database page with the reverse-proxy prefix when alert rules need v2', async () => {
    setBasePath('/proxy');
    navigation.redirect('/ui/settings/notifications?tab=rules');
    vi.mocked(fetchAlertRules).mockRejectedValueOnce(conflict());
    const user = userEvent.setup();
    render(NotificationsSettingsPage);

    const link = await screen.findByRole('link', { name: 'settings.alerts.v2RequiredLink' });
    expect(link).toHaveAttribute('href', '/proxy/ui/system/database');

    await user.click(link);
    expect(navigation.currentPath).toBe('/ui/system/database');
  });
});
