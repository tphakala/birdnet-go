/**
 * The playback defaults dialog in AudioPlayer: the gear is shown to signed-in
 * users only, and after a Save the open player adopts the items that changed and
 * treats them as its defaults (overlay Reset and dot, the GET denoise URL).
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { screen, fireEvent, cleanup, render, within } from '@testing-library/svelte';
import { get } from 'svelte/store';
import {
  installFakeMediaElement,
  fakeAudioNodeChain,
  settle,
  type FakeMediaController,
} from '../../../../test/fakeMediaElement';
import { attachAudioGraphWhenRunning } from '$lib/utils/audioNodes';
import { DENOISE_BUTTON_NAME } from '../../../../test/audioPlayerQueries';
import { auth } from '$lib/stores/auth';
import { settingsStore } from '$lib/stores/settings';
import { seedSavedRealtime } from '../../../../test/settings-store-helpers';
import { deferred } from '../../../../test/async-helpers';
import { mockAudioProbe } from '../../../../test/audioProbeMock';
import { settingsAPI } from '$lib/utils/settingsApi.js';
import { openDialogCount } from '$lib/desktop/components/ui/Modal.svelte';
import AudioPlayer from './AudioPlayer.svelte';

vi.mock('$lib/utils/audioContextManager', () => ({
  getAudioContext: vi.fn().mockResolvedValue({ state: 'running' }),
  isAudioContextSupported: vi.fn().mockReturnValue(true),
  releaseAudioContext: vi.fn(),
}));

vi.mock('$lib/utils/audioNodes', () => ({
  attachAudioGraphWhenRunning: vi.fn(),
  disconnectAudioNodes: vi.fn(),
}));

const DETECTION_ID = '4242';
const STORED_URL = `/api/v2/audio/${DETECTION_ID}`;
const OTHER_URL = '/api/v2/audio/4343';
const GEAR_NAME = 'components.audioPlayer.defaults.open';
const DIALOG_NAME = 'components.audioPlayer.defaults.title';
const SAVE = 'settings.actions.save';
const USE_CURRENT = 'components.audioPlayer.defaults.useCurrent';
const TOOLBAR_GAIN_LABEL = 'components.audioPlayer.processing.gain';
const DIALOG_GAIN_LABEL = 'settings.userInterface.audioPlayback.defaultGain';
const DIALOG_HIGH_PASS_LABEL = 'settings.userInterface.audioPlayback.highPassLabel';
const DIALOG_DENOISE_LABEL = 'settings.userInterface.audioPlayback.denoiseLabel';
const SETTINGS_BUTTON = 'media.audio.settings';
const RESET_BUTTON = 'common.reset';
const PLAY_LABEL = 'media.audio.play';
const DENOISE_LIGHT_TEXT = 'components.audioPlayer.processing.denoiseLight';
const DENOISE_HEAVY_TEXT = 'components.audioPlayer.processing.denoiseHeavy';
const PROCESSING_DEBOUNCE_MS = 300;
const PROCESSED_BLOB_URL = 'blob:processed-clip';
// Wide enough for the overlay controls, which a zero-width jsdom player hides.
const WIDE_CONTAINER_PX = 400;

const initialStore = get(settingsStore);

describe('AudioPlayer playback defaults dialog', () => {
  let ctl: FakeMediaController;
  const originalFetch = globalThis.fetch;
  const originalCreateObjectURL = URL.createObjectURL;
  const originalRevokeObjectURL = URL.revokeObjectURL;
  const originalOffsetWidth = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetWidth');
  const originalPatch = vi.mocked(settingsAPI.patchSection).getMockImplementation();
  let fetchMock: ReturnType<typeof vi.fn>;

  function mount(props: Record<string, unknown> = {}) {
    return render(AudioPlayer, {
      props: {
        audioUrl: STORED_URL,
        detectionId: DETECTION_ID,
        enableClipExtraction: true,
        showSpectrogram: false,
        ...props,
      },
    });
  }

  function elementPath(): string {
    const url = new URL(ctl.element.src);
    return `${url.pathname}${url.search}`;
  }

  function postedUrls(): string[] {
    return fetchMock.mock.calls
      .map(call => String(call[0]))
      .filter(url => url.includes('/audio/') && url.includes('/process'));
  }

  const dialog = () => screen.getByRole('dialog', { name: DIALOG_NAME });

  async function openDialog() {
    await fireEvent.click(screen.getByRole('button', { name: GEAR_NAME }));
    await settle();
    return dialog();
  }

  async function saveDialog() {
    await fireEvent.click(within(dialog()).getByRole('button', { name: SAVE }));
    await settle();
  }

  async function chooseToolbarDenoise(text: string) {
    await fireEvent.click(screen.getByRole('button', { name: DENOISE_BUTTON_NAME }));
    await fireEvent.click(screen.getByText(text));
    await vi.advanceTimersByTimeAsync(PROCESSING_DEBOUNCE_MS);
    await settle();
  }

  async function chooseDialogDenoise(text: string) {
    await fireEvent.click(within(dialog()).getByRole('combobox', { name: DIALOG_DENOISE_LABEL }));
    await fireEvent.click(screen.getByRole('option', { name: text }));
    await settle();
  }

  function overlayGain(container: HTMLElement): HTMLInputElement {
    const [gain] = Array.from(
      container.querySelectorAll<HTMLInputElement>('.settings-menu input[type="range"]')
    );
    return gain;
  }

  function overlayFilter(container: HTMLElement): HTMLInputElement {
    const [, filter] = Array.from(
      container.querySelectorAll<HTMLInputElement>('.settings-menu input[type="range"]')
    );
    return filter;
  }

  beforeEach(() => {
    vi.useFakeTimers();
    settingsStore.set(initialStore);
    seedSavedRealtime({
      dashboard: {
        defaultAudioGain: 0,
        playbackDenoise: { enabled: false, strength: 'medium' },
        playback: { highPassHz: 0 },
      },
    });
    ctl = installFakeMediaElement();
    vi.mocked(attachAudioGraphWhenRunning).mockReset();
    vi.mocked(attachAudioGraphWhenRunning).mockImplementation(() => fakeAudioNodeChain());
    URL.createObjectURL = vi.fn(() => PROCESSED_BLOB_URL);
    URL.revokeObjectURL = vi.fn();
    fetchMock = vi.fn().mockImplementation(async () => ({
      ok: true,
      status: 200,
      blob: async () => new Blob(['processed']),
      headers: new Headers(),
    }));
    // The mock answers only the fields the player reads from a Response.
    globalThis.fetch = fetchMock as unknown as typeof globalThis.fetch;
    Object.defineProperty(HTMLElement.prototype, 'offsetWidth', {
      configurable: true,
      get: () => WIDE_CONTAINER_PX,
    });
  });

  afterEach(() => {
    cleanup();
    vi.clearAllTimers();
    vi.useRealTimers();
    ctl.restore();
    globalThis.fetch = originalFetch;
    URL.createObjectURL = originalCreateObjectURL;
    URL.revokeObjectURL = originalRevokeObjectURL;
    if (originalOffsetWidth) {
      Object.defineProperty(HTMLElement.prototype, 'offsetWidth', originalOffsetWidth);
    }
    vi.mocked(settingsAPI.patchSection).mockReset();
    if (originalPatch) vi.mocked(settingsAPI.patchSection).mockImplementation(originalPatch);
    auth.setSecurity(false, true);
    settingsStore.set(initialStore);
  });

  it('shows no gear to a visitor who is not signed in', async () => {
    auth.setSecurity(true, false);
    mount();
    await settle();

    expect(screen.queryByRole('button', { name: GEAR_NAME })).toBeNull();
  });

  it('opens the dialog from the gear', async () => {
    mount();
    await settle();

    expect(await openDialog()).toBeInTheDocument();
  });

  it('adopts a saved high-pass default and makes it the overlay default, keeping the session gain', async () => {
    const { container } = mount();
    await settle();
    await fireEvent.input(screen.getByLabelText(TOOLBAR_GAIN_LABEL), { target: { value: '6' } });

    await openDialog();
    await fireEvent.input(within(dialog()).getByLabelText(DIALOG_HIGH_PASS_LABEL), {
      target: { value: '300' },
    });
    await saveDialog();

    expect(settingsAPI.patchSection).toHaveBeenCalledWith('dashboard', {
      playback: { highPassHz: 300 },
    });
    expect(screen.getByLabelText<HTMLInputElement>(TOOLBAR_GAIN_LABEL).value).toBe('6');
    await fireEvent.click(screen.getByRole('button', { name: SETTINGS_BUTTON }));
    expect(overlayFilter(container).value).toBe('300');

    await fireEvent.input(overlayFilter(container), { target: { value: '1000' } });
    await fireEvent.click(screen.getByRole('button', { name: RESET_BUTTON }));
    expect(overlayFilter(container).value).toBe('300');
  });

  it('adopts a saved gain default as the toolbar gain and the overlay Reset target', async () => {
    const { container } = mount();
    await settle();

    await openDialog();
    await fireEvent.input(within(dialog()).getByLabelText(DIALOG_GAIN_LABEL), {
      target: { value: '9' },
    });
    await saveDialog();

    expect(screen.getByLabelText<HTMLInputElement>(TOOLBAR_GAIN_LABEL).value).toBe('9');
    await fireEvent.click(screen.getByRole('button', { name: SETTINGS_BUTTON }));
    await fireEvent.input(overlayGain(container), { target: { value: '15' } });
    await fireEvent.click(screen.getByRole('button', { name: RESET_BUTTON }));
    expect(overlayGain(container).value).toBe('9');
  });

  it('plays a newly saved denoise default after the previous default clip failed', async () => {
    const SERVER_ERROR_STATUS = 500;
    seedSavedRealtime({ dashboard: { playbackDenoise: { enabled: true, strength: 'light' } } });
    const restoreFetch = mockAudioProbe({ status: SERVER_ERROR_STATUS });
    try {
      mount();
      ctl.completeLoad();
      await fireEvent.click(screen.getByLabelText(PLAY_LABEL));
      await settle();
      expect(elementPath()).toBe(`${STORED_URL}?denoise=light`);
      ctl.failLoad();
      await settle();
      expect(elementPath()).toBe(STORED_URL);

      await openDialog();
      await chooseDialogDenoise(DENOISE_HEAVY_TEXT);
      await saveDialog();
      await vi.advanceTimersByTimeAsync(PROCESSING_DEBOUNCE_MS);
      await settle();

      expect(elementPath()).toBe(`${STORED_URL}?denoise=heavy`);
    } finally {
      restoreFetch();
    }
  });

  it('does not reprocess when the saved denoise is what the player already plays', async () => {
    mount();
    ctl.completeLoad();
    await settle();
    await chooseToolbarDenoise(DENOISE_HEAVY_TEXT);
    expect(postedUrls()).toHaveLength(1);

    await openDialog();
    await fireEvent.click(within(dialog()).getByRole('button', { name: USE_CURRENT }));
    await saveDialog();
    await vi.advanceTimersByTimeAsync(PROCESSING_DEBOUNCE_MS);
    await settle();

    expect(settingsAPI.patchSection).toHaveBeenCalledWith('dashboard', {
      playbackDenoise: { enabled: true, strength: 'heavy' },
    });
    expect(postedUrls()).toHaveLength(1);
    expect(ctl.element.src).toBe(PROCESSED_BLOB_URL);
  });

  it('plays the GET denoise URL of a newly saved denoise default, without /process', async () => {
    mount();
    ctl.completeLoad();
    await fireEvent.click(screen.getByLabelText(PLAY_LABEL));
    await settle();
    expect(elementPath()).toBe(STORED_URL);

    await openDialog();
    await chooseDialogDenoise(DENOISE_LIGHT_TEXT);
    await saveDialog();
    await vi.advanceTimersByTimeAsync(PROCESSING_DEBOUNCE_MS);
    await settle();

    expect(elementPath()).toBe(`${STORED_URL}?denoise=light`);
    expect(postedUrls()).toHaveLength(0);
  });

  it('drops a processing request still in flight when a saved default changes denoise', async () => {
    const signals: AbortSignal[] = [];
    const first = deferred<unknown>();
    fetchMock.mockImplementation((_url: string, init?: RequestInit) => {
      if (init?.signal) signals.push(init.signal);
      return first.promise;
    });
    mount();
    ctl.completeLoad();
    await settle();
    await chooseToolbarDenoise(DENOISE_HEAVY_TEXT);
    expect(signals).toHaveLength(1);

    await openDialog();
    await chooseDialogDenoise(DENOISE_LIGHT_TEXT);
    await saveDialog();

    expect(signals[0].aborted).toBe(true);
    first.resolve({
      ok: true,
      status: 200,
      blob: async () => new Blob(['stale']),
      headers: new Headers(),
    });
    await vi.advanceTimersByTimeAsync(PROCESSING_DEBOUNCE_MS);
    await settle();
    expect(ctl.element.src).not.toBe(PROCESSED_BLOB_URL);
  });

  it('keeps the dialog closed when the toolbar goes away and comes back', async () => {
    const view = mount();
    await settle();
    await openDialog();

    await view.rerender({ enableClipExtraction: false });
    await settle();
    await view.rerender({ enableClipExtraction: true });
    await settle();

    expect(openDialogCount()).toBe(0);
  });

  it('closes the dialog when the user is signed out', async () => {
    mount();
    await settle();
    await openDialog();

    auth.setSecurity(true, false);
    await settle();

    expect(openDialogCount()).toBe(0);
  });

  it('closes the dialog when the player moves to another clip', async () => {
    const view = mount();
    await settle();
    await openDialog();
    expect(openDialogCount()).toBe(1);

    await view.rerender({ audioUrl: OTHER_URL, detectionId: '4343' });
    await settle();

    expect(openDialogCount()).toBe(0);
  });
});
