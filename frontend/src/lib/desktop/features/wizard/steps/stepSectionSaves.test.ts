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

// LocationPickerMap relies on maplibre-gl; LanguageSelector is not under test
vi.mock('../components/LocationPickerMap.svelte');
vi.mock('$lib/desktop/components/ui/LanguageSelector.svelte');

// These tests use the real settings store with the settingsAPI mock from setup.ts.
import AudioSourceStep from './AudioSourceStep.svelte';
import DetectionStep from './DetectionStep.svelte';
import IntegrationStep from './IntegrationStep.svelte';
import LocationLanguageStep from './LocationLanguageStep.svelte';
import { hasUnsavedChanges, settingsActions, settingsStore } from '$lib/stores/settings';
import { settingsAPI } from '$lib/utils/settingsApi.js';
import { flushAsync, renderStep } from './stepTestUtils';

/** A server response with every section the wizard patches, and nested values it must not lose. */
const serverSettings = () =>
  ({
    main: { name: 'TestNode' },
    birdnet: {
      threshold: 0.8,
      latitude: 40,
      longitude: -74,
      locale: 'en',
      locationConfigured: false,
      rangeFilter: { threshold: 0.03, passUnmappedSpecies: false, speciesCount: null, species: [] },
    },
    realtime: {
      dashboard: { summaryLimit: 100, locale: 'en' },
      audio: {
        source: '',
        sources: [{ name: 'Card', device: 'hw:0' }],
        equalizer: { enabled: true, filters: [] },
      },
      rtsp: { streams: [], health: { healthyDataThreshold: 60 }, ffmpegParameters: ['-x'] },
      privacyFilter: { enabled: true, confidence: 0.7, debug: true },
      birdweather: { enabled: false, id: '', threshold: 0.9 },
    },
    sentry: { enabled: false },
  }) as unknown as SettingsFormData;

function lookup(root: unknown, path: string[]): unknown {
  let current: unknown = root;
  for (const segment of path) {
    if (current === null || typeof current !== 'object') return undefined;
    const record = current as Record<string, unknown>;
    // eslint-disable-next-line security/detect-object-injection -- test helper with fixed paths
    current = Object.hasOwn(record, segment) ? record[segment] : undefined;
  }
  return current;
}

const patchCalls = () => vi.mocked(settingsAPI.patchSection).mock.calls as unknown[][];

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
      screen.getByRole('button', { name: /wizard\.steps\.integration\.privacyFilterLabel/ })
    );
    await fireEvent.click(
      screen.getByRole('button', { name: /wizard\.steps\.integration\.errorReportingLabel/ })
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

  it('AudioSourceStep patches rtsp with one enabled stream and keeps health', async () => {
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
            {
              name: 'Stream 1',
              url: 'rtsp://camera.example/stream',
              enabled: true,
              type: 'rtsp',
              transport: 'tcp',
            },
          ],
        },
      ],
    ]);
    expect(settingsAPI.save).not.toHaveBeenCalled();
    expect(get(hasUnsavedChanges)).toBe(false);
    const { formData } = get(settingsStore);
    expect(formData.realtime?.rtsp?.health).toEqual({ healthyDataThreshold: 60 });
    expect(formData.realtime?.audio?.sources).toEqual([{ name: 'Card', device: 'hw:0' }]);
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
