import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { screen, fireEvent, cleanup } from '@testing-library/svelte';
import { renderTyped } from '../../../../../test/render-helpers';
import { get } from 'svelte/store';
import { settingsStore } from '$lib/stores/settings';
import UserInterfaceSettingsPage from './UserInterfaceSettingsPage.svelte';

// Tests run without translations, so the text shown is the key.
const TAB_LABEL = 'settings.userInterface.tabs.audioPlayback';
const GAIN_LABEL = 'settings.userInterface.audioPlayback.defaultGain';
const HIGH_PASS_LABEL = 'settings.userInterface.audioPlayback.highPassLabel';
const HIGH_PASS_OFF = 'settings.userInterface.audioPlayback.highPassOff';
const GAIN_UNIT = 'settings.userInterface.audioPlayback.defaultGainUnit';
const TAB_CHANGED = 'settings.tabs.hasChanges';
const CARD_CHANGED = 'settings.card.changed';

interface DashboardView {
  defaultAudioGain: number;
  playback: { highPassHz: number; [key: string]: unknown };
  [key: string]: unknown;
}

function seedStore(dashboardOverrides: Record<string, unknown> = {}) {
  // A factory, so each copy gets its own default nested objects. The override values are
  // shared by both copies; the page replaces nested objects whole, so none is mutated.
  const buildFormData = () => ({
    realtime: {
      dashboard: {
        thumbnails: { summary: true, recent: true, imageProvider: 'avicommons' },
        summaryLimit: 30,
        defaultAudioGain: 0,
        playbackDenoise: { enabled: false, strength: 'light-medium' },
        playback: { highPassHz: 0 },
        ...dashboardOverrides,
      },
      audio: { export: { type: 'flac', denoise: { enabled: false, strength: 'light-medium' } } },
    },
  });
  // The seeded store holds only the sections the page reads, not a full form type.
  settingsStore.update(
    store =>
      ({
        ...store,
        isLoading: false,
        isSaving: false,
        dataLoaded: true,
        originalData: buildFormData(),
        formData: buildFormData(),
      }) as unknown as typeof store
  );
}

async function renderTab(dashboardOverrides: Record<string, unknown> = {}) {
  seedStore(dashboardOverrides);
  renderTyped(UserInterfaceSettingsPage);
  await fireEvent.click(await screen.findByRole('tab', { name: new RegExp(TAB_LABEL) }));
  const slider = await screen.findByLabelText<HTMLInputElement>(HIGH_PASS_LABEL);
  return slider;
}

// The store's form data is a loose type; read back only the one section under test.
function dashboard(source: 'formData' | 'originalData'): DashboardView {
  const data = get(settingsStore)[source] as unknown as { realtime: { dashboard: DashboardView } };
  return data.realtime.dashboard;
}

describe('UserInterfaceSettingsPage playback defaults sliders', () => {
  const initial = get(settingsStore);

  beforeEach(() => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({ ok: true, json: () => Promise.resolve({ providers: [] }) })
    );
  });

  afterEach(() => {
    cleanup();
    settingsStore.set(initial);
    vi.unstubAllGlobals();
  });

  it('offers the shared gain range and step', async () => {
    await renderTab();
    const gain = await screen.findByLabelText<HTMLInputElement>(GAIN_LABEL);

    expect([gain.min, gain.max, gain.step]).toEqual(['-12', '24', '0.5']);
  });

  it.each([
    { name: 'a positive value', stored: 7.5, text: `+7.5 ${GAIN_UNIT}` },
    { name: 'zero', stored: 0, text: `0 ${GAIN_UNIT}` },
    { name: 'a negative value', stored: -6.5, text: `-6.5 ${GAIN_UNIT}` },
  ])('shows $name with a sign, no trailing zero and a spaced unit', async ({ stored, text }) => {
    await renderTab({ defaultAudioGain: stored });
    const gain = await screen.findByLabelText<HTMLInputElement>(GAIN_LABEL);

    expect(screen.getByText(text)).toBeInTheDocument();
    expect(gain).toHaveAttribute('aria-valuetext', text);
  });

  it('shows an off-grid stored gain at its stepped value and leaves the form clean', async () => {
    await renderTab({ defaultAudioGain: -6.3 });
    const gain = await screen.findByLabelText<HTMLInputElement>(GAIN_LABEL);

    expect(gain.value).toBe('-6.5');
    expect(screen.getByText(`-6.5 ${GAIN_UNIT}`)).toBeInTheDocument();
    expect(dashboard('formData').defaultAudioGain).toBe(-6.3);
    expect(dashboard('originalData').defaultAudioGain).toBe(-6.3);
    expect(screen.queryByText(CARD_CHANGED)).toBeNull();
  });

  it('shows a stored off filter as the floor and labels it Off', async () => {
    const slider = await renderTab();

    expect([slider.min, slider.max, slider.step]).toEqual(['20', '5000', '10']);
    expect(slider.value).toBe('20');
    expect(screen.getByText(HIGH_PASS_OFF)).toBeInTheDocument();
  });

  it('writes the whole playback object to the form and leaves the saved copy alone when the cutoff moves', async () => {
    const slider = await renderTab({ playback: { highPassHz: 0, futureField: 'kept' } });

    await fireEvent.input(slider, { target: { value: '300' } });

    expect(dashboard('formData').playback).toEqual({ highPassHz: 300, futureField: 'kept' });
    expect(dashboard('originalData').playback).toEqual({ highPassHz: 0, futureField: 'kept' });
    expect(
      screen.getByText('300 settings.userInterface.audioPlayback.highPassUnit')
    ).toBeInTheDocument();
  });

  it('marks the tab and the section changed once the cutoff moves, and not before', async () => {
    const slider = await renderTab();
    const tab = screen.getByRole('tab', { name: new RegExp(TAB_LABEL) });
    expect(tab).not.toHaveAccessibleDescription(TAB_CHANGED);
    expect(screen.queryByText(CARD_CHANGED)).toBeNull();

    await fireEvent.input(slider, { target: { value: '300' } });

    expect(tab).toHaveAccessibleDescription(TAB_CHANGED);
    expect(screen.getByText(CARD_CHANGED)).toBeInTheDocument();
  });

  it('stores the floor as 0, not 20', async () => {
    const slider = await renderTab({ playback: { highPassHz: 300 } });

    await fireEvent.input(slider, { target: { value: '20' } });

    expect(dashboard('formData').playback.highPassHz).toBe(0);
  });

  it('leaves the other dashboard values alone when the cutoff moves', async () => {
    const slider = await renderTab({ defaultAudioGain: -6.5 });

    await fireEvent.input(slider, { target: { value: '500' } });

    expect(dashboard('formData').defaultAudioGain).toBe(-6.5);
    expect(dashboard('formData').summaryLimit).toBe(30);
  });
});
