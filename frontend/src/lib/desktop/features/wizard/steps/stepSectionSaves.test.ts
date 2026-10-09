import { describe, it, expect, beforeEach, vi } from 'vitest';
import { fireEvent, screen } from '@testing-library/svelte';
import { get } from 'svelte/store';
import type { SettingsFormData } from '$lib/stores/settings';

vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string) => key),
  getLocale: vi.fn(() => 'en'),
  setLocale: vi.fn(),
  isValidLocale: vi.fn(() => true),
}));

vi.mock('$lib/utils/api', () => ({
  api: {
    get: vi.fn((url: string) =>
      Promise.resolve(url === '/api/v2/settings/locales' ? { en: 'English' } : [])
    ),
    post: vi.fn(),
  },
}));

// LocationMap relies on maplibre-gl; LanguageSelector is not under test
vi.mock('$lib/desktop/components/forms/LocationMap.svelte');
vi.mock('$lib/desktop/components/ui/LanguageSelector.svelte');

// These tests use the real settings store with the settingsAPI mock from setup.ts.
import AudioSourceStep from './AudioSourceStep.svelte';
import DetectionStep from './DetectionStep.svelte';
import IntegrationStep from './IntegrationStep.svelte';
import LocationLanguageStep from './LocationLanguageStep.svelte';
import { hasUnsavedChanges, settingsActions, settingsStore } from '$lib/stores/settings';
import { settingsAPI } from '$lib/utils/settingsApi.js';
import { flushAsync, renderStep } from './stepTestUtils';
import {
  lookup,
  serverSettings as sharedServerSettings,
} from '../../../../../test/settings-helpers';

/** The shared server fixture, with the location and privacy filter state these steps start from. */
const serverSettings = (): SettingsFormData => {
  const settings = sharedServerSettings();
  const privacyFilter = settings.realtime?.privacyFilter;
  if (!privacyFilter) throw new Error('shared server fixture has no privacy filter section');
  return {
    ...settings,
    birdnet: { ...settings.birdnet, latitude: 40, longitude: -74 },
    realtime: {
      ...settings.realtime,
      privacyFilter: { ...privacyFilter, enabled: true },
    },
  };
};

const patchCalls = () => vi.mocked(settingsAPI.patchSection).mock.calls;

