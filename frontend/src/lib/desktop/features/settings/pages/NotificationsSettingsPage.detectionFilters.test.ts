import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import NotificationsSettingsPage from './NotificationsSettingsPage.svelte';
import { ApiError } from '$lib/utils/api';
import { t } from '$lib/i18n';
import { navigation } from '$lib/stores/navigation.svelte';
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
  });

  afterEach(() => {
    Object.defineProperty(window, 'location', { value: originalLocation, configurable: true });
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

  it('names the ignored filter values, as a percentage and minutes, when either is set', async () => {
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
});
