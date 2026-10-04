import { describe, it, expect, beforeEach, vi } from 'vitest';
import { fireEvent, screen } from '@testing-library/svelte';

vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string) => key),
  getLocale: vi.fn(() => 'en'),
}));

vi.mock('$lib/utils/api', () => ({
  api: {
    get: vi.fn().mockResolvedValue([]),
    post: vi.fn(),
  },
}));

vi.mock('$lib/stores/settings', async () => {
  const { createSettingsMock } = await import('./stepTestUtils');
  return createSettingsMock({ realtime: { audio: { source: '' }, rtsp: { streams: [] } } });
});

import AudioSourceStep from './AudioSourceStep.svelte';
import { settingsActions, settingsStore } from '$lib/stores/settings';
import { api } from '$lib/utils/api';
import { flushAsync, renderStep } from './stepTestUtils';

const RTSP_URL = 'rtsp://camera.example/stream';

async function edit() {
  await fireEvent.click(
    screen.getByRole('radio', { name: /wizard\.steps\.audioSource\.rtspStream/ })
  );
  const input = await screen.findByPlaceholderText('wizard.steps.audioSource.rtspUrlPlaceholder');
  await fireEvent.input(input, { target: { value: RTSP_URL } });
}

// The leave handler contract shared by every step is in stepContract.test.ts
describe('AudioSourceStep - leave handler', () => {
  beforeEach(() => {
    vi.mocked(settingsActions.updateSection).mockClear();
    vi.mocked(settingsActions.saveSettings).mockClear().mockResolvedValue(undefined);
    vi.mocked(settingsActions.resetAllSettings).mockClear();
  });

  it('the leave handler saves the edited values once', async () => {
    const { leave } = renderStep(AudioSourceStep);
    await flushAsync();
    await edit();

    await leave();
    await leave();

    expect(settingsActions.updateSection).toHaveBeenCalledTimes(1);
    expect(settingsActions.updateSection).toHaveBeenCalledWith('realtime', {
      rtsp: {
        streams: [{ name: 'Stream 1', url: RTSP_URL, type: 'rtsp', transport: 'tcp' }],
      },
    });
    expect(settingsActions.saveSettings).toHaveBeenCalledTimes(1);
    expect(settingsActions.saveSettings).toHaveBeenCalledWith({ notify: false });
  });

  it('configure later saves nothing', async () => {
    const { leave } = renderStep(AudioSourceStep);
    await flushAsync();
    await fireEvent.click(
      screen.getByRole('radio', { name: /wizard\.steps\.audioSource\.rtspStream/ })
    );
    await fireEvent.click(
      await screen.findByRole('button', { name: 'wizard.steps.audioSource.configureLater' })
    );

    await leave();

    expect(settingsActions.updateSection).not.toHaveBeenCalled();
    expect(settingsActions.saveSettings).not.toHaveBeenCalled();
  });

  it('the leave handler saves the selected sound card device', async () => {
    vi.mocked(api.get).mockResolvedValueOnce([{ name: 'USB Mic', index: 1, id: 'hw:1,0' }]);
    settingsStore.update(state => {
      const realtime = state.formData.realtime as unknown as { audio: { source: string } };
      realtime.audio.source = 'hw:1,0';
      return state;
    });
    const { leave } = renderStep(AudioSourceStep);
    await flushAsync();
    await fireEvent.click(
      screen.getByRole('radio', { name: /wizard\.steps\.audioSource\.soundcard/ })
    );

    await leave();

    expect(settingsActions.updateSection).toHaveBeenCalledTimes(1);
    expect(settingsActions.updateSection).toHaveBeenCalledWith('realtime', {
      audio: { source: 'hw:1,0' },
    });
    expect(settingsActions.saveSettings).toHaveBeenCalledTimes(1);

    settingsStore.update(state => {
      (state.formData.realtime as unknown as { audio: { source: string } }).audio.source = '';
      return state;
    });
  });
});
