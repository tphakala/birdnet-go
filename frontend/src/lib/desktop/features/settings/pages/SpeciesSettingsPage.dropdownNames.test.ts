import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { settingsStore, type SettingsFormData } from '$lib/stores/settings';
import SpeciesSettingsPage from './SpeciesSettingsPage.svelte';

vi.mock('$lib/utils/api', async importOriginal => ({
  ...(await importOriginal<typeof import('$lib/utils/api')>()),
  api: {
    get: vi.fn().mockResolvedValue({}),
    post: vi.fn().mockResolvedValue({}),
    put: vi.fn().mockResolvedValue({}),
    delete: vi.fn().mockResolvedValue({}),
  },
}));

const trackingSettings = {
  enabled: true,
  newSpeciesWindowDays: 14,
  syncIntervalMinutes: 60,
  notificationSuppressionHours: 24,
  yearlyTracking: { enabled: true, resetMonth: 3, resetDay: 1, windowDays: 7 },
  seasonalTracking: {
    enabled: true,
    windowDays: 7,
    seasons: {
      spring: { startMonth: 3, startDay: 20 },
      summer: { startMonth: 6, startDay: 21 },
      fall: { startMonth: 9, startDay: 22 },
      winter: { startMonth: 12, startDay: 21 },
    },
  },
  infrequentTracking: { enabled: false, absenceDays: 14 },
};

describe('SpeciesSettingsPage tracking dropdown names', () => {
  beforeEach(() => {
    Element.prototype.scrollIntoView = vi.fn();
    // Partial on purpose: SettingsFormData is large and the page reads only these fields here
    const formData = {
      realtime: {
        species: { include: [], exclude: [], config: {} },
        speciesTracking: trackingSettings,
      },
      taxonomySynonyms: {},
    } as unknown as SettingsFormData;
    settingsStore.update(state => ({
      ...state,
      formData,
      originalData: JSON.parse(JSON.stringify(formData)),
    }));
  });

  it('names the reset month and season start month dropdowns by their visible labels', async () => {
    const user = userEvent.setup();
    render(SpeciesSettingsPage);

    await user.click(
      await screen.findByRole('tab', { name: /settings\.species\.tracking\.tabLabel/ })
    );

    const resetMonth = await screen.findByRole('combobox', {
      name: 'settings.species.tracking.yearly.resetMonth.label',
    });
    expect(resetMonth).toHaveAttribute('aria-haspopup', 'listbox');

    // Each season's dropdown carries the season in its name, so the four are told apart
    const startMonths = ['spring', 'summer', 'fall', 'winter'].map(season =>
      screen.getByRole('combobox', {
        name: `settings.species.tracking.seasonal.seasons.${season} settings.species.tracking.seasonal.seasons.startMonth`,
      })
    );
    // The selected month is the combobox value (its text), not part of the name
    expect(resetMonth).toHaveTextContent(/march/i);
    expect(startMonths[0]).toHaveTextContent(/march/i);
    expect(startMonths[1]).toHaveTextContent(/june/i);
  });
});
