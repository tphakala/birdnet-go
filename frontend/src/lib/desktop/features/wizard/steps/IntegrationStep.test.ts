import { describe, it, expect, beforeEach, vi } from 'vitest';
import { fireEvent, screen } from '@testing-library/svelte';

vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string) => key),
  getLocale: vi.fn(() => 'en'),
}));

vi.mock('$lib/stores/settings', async () => {
  const { createSettingsMock } = await import('./stepTestUtils');
  return createSettingsMock({
    realtime: { privacyFilter: { enabled: true }, birdweather: { enabled: false, id: '' } },
    sentry: { enabled: false },
  });
});

import IntegrationStep from './IntegrationStep.svelte';
import { settingsActions } from '$lib/stores/settings';
import { flushAsync, renderStep } from './stepTestUtils';

// The leave handler contract shared by every step is in stepContract.test.ts
describe('IntegrationStep - leave handler', () => {
  beforeEach(() => {
    vi.mocked(settingsActions.updateSection).mockClear();
    vi.mocked(settingsActions.saveSettings).mockClear().mockResolvedValue(undefined);
  });

  it('the leave handler saves the edited values once', async () => {
    const { leave } = renderStep(IntegrationStep);
    await flushAsync();
    await fireEvent.click(
      screen.getByRole('button', { name: /wizard\.steps\.integration\.errorReportingLabel/ })
    );

    await leave();
    await leave();

    expect(settingsActions.updateSection).toHaveBeenCalledTimes(2);
    expect(settingsActions.updateSection).toHaveBeenCalledWith('realtime', {
      privacyFilter: { enabled: true },
      birdweather: { enabled: false, id: '' },
    });
    expect(settingsActions.updateSection).toHaveBeenCalledWith('sentry', { enabled: true });
    expect(settingsActions.saveSettings).toHaveBeenCalledTimes(1);
    expect(settingsActions.saveSettings).toHaveBeenCalledWith({ notify: false });
  });
});
