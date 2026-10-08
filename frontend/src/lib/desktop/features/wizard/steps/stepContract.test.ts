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
  /** Makes an edit whose section is invalid. Optional: only steps that can be invalid. */
  invalidEdit?: (container: HTMLElement) => Promise<void>;
  /** Sections the leave handler must not send after invalidEdit. */
  invalidSections?: string[];
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
    invalidEdit: async () => {
      await fireEvent.click(
        screen.getByRole('radio', { name: /wizard\.steps\.audioSource\.rtspStream/ })
      );
      const input = await screen.findByPlaceholderText(
        'wizard.steps.audioSource.rtspUrlPlaceholder'
      );
      await fireEvent.input(input, { target: { value: 'camera.example/stream' } });
    },
    invalidSections: ['audio', 'rtsp'],
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
        screen.getByRole('checkbox', { name: /wizard\.steps\.integration\.errorReportingLabel/ })
      );
    },
    invalidEdit: async () => {
      await fireEvent.click(
        screen.getByRole('checkbox', { name: /wizard\.steps\.integration\.birdweatherLabel/ })
      );
      await fireEvent.input(
        await screen.findByLabelText('settings.integration.birdweather.token.label'),
        { target: { value: 'abc' } }
      );
    },
    invalidSections: ['birdweather'],
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
    vi.mocked(settingsActions.saveSection).mockClear().mockResolvedValue(undefined);
    vi.mocked(settingsActions.updateSection).mockClear();
    vi.mocked(settingsActions.saveSettings).mockClear();
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

    expect(settingsActions.saveSection).not.toHaveBeenCalled();
    expect(settingsActions.saveSettings).not.toHaveBeenCalled();
  });

  it('the leave handler does nothing without edits', async () => {
    const { leave } = renderStep(component);
    await flushAsync();

    await leave();

    expect(settingsActions.saveSection).not.toHaveBeenCalled();
    expect(settingsActions.saveSettings).not.toHaveBeenCalled();
  });

  it('the leave handler saves sections only, never the whole form or the store directly', async () => {
    const { leave, container } = renderStep(component);
    await flushAsync();
    await edit(container);

    await leave();

    expect(settingsActions.saveSection).toHaveBeenCalled();
    expect(settingsActions.saveSettings).not.toHaveBeenCalled();
    expect(settingsActions.updateSection).not.toHaveBeenCalled();
  });

  it('rejects when the section save rejects and retries the edits on the next leave call', async () => {
    const failure = new Error('save failed');
    vi.mocked(settingsActions.saveSection).mockRejectedValueOnce(failure);
    const { leave, container } = renderStep(component);
    await flushAsync();
    await edit(container);

    await expect(leave()).rejects.toBe(failure);
    const firstLeaveCalls = vi.mocked(settingsActions.saveSection).mock.calls.length;
    await leave();

    const calls = vi.mocked(settingsActions.saveSection).mock.calls;
    expect(firstLeaveCalls).toBe(1);
    // The retry starts with the section that failed, with the same payload.
    expect(calls.slice(firstLeaveCalls, firstLeaveCalls + 1)).toEqual(calls.slice(0, 1));
  });
});

// Steps that can be invalid: Back runs their leave handler too, so it must skip the invalid part
describe.each(
  stepCases.flatMap(({ name, component, invalidEdit, invalidSections }) =>
    invalidEdit === undefined || invalidSections === undefined
      ? []
      : [{ name, component, invalidEdit, invalidSections }]
  )
)('$name leave handler with an invalid edit', ({ component, invalidEdit, invalidSections }) => {
  beforeEach(() => {
    vi.mocked(settingsActions.saveSection).mockClear().mockResolvedValue(undefined);
  });

  it('the leave handler never sends a section that is invalid', async () => {
    const { leave, container } = renderStep(component);
    await flushAsync();
    await invalidEdit(container);

    await leave();

    const sent = vi.mocked(settingsActions.saveSection).mock.calls.map(([section]) => section);
    for (const section of invalidSections) {
      expect(sent).not.toContain(section);
    }
  });
});
