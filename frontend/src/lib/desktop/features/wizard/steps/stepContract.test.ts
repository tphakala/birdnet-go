import { describe, it, expect, beforeEach, vi } from 'vitest';
import { fireEvent, screen } from '@testing-library/svelte';
import type { Component } from 'svelte';
import type { WizardStepProps } from '../types';

vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string) => key),
  getLocale: vi.fn(() => 'en'),
}));

vi.mock('$lib/utils/api', () => ({
  api: {
    get: vi.fn((url: string) =>
      Promise.resolve(url === '/api/v2/settings/locales' ? { en: 'English' } : [])
    ),
    post: vi.fn(),
  },
}));

// LocationPickerMap relies on maplibre-gl; LanguageSelector is not under test
vi.mock('../components/LocationPickerMap.svelte');
vi.mock('$lib/desktop/components/ui/LanguageSelector.svelte');

vi.mock('$lib/stores/settings', async () => {
  const { createSettingsMock } = await import('./stepTestUtils');
  return createSettingsMock({
    birdnet: { threshold: 0.8, latitude: 40, longitude: -74, locale: 'en' },
    realtime: {
      audio: { source: '' },
      rtsp: { streams: [] },
      privacyFilter: { enabled: true },
      birdweather: { enabled: false, id: '' },
      dashboard: { summaryLimit: 100, locale: 'en' },
    },
    sentry: { enabled: false },
  });
});

import AudioSourceStep from './AudioSourceStep.svelte';
import DetectionStep from './DetectionStep.svelte';
import IntegrationStep from './IntegrationStep.svelte';
import LocationLanguageStep from './LocationLanguageStep.svelte';
import { settingsActions } from '$lib/stores/settings';
import { flushAsync, renderStep } from './stepTestUtils';

interface StepCase {
  name: string;
  component: Component<WizardStepProps>;
  /** Makes an edit the step's leave handler must save. */
  edit: (container: HTMLElement) => Promise<void>;
}

const stepCases: StepCase[] = [
  {
    name: 'AudioSourceStep',
    component: AudioSourceStep,
    edit: async () => {
      await fireEvent.click(
        screen.getByRole('radio', { name: /wizard\.steps\.audioSource\.rtspStream/ })
      );
      const input = await screen.findByPlaceholderText(
        'wizard.steps.audioSource.rtspUrlPlaceholder'
      );
      await fireEvent.input(input, { target: { value: 'rtsp://camera.example/stream' } });
    },
  },
  {
    name: 'DetectionStep',
    component: DetectionStep,
    edit: async () => {
      await fireEvent.click(
        screen.getByRole('radio', { name: /wizard\.steps\.detection\.highAccuracy/ })
      );
    },
  },
  {
    name: 'IntegrationStep',
    component: IntegrationStep,
    edit: async () => {
      await fireEvent.click(
        screen.getByRole('button', { name: /wizard\.steps\.integration\.errorReportingLabel/ })
      );
    },
  },
  {
    name: 'LocationLanguageStep',
    component: LocationLanguageStep,
    edit: async container => {
      const latitude = container.querySelector('input[type="number"]');
      if (!(latitude instanceof HTMLInputElement)) throw new Error('latitude input not found');
      await fireEvent.input(latitude, { target: { value: '41.5' } });
      await fireEvent.change(latitude, { target: { value: '41.5' } });
    },
  },
];

describe.each(stepCases)('$name leave handler contract', ({ component, edit }) => {
  beforeEach(() => {
    vi.mocked(settingsActions.updateSection).mockClear();
    vi.mocked(settingsActions.saveSettings).mockClear().mockResolvedValue(undefined);
    vi.mocked(settingsActions.resetAllSettings).mockClear();
  });

  it('registers a leave handler on mount and unregisters on destroy', async () => {
    const { registerLeaveHandler, unregister, unmount } = renderStep(component);
    await flushAsync();

    expect(registerLeaveHandler).toHaveBeenCalledTimes(1);
    expect(unregister).not.toHaveBeenCalled();

    unmount();

    expect(unregister).toHaveBeenCalledTimes(1);
  });

  it('unmounting never saves', async () => {
    const { unmount, container } = renderStep(component);
    await flushAsync();
    await edit(container);

    unmount();
    await flushAsync();

    expect(settingsActions.updateSection).not.toHaveBeenCalled();
    expect(settingsActions.saveSettings).not.toHaveBeenCalled();
  });

  it('the leave handler does nothing without edits', async () => {
    const { leave } = renderStep(component);
    await flushAsync();

    await leave();

    expect(settingsActions.updateSection).not.toHaveBeenCalled();
    expect(settingsActions.saveSettings).not.toHaveBeenCalled();
  });

  it('the leave handler rejects and reverts the store when the save rejects', async () => {
    const failure = new Error('save failed');
    vi.mocked(settingsActions.saveSettings).mockRejectedValueOnce(failure);
    const { leave, container } = renderStep(component);
    await flushAsync();
    await edit(container);

    await expect(leave()).rejects.toBe(failure);
    expect(settingsActions.resetAllSettings).toHaveBeenCalledTimes(1);
  });

  it('does not revert the store when the save fails after the step unmounted', async () => {
    const failure = new Error('late failure');
    vi.mocked(settingsActions.saveSettings).mockRejectedValueOnce(failure);
    const { leave, unmount, container } = renderStep(component);
    await flushAsync();
    await edit(container);

    const pending = leave();
    unmount();

    await expect(pending).rejects.toBe(failure);
    expect(settingsActions.resetAllSettings).not.toHaveBeenCalled();
  });

  it('keeps the edits after a failed save so the next leave call retries them', async () => {
    vi.mocked(settingsActions.saveSettings).mockRejectedValueOnce(new Error('save failed'));
    const { leave, container } = renderStep(component);
    await flushAsync();
    await edit(container);

    await expect(leave()).rejects.toThrow('save failed');
    const firstLeaveCalls = vi.mocked(settingsActions.updateSection).mock.calls.length;
    await leave();

    expect(settingsActions.saveSettings).toHaveBeenCalledTimes(2);
    expect(firstLeaveCalls).toBeGreaterThan(0);
    const calls = vi.mocked(settingsActions.updateSection).mock.calls;
    expect(calls.slice(firstLeaveCalls)).toEqual(calls.slice(0, firstLeaveCalls));
  });
});