describe('wizard steps save with section PATCH requests', () => {
  beforeEach(async () => {
    vi.mocked(settingsAPI.load).mockResolvedValue(serverSettings());
    vi.mocked(settingsAPI.save).mockReset().mockResolvedValue(undefined);
    vi.mocked(settingsAPI.patchSection).mockReset().mockResolvedValue({});
    await settingsActions.loadSettings();
  });

  it('DetectionStep patches birdnet threshold and keeps the rest of the section', async () => {
    const { leave } = renderStep(DetectionStep);
    await flushAsync();
    await fireEvent.click(
      screen.getByRole('radio', { name: /wizard\.steps\.detection\.highAccuracy/ })
    );

    await leave();

    expect(patchCalls()).toEqual([['birdnet', { threshold: 0.9 }]]);
    expect(settingsAPI.save).not.toHaveBeenCalled();
    expect(get(hasUnsavedChanges)).toBe(false);
    const { formData } = get(settingsStore);
    expect(formData.birdnet.threshold).toBe(0.9);
    expect(formData.birdnet.rangeFilter.threshold).toBe(0.03);
  });

  it('IntegrationStep patches privacyfilter and sentry and keeps nested objects intact', async () => {
    const { leave } = renderStep(IntegrationStep);
    await flushAsync();
    await fireEvent.click(
      screen.getByRole('checkbox', { name: /wizard\.steps\.integration\.privacyFilterLabel/ })
    );
    await fireEvent.click(
      screen.getByRole('checkbox', { name: /wizard\.steps\.integration\.errorReportingLabel/ })
    );

    await leave();

    expect(patchCalls()).toEqual([
      ['privacyfilter', { enabled: false }],
      ['sentry', { enabled: true }],
    ]);
    expect(settingsAPI.save).not.toHaveBeenCalled();
    expect(get(hasUnsavedChanges)).toBe(false);
    for (const copy of [get(settingsStore).formData, get(settingsStore).originalData]) {
      expect(lookup(copy, ['realtime', 'privacyFilter', 'enabled'])).toBe(false);
      expect(lookup(copy, ['realtime', 'privacyFilter', 'confidence'])).toBe(0.7);
      expect(lookup(copy, ['realtime', 'privacyFilter', 'debug'])).toBe(true);
      expect(lookup(copy, ['realtime', 'birdweather', 'threshold'])).toBe(0.9);
      expect(lookup(copy, ['sentry', 'enabled'])).toBe(true);
    }
  });

  it('returning to Integration after Back from a malformed token shows the stored BirdWeather state and the saved toggles', async () => {
    const first = renderStep(IntegrationStep);
    await flushAsync();
    await fireEvent.click(
      screen.getByRole('checkbox', { name: /wizard\.steps\.integration\.privacyFilterLabel/ })
    );
    await fireEvent.click(
      screen.getByRole('checkbox', { name: /wizard\.steps\.integration\.errorReportingLabel/ })
    );
    await fireEvent.click(
      screen.getByRole('checkbox', { name: /wizard\.steps\.integration\.birdweatherLabel/ })
    );
    await fireEvent.input(
      await screen.findByRole('textbox', {
        name: 'settings.integration.birdweather.token.label',
      }),
      { target: { value: 'abc' } }
    );

    await first.leave();
    first.unmount();
    const second = renderStep(IntegrationStep);
    await flushAsync();

    expect(patchCalls()).toEqual([
      ['privacyfilter', { enabled: false }],
      ['sentry', { enabled: true }],
    ]);
    expect(
      screen.getByRole('checkbox', { name: /wizard\.steps\.integration\.privacyFilterLabel/ })
    ).not.toBeChecked();
    expect(
      screen.getByRole('checkbox', { name: /wizard\.steps\.integration\.errorReportingLabel/ })
    ).toBeChecked();
    expect(
      screen.getByRole('checkbox', { name: /wizard\.steps\.integration\.birdweatherLabel/ })
    ).not.toBeChecked();
    expect(screen.queryByLabelText('settings.integration.birdweather.token.label')).toBeNull();
    await second.leave();
    expect(patchCalls()).toHaveLength(2);
  });

  it('AudioSourceStep stream choice keeps existing streams and clears sound cards', async () => {
    const { leave } = renderStep(AudioSourceStep);
    await flushAsync();
    await fireEvent.click(
      screen.getByRole('radio', { name: /wizard\.steps\.audioSource\.rtspStream/ })
    );
    const input = await screen.findByPlaceholderText('wizard.steps.audioSource.rtspUrlPlaceholder');
    await fireEvent.input(input, { target: { value: 'rtsp://camera.example/stream' } });

    await leave();

    expect(patchCalls()).toEqual([
      [
        'rtsp',
        {
          streams: [
            { name: 'Old', url: 'rtsp://camera.example/stream', enabled: true, type: 'rtsp' },
          ],
        },
      ],
      ['audio', { sources: [], source: '' }],
    ]);
    expect(settingsAPI.save).not.toHaveBeenCalled();
    expect(get(hasUnsavedChanges)).toBe(false);
    for (const copy of [get(settingsStore).formData, get(settingsStore).originalData]) {
      expect(copy.realtime?.rtsp?.health).toEqual({ healthyDataThreshold: 60 });
      expect(copy.realtime?.audio?.sources).toEqual([]);
      expect(copy.realtime?.audio?.source).toBe('');
      expect(copy.realtime?.rtsp?.streams).toEqual([
        { name: 'Old', url: 'rtsp://camera.example/stream', enabled: true, type: 'rtsp' },
      ]);
    }
  });

  it('LocationLanguageStep patches birdnet with the location and keeps the range filter', async () => {
    const { leave, container } = renderStep(LocationLanguageStep);
    await flushAsync();
    const latitude = container.querySelector('input[type="number"]');
    if (!(latitude instanceof HTMLInputElement)) throw new Error('latitude input not found');
    await fireEvent.input(latitude, { target: { value: '41.5' } });
    await fireEvent.change(latitude, { target: { value: '41.5' } });

    await leave();

    expect(patchCalls()).toEqual([
      ['birdnet', { latitude: 41.5, longitude: -74, locale: 'en', locationConfigured: true }],
    ]);
    expect(settingsAPI.save).not.toHaveBeenCalled();
    expect(get(hasUnsavedChanges)).toBe(false);
    const { formData } = get(settingsStore);
    expect(formData.birdnet.latitude).toBe(41.5);
    expect(formData.birdnet.locationConfigured).toBe(true);
    expect(formData.birdnet.rangeFilter.threshold).toBe(0.03);
  });

  it('a rejected PATCH makes leave() reject and leaves the store as loaded', async () => {
    const before = JSON.stringify(get(settingsStore));
    vi.mocked(settingsAPI.patchSection).mockRejectedValueOnce(new Error('rejected by server'));
    const { leave } = renderStep(DetectionStep);
    await flushAsync();
    await fireEvent.click(
      screen.getByRole('radio', { name: /wizard\.steps\.detection\.highAccuracy/ })
    );

    await expect(leave()).rejects.toThrow('rejected by server');

    expect(JSON.stringify(get(settingsStore))).toBe(before);
    expect(settingsAPI.save).not.toHaveBeenCalled();
  });
});

