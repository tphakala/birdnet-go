/**
 * Selection playback and the clip extraction listeners in AudioPlayer.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { screen, fireEvent, cleanup, render } from '@testing-library/svelte';
import {
  installFakeMediaElement,
  fakeAudioNodeChain,
  settle,
  type FakeMediaController,
} from '../../../../test/fakeMediaElement';
import { attachAudioGraphWhenRunning } from '$lib/utils/audioNodes';
import { getAudioContext } from '$lib/utils/audioContextManager';
import { AUDIO_RETRY_DELAY_MS, dbToGain } from '$lib/utils/audio';
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

const PLAY_LABEL = 'media.audio.play';
const PLAY_ERROR_TEXT = 'media.audio.playError';
const SETTINGS_LABEL = 'media.audio.settings';
const GAIN_LABEL = 'components.audioPlayer.processing.gain';
const SKIP_TO_START_LABEL = 'components.audioPlayer.processing.skipToStart';
const WIDE_CONTAINER_PX = 400;
const SELECTION_START_PX = 40;
const SELECTION_END_PX = 200;
const RAISED_GAIN_DB = 12;
const LATER_GAIN_DB = 6;
const MS_PER_SECOND = 1000;

describe('AudioPlayer selection play', () => {
  let ctl: FakeMediaController;
  const mockedAttach = vi.mocked(attachAudioGraphWhenRunning);
  const originalRect = Element.prototype.getBoundingClientRect;
  const originalOffsetWidth = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetWidth');

  function mount(props: Record<string, unknown> = {}) {
    return render(AudioPlayer, {
      props: {
        audioUrl: '/audio/test.mp3',
        detectionId: 'test-123',
        enableClipExtraction: true,
        showSpectrogram: false,
        ...props,
      },
    });
  }

  function player(container: HTMLElement): HTMLElement {
    const el = container.firstElementChild;
    if (!(el instanceof HTMLElement)) throw new Error('player container missing');
    return el;
  }

  async function startDrag(container: HTMLElement) {
    await fireEvent.mouseDown(player(container), { button: 0, clientX: SELECTION_START_PX });
    await fireEvent.mouseMove(document, { buttons: 1, clientX: SELECTION_END_PX });
  }

  async function dragSelection(container: HTMLElement) {
    await startDrag(container);
    await fireEvent.mouseUp(document, { clientX: SELECTION_END_PX });
    // Without a selection the toolbar would start a plain play instead.
    expect(screen.getByLabelText(SKIP_TO_START_LABEL)).toBeEnabled();
  }

  async function press() {
    await fireEvent.click(screen.getByLabelText(PLAY_LABEL));
    await settle();
  }

  async function setGain(db: number) {
    await fireEvent.input(screen.getByLabelText(GAIN_LABEL), { target: { value: String(db) } });
  }

  // Holds the next audio context start until the returned function is called.
  function holdContextStart(): () => void {
    let resolveContext: (ctx: AudioContext) => void = () => {};
    vi.mocked(getAudioContext).mockReturnValueOnce(
      new Promise<AudioContext>(resolve => {
        resolveContext = resolve;
      })
    );
    // Only the state is read from the context in these tests.
    return () => resolveContext({ state: 'running' } as unknown as AudioContext);
  }

  beforeEach(() => {
    vi.useFakeTimers();
    ctl = installFakeMediaElement();
    mockedAttach.mockReset();
    mockedAttach.mockImplementation(() => fakeAudioNodeChain());
    Element.prototype.getBoundingClientRect = () =>
      new DOMRect(0, 0, WIDE_CONTAINER_PX, WIDE_CONTAINER_PX);
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
    Element.prototype.getBoundingClientRect = originalRect;
    if (originalOffsetWidth) {
      Object.defineProperty(HTMLElement.prototype, 'offsetWidth', originalOffsetWidth);
    }
  });

  it('builds the audio graph with the current gain when the first play is a selection', async () => {
    const chain = fakeAudioNodeChain();
    mockedAttach.mockImplementation(() => chain);
    const { container } = mount();
    ctl.completeLoad();
    await settle();
    await setGain(RAISED_GAIN_DB);
    await dragSelection(container);

    await press();

    expect(mockedAttach).toHaveBeenCalledWith(
      expect.anything(),
      ctl.element,
      null,
      expect.objectContaining({ gainDb: RAISED_GAIN_DB, includeCompressor: true })
    );
    await setGain(LATER_GAIN_DB);
    expect(chain.gain.gain.value).toBeCloseTo(dbToGain(LATER_GAIN_DB));
  });

  it('plays only the selection and reuses the graph when it follows a full play', async () => {
    const { container } = mount();
    ctl.completeLoad();
    await settle();
    await press();
    expect(mockedAttach).toHaveBeenCalledTimes(1);
    ctl.element.pause();
    await dragSelection(container);

    await press();

    expect(ctl.paused).toBe(false);
    expect(mockedAttach).toHaveBeenCalledTimes(1);
    // Only a selection play seeks to the selection start.
    const selectionStartSec = (SELECTION_START_PX / WIDE_CONTAINER_PX) * ctl.element.duration;
    expect(ctl.element.currentTime).toBeCloseTo(selectionStartSec);

    // The selection ends on its own timer, not at the end of the clip.
    const selectionLengthMs =
      ((SELECTION_END_PX - SELECTION_START_PX) / WIDE_CONTAINER_PX) *
      ctl.element.duration *
      MS_PER_SECOND;
    await vi.advanceTimersByTimeAsync(selectionLengthMs);
    expect(ctl.paused).toBe(true);
  });

  it('disables the audio settings when the graph cannot attach on a selection play', async () => {
    mockedAttach.mockImplementation(() => null);
    const { container } = mount();
    ctl.completeLoad();
    await settle();
    await dragSelection(container);

    await press();

    expect(ctl.paused).toBe(false);
    expect(screen.getByRole('button', { name: SETTINGS_LABEL })).toBeDisabled();
  });

  it('shows the play error when a selection play is rejected', async () => {
    const { container } = mount();
    ctl.completeLoad();
    await settle();
    await dragSelection(container);
    ctl.rejectNextPlay('NotAllowedError');

    await press();

    expect(screen.getByText(PLAY_ERROR_TEXT)).toBeInTheDocument();
    expect(screen.getByLabelText(PLAY_LABEL)).toBeInTheDocument();
  });

  it.each([
    ['the play button', false],
    ['a selection play', true],
  ])(
    'does not attach the graph or play when unmounted while the context starts for %s',
    async (_name, withSelection) => {
      const start = holdContextStart();
      const { container, unmount } = mount();
      ctl.completeLoad();
      await settle();
      if (withSelection) await dragSelection(container);
      await fireEvent.click(screen.getByLabelText(PLAY_LABEL));

      unmount();
      start();
      await settle();

      expect(mockedAttach).not.toHaveBeenCalled();
      expect(ctl.play).not.toHaveBeenCalled();
    }
  );

  it('does not resume the whole clip when the load of a selection play is retried', async () => {
    const { container } = mount();
    ctl.completeLoad();
    await settle();
    await dragSelection(container);
    ctl.setPlayMode('hold');
    await press();

    ctl.failLoad();
    await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS);
    ctl.completeLoad();
    await settle();

    expect(ctl.paused).toBe(true);
    expect(screen.queryByText(PLAY_ERROR_TEXT)).not.toBeInTheDocument();
  });

  it('does not play a selection that was cleared while the context starts', async () => {
    const start = holdContextStart();
    const { container } = mount();
    ctl.completeLoad();
    await settle();
    await dragSelection(container);
    await fireEvent.click(screen.getByLabelText(PLAY_LABEL));

    await fireEvent.keyDown(document, { key: 'Escape' });
    start();
    await settle();

    expect(ctl.play).not.toHaveBeenCalled();
  });

  it('plays once, with the graph, from the selection start when pressed twice while the context starts', async () => {
    const start = holdContextStart();
    const { container } = mount();
    ctl.completeLoad();
    await settle();
    await dragSelection(container);
    await fireEvent.click(screen.getByLabelText(PLAY_LABEL));
    await fireEvent.click(screen.getByLabelText(PLAY_LABEL));

    start();
    await settle();

    expect(ctl.play).toHaveBeenCalledTimes(1);
    expect(mockedAttach).toHaveBeenCalledTimes(1);
    expect(ctl.element.currentTime).toBeCloseTo(
      (SELECTION_START_PX / WIDE_CONTAINER_PX) * ctl.element.duration
    );
  });

  it('does not play after the selection is stopped while the context starts', async () => {
    const start = holdContextStart();
    const { container } = mount();
    ctl.completeLoad();
    await settle();
    await dragSelection(container);
    await fireEvent.click(screen.getByLabelText(PLAY_LABEL));

    // Skip to start stops the preview and keeps the selection.
    await fireEvent.click(screen.getByLabelText(SKIP_TO_START_LABEL));
    start();
    await settle();

    expect(ctl.play).not.toHaveBeenCalled();
  });

  describe('when audible bats swaps the source while the context starts', () => {
    const BATS_TITLE = 'media.audio.audibleBats.title';
    const BATS_ENABLE_TEXT = 'media.audio.audibleBats.enable';
    const originalFetch = globalThis.fetch;
    const originalCreateObjectURL = URL.createObjectURL;
    const originalRevokeObjectURL = URL.revokeObjectURL;

    beforeEach(() => {
      URL.createObjectURL = vi.fn(() => 'blob:audible-bats-clip');
      URL.revokeObjectURL = vi.fn();
      globalThis.fetch = vi.fn().mockImplementation(async () => ({
        ok: true,
        status: 200,
        blob: async () => new Blob(['bats']),
        headers: new Headers(),
      }));
    });

    afterEach(() => {
      globalThis.fetch = originalFetch;
      URL.createObjectURL = originalCreateObjectURL;
      URL.revokeObjectURL = originalRevokeObjectURL;
    });

    it('does not play the replaced source', async () => {
      const start = holdContextStart();
      mount({ modelType: 'bat' });
      ctl.completeLoad();
      await settle();
      await fireEvent.click(screen.getByLabelText(PLAY_LABEL));

      await fireEvent.click(screen.getByLabelText(BATS_TITLE));
      await fireEvent.click(screen.getByText(BATS_ENABLE_TEXT));
      await settle();
      start();
      await settle();

      expect(ctl.play).not.toHaveBeenCalled();
    });
  });

  it('follows enableClipExtraction switched on after mount: drag selects, Escape clears', async () => {
    const { container, rerender } = mount({ enableClipExtraction: false });
    ctl.completeLoad();
    await settle();

    await rerender({ enableClipExtraction: true });
    await dragSelection(container);

    await fireEvent.keyDown(document, { key: 'Escape' });
    expect(screen.getByLabelText(SKIP_TO_START_LABEL)).toBeDisabled();

    await rerender({ enableClipExtraction: false });
    await rerender({ enableClipExtraction: true });
    await dragSelection(container);
  });

  it('stops blocking text selection when extraction is switched off during a drag', async () => {
    const { container, rerender } = mount();
    ctl.completeLoad();
    await settle();
    await startDrag(container);

    await rerender({ enableClipExtraction: false });

    const selectStart = new Event('selectstart', { cancelable: true });
    document.dispatchEvent(selectStart);
    expect(selectStart.defaultPrevented).toBe(false);
  });
});
