import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/svelte';
import { get } from 'svelte/store';
import { settingsStore } from '$lib/stores/settings';
import UserInterfaceSettingsPage from './UserInterfaceSettingsPage.svelte';

// Tests run without translations, so the text shown is the key.
const TAB_LABEL = 'settings.userInterface.tabs.audioPlayback';
const ENABLE_LABEL = 'settings.userInterface.audioPlayback.denoiseLabel';
const ENABLE_HELP = 'settings.userInterface.audioPlayback.denoiseHelpText';
const STRENGTH_LABEL = 'settings.userInterface.audioPlayback.denoiseStrengthLabel';
const STRENGTH_DISABLED_HELP = 'settings.userInterface.audioPlayback.denoiseStrengthDisabledHelp';
const EXPORT_PRECEDENCE = 'settings.userInterface.audioPlayback.denoiseExportPrecedence';
const REDENOISE_NOTE = 'settings.userInterface.audioPlayback.denoiseRedenoiseNote';
const LOSSY_NOTE = 'settings.userInterface.audioPlayback.denoiseLossyNote';
const HEAVY_TEXT = 'components.audioPlayer.processing.denoiseHeavy';

interface Denoise {
  enabled: boolean;
  strength: string;
}

/** Seed the real settings store with the sections the page reads. */
function seedStore(options: { playback?: Denoise; exportDenoise?: Denoise; exportType?: string }) {
  const formData = {
    realtime: {
      dashboard: {
        thumbnails: { summary: true, recent: true, imageProvider: 'avicommons' },
        summaryLimit: 30,
        defaultAudioGain: 0,
        playbackDenoise: options.playback ?? { enabled: false, strength: 'light-medium' },
      },
      audio: {
        export: {
          type: options.exportType ?? 'flac',
          denoise: options.exportDenoise ?? { enabled: false, strength: 'light-medium' },
        },
      },
    },
  };
  // The seeded store holds only the sections the page reads, not a full form type.
  settingsStore.update(
    store =>
      ({
        ...store,
        isLoading: false,
        isSaving: false,
        dataLoaded: true,
        originalData: JSON.parse(JSON.stringify(formData)) as typeof formData,
        formData: JSON.parse(JSON.stringify(formData)) as typeof formData,
      }) as unknown as typeof store
  );
}

async function renderAudioPlaybackTab(options: Parameters<typeof seedStore>[0]) {
  seedStore(options);
  render(UserInterfaceSettingsPage);
  await fireEvent.click(await screen.findByRole('tab', { name: new RegExp(TAB_LABEL) }));
  return screen.findByLabelText(ENABLE_LABEL);
}

function playbackDenoise(): unknown {
  // Narrow view of the loose form data to the one field under test.
  const formData = get(settingsStore).formData as unknown as {
    realtime: { dashboard: { playbackDenoise: unknown } };
  };
  return formData.realtime.dashboard.playbackDenoise;
}

describe('UserInterfaceSettingsPage playback denoise', () => {
  const initial = get(settingsStore);

  beforeEach(() => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({ ok: true, json: () => Promise.resolve({ providers: [] }) })
    );
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    settingsStore.set(initial);
  });

  it('writes the whole object when the switch is turned on, keeping the strength', async () => {
    const enable = await renderAudioPlaybackTab({
      playback: { enabled: false, strength: 'medium' },
    });

    await fireEvent.click(enable);

    await waitFor(() => expect(playbackDenoise()).toEqual({ enabled: true, strength: 'medium' }));
  });

  it('writes the chosen strength together with the enabled state', async () => {
    await renderAudioPlaybackTab({ playback: { enabled: true, strength: 'light-medium' } });

    await fireEvent.click(await screen.findByLabelText(STRENGTH_LABEL));
    await fireEvent.click(await screen.findByText(HEAVY_TEXT));

    await waitFor(() => expect(playbackDenoise()).toEqual({ enabled: true, strength: 'heavy' }));
  });

  it('names the reason the strength dropdown is disabled while the switch is off', async () => {
    await renderAudioPlaybackTab({});

    expect(await screen.findByText(STRENGTH_DISABLED_HELP)).toBeTruthy();
    expect(await screen.findByLabelText<HTMLButtonElement>(STRENGTH_LABEL)).toHaveProperty(
      'disabled',
      true
    );
  });

  it('shows the double-denoise note only while the switch is on', async () => {
    await renderAudioPlaybackTab({});
    expect(screen.queryByText(REDENOISE_NOTE)).toBeNull();
    cleanup();

    await renderAudioPlaybackTab({ playback: { enabled: true, strength: 'light-medium' } });
    expect(await screen.findByText(REDENOISE_NOTE)).toBeTruthy();
  });

  it.each([
    ['mp3', true],
    ['aac', true],
    ['opus', true],
    ['flac', false],
    ['wav', false],
  ])('shows the lossy-format note for %s: %s', async (exportType, shown) => {
    await renderAudioPlaybackTab({
      playback: { enabled: true, strength: 'light-medium' },
      exportType,
    });

    await screen.findByText(REDENOISE_NOTE);
    expect(screen.queryByText(LOSSY_NOTE) !== null).toBe(shown);
  });

  describe('while denoising of saved clips is on', () => {
    const exportDenoise = { enabled: true, strength: 'light-medium' };

    it('disables both controls and names the export setting as the reason', async () => {
      const enable = await renderAudioPlaybackTab({
        playback: { enabled: true, strength: 'light-medium' },
        exportDenoise,
      });

      expect((enable as HTMLInputElement).disabled).toBe(true);
      expect((await screen.findByLabelText<HTMLButtonElement>(STRENGTH_LABEL)).disabled).toBe(true);
      expect(screen.getAllByText(EXPORT_PRECEDENCE).length).toBeGreaterThan(0);
      expect(screen.queryByText(ENABLE_HELP)).toBeNull();
      expect(screen.queryByText(REDENOISE_NOTE)).toBeNull();
    });

    it('links the reason to the strength dropdown', async () => {
      await renderAudioPlaybackTab({ exportDenoise });

      const strength = await screen.findByLabelText(STRENGTH_LABEL);
      const described = (strength.getAttribute('aria-describedby') ?? '')
        .split(/\s+/)
        .map(id => document.getElementById(id)?.textContent ?? '')
        .join(' ');
      expect(described).toContain(EXPORT_PRECEDENCE);
    });
  });
});