describe('DetectionStep on the real settings store', () => {
  const withThreshold = (threshold: number): SettingsFormData => {
    const settings = serverSettings();
    return { ...settings, birdnet: { ...settings.birdnet, threshold } };
  };
  const checkedName = () =>
    screen.getAllByRole('radio').filter(r => r.getAttribute('aria-checked') === 'true');
  const loadThreshold = async (threshold: number) => {
    vi.mocked(settingsAPI.load).mockResolvedValue(withThreshold(threshold));
    vi.mocked(settingsAPI.patchSection).mockReset().mockResolvedValue({});
    await settingsActions.loadSettings();
  };

  it('stored 0.75: a pick saves once and a remount shows the pick without a stored value line', async () => {
    await loadThreshold(0.75);
    const first = renderStep(DetectionStep);
    await flushAsync();
    await fireEvent.click(
      screen.getByRole('radio', { name: /wizard\.steps\.detection\.highAccuracy/ })
    );
    await first.leave();
    first.unmount();

    const second = renderStep(DetectionStep);
    await flushAsync();

    expect(patchCalls()).toEqual([['birdnet', { threshold: 0.9 }]]);
    expect(checkedName()).toHaveLength(1);
    expect(checkedName()[0]).toHaveAccessibleName(/highAccuracy/);
    expect(screen.queryByText(/detection\.descriptionStored/)).toBeNull();
    await second.leave();
    expect(patchCalls()).toHaveLength(1);
  });

  it('stored 0.75: leaving untouched twice writes nothing and keeps the stored value line', async () => {
    await loadThreshold(0.75);
    const first = renderStep(DetectionStep);
    await flushAsync();
    await first.leave();
    first.unmount();
    const second = renderStep(DetectionStep);
    await flushAsync();

    expect(checkedName()).toHaveLength(0);
    expect(screen.getByText(/detection\.descriptionStored/)).toBeInTheDocument();
    await second.leave();
    expect(patchCalls()).toEqual([]);
  });

  it('stored 0.8: a failed save is resent and a successful one is not', async () => {
    await loadThreshold(0.8);
    const { leave } = renderStep(DetectionStep);
    await flushAsync();
    await fireEvent.click(
      screen.getByRole('radio', { name: /wizard\.steps\.detection\.highAccuracy/ })
    );
    vi.mocked(settingsAPI.patchSection).mockRejectedValueOnce(new Error('offline'));

    await expect(leave()).rejects.toThrow();
    await leave();
    await leave();

    expect(patchCalls()).toEqual([
      ['birdnet', { threshold: 0.9 }],
      ['birdnet', { threshold: 0.9 }],
    ]);
  });
});
