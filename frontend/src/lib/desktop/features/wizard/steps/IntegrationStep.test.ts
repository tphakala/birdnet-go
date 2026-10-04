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
    vi.mocked(settingsActions.saveSection).mockClear().mockResolvedValue(undefined);
  });

  const clickByName = (name: RegExp) => fireEvent.click(screen.getByRole('button', { name }));
  const PRIVACY = /wizard\.steps\.integration\.privacyFilterLabel/;
  const BIRDWEATHER = /wizard\.steps\.integration\.birdweatherLabel/;
  const SENTRY = /wizard\.steps\.integration\.errorReportingLabel/;

  async function changeAll() {
    await clickByName(PRIVACY);
    await clickByName(BIRDWEATHER);
    const input = await screen.findByRole('textbox');
    await fireEvent.input(input, { target: { value: 'abc123' } });
    await clickByName(SENTRY);
  }

  it('the leave handler patches birdweather, privacyfilter and sentry in order', async () => {
    const { leave } = renderStep(IntegrationStep);
    await flushAsync();
    await changeAll();

    await leave();
    await leave();

    expect(vi.mocked(settingsActions.saveSection).mock.calls).toEqual([
      ['birdweather', { enabled: true, id: 'abc123' }],
      ['privacyfilter', { enabled: false }],
      ['sentry', { enabled: true }],
    ]);
  });

  it('the leave handler patches only the sections that changed', async () => {
    const { leave } = renderStep(IntegrationStep);
    await flushAsync();
    await clickByName(SENTRY);

    await leave();

    expect(vi.mocked(settingsActions.saveSection).mock.calls).toEqual([
      ['sentry', { enabled: true }],
    ]);
  });

  it('sends no further sections once the step unmounted during a save', async () => {
    let resolveSave: () => void = () => {};
    vi.mocked(settingsActions.saveSection).mockImplementationOnce(
      () =>
        new Promise<void>(resolve => {
          resolveSave = resolve;
        })
    );
    const { leave, unmount } = renderStep(IntegrationStep);
    await flushAsync();
    await changeAll();

    const pending = leave();
    await flushAsync();
    unmount();
    resolveSave();
    await pending;

    expect(vi.mocked(settingsActions.saveSection).mock.calls).toEqual([
      ['birdweather', { enabled: true, id: 'abc123' }],
    ]);
  });

  it('a failed birdweather save sends nothing else', async () => {
    vi.mocked(settingsActions.saveSection).mockRejectedValueOnce(new Error('bad token'));
    const { leave } = renderStep(IntegrationStep);
    await flushAsync();
    await changeAll();

    await expect(leave()).rejects.toThrow('bad token');

    expect(settingsActions.saveSection).toHaveBeenCalledTimes(1);
    expect(settingsActions.saveSection).toHaveBeenCalledWith('birdweather', {
      enabled: true,
      id: 'abc123',
    });
  });

  it('a retry after a failed privacyfilter save sends only privacyfilter and sentry', async () => {
    vi.mocked(settingsActions.saveSection)
      .mockResolvedValueOnce(undefined)
      .mockRejectedValueOnce(new Error('privacy failed'));
    const { leave } = renderStep(IntegrationStep);
    await flushAsync();
    await changeAll();

    await expect(leave()).rejects.toThrow('privacy failed');
    vi.mocked(settingsActions.saveSection).mockClear();
    await leave();

    expect(vi.mocked(settingsActions.saveSection).mock.calls).toEqual([
      ['privacyfilter', { enabled: false }],
      ['sentry', { enabled: true }],
    ]);
  });
});
