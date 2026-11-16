/**
 * StreamManager on the immediately saved streams tab: UI-only row ids key the
 * cards and their open state, add and delete save the whole array at once, and
 * the model list stays live while mounted.
 */
import { tick } from 'svelte';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/svelte';
import { deferred } from '../../../../test/async-helpers';
import { createComponentTestFactory, renderTyped } from '../../../../test/render-helpers';
import {
  restoreElementAnimate,
  stubElementAnimate,
} from '../../../../test/fixtures/elementAnimate';
import type { ChannelAnalysis, StreamConfig } from '$lib/stores/settings';
import { STREAMS_LIST_FIELD } from './sourceFields';
import {
  autoSaveStatus,
  commitSetting,
  resetAutoSaveForTest,
  type CommitOptions,
} from '$lib/stores/settingsAutoSave.svelte';

vi.mock('./SelectDropdown.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockSelectDropdown.svelte')).default,
}));
vi.mock('./TextInput.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockTextInput.svelte')).default,
}));
vi.mock('./StreamTestButton.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockStreamTestButton.svelte')).default,
}));
vi.mock('./ModelToggleList.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockModelToggleList.svelte')).default,
}));
vi.mock('./QuietHoursEditor.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockEmpty.svelte')).default,
}));
vi.mock('./StreamChannelControls.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockStreamChannelControls.svelte')).default,
}));
vi.mock('./streamOptions', async importOriginal => ({
  ...(await importOriginal<typeof import('./streamOptions')>()),
  analyzeStreamChannels: vi.fn(),
}));
vi.mock('$lib/desktop/features/settings/components/AudioEqualizerSettings.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockAudioEqualizerSettings.svelte')).default,
}));

vi.mock('$lib/stores/models.svelte', () => ({
  getAvailableModels: vi.fn(() => []),
  DEFAULT_MODEL_ID: 'birdnet',
  watchModels: vi.fn(() => () => {}),
  modelsLoading: vi.fn(() => false),
  modelsFailed: vi.fn(() => false),
  refreshModels: vi.fn(),
}));

vi.mock('$lib/stores/acousticModels.svelte', () => ({
  subscribeAcousticModels: vi.fn(() => () => {}),
  acousticModelAvailability: vi.fn(() => ({ kind: 'unknown' })),
}));

vi.mock('$lib/stores/quietHours.svelte', () => ({
  quietHoursStore: { startPolling: vi.fn(), stopPolling: vi.fn(), status: null },
}));

vi.mock('$lib/utils/api', async importOriginal => ({
  ...(await importOriginal<typeof import('$lib/utils/api')>()),
  api: { get: vi.fn(() => Promise.resolve([])) },
}));

const store = vi.hoisted(() => ({
  stageSectionEdit: vi.fn(),
  saveStagedSection: vi.fn(),
  onStagedEditsDropped: vi.fn(() => () => undefined),
}));

// The add outcome tests run the real auto-save engine over a mocked store.
vi.mock('$lib/stores/settings', async importOriginal => {
  const actual = await importOriginal<typeof import('$lib/stores/settings')>();
  return { ...actual, settingsActions: { ...actual.settingsActions, ...store } };
});

vi.mock('$lib/utils/ReconnectingEventSource', () => ({
  ReconnectingEventSource: class ReconnectingEventSource {
    addEventListener(): void {}
    close(): void {}
  },
}));

import { watchModels } from '$lib/stores/models.svelte';
import StreamManager from './StreamManager.svelte';
import { analyzeStreamChannels } from './streamOptions';

const ADD_KEY = 'settings.audio.streams.addStream';
const LEFT_ANALYSIS: ChannelAnalysis = { channels: 2, energy: [], recommended: 'left' };

const streamA: StreamConfig = {
  name: 'A',
  url: 'rtsp://a/stream',
  enabled: true,
  type: 'rtsp',
  models: [],
};
const streamB: StreamConfig = { ...streamA, name: 'B', url: 'rtsp://b/stream' };
const streamC: StreamConfig = { ...streamA, name: 'C', url: 'rtsp://c/stream' };

function renderRows(streams: StreamConfig[]) {
  const onUpdateStreams = vi.fn();
  const view = createComponentTestFactory(StreamManager, {
    streams,
    disabled: false,
    onUpdateStreams,
  }).render();
  return { ...view, onUpdateStreams };
}

function header(name: string): HTMLElement {
  // The header button is named by the stream name, its status and its URL.
  return screen.getByRole('button', { name: accessible => accessible.startsWith(`${name} `) });
}

describe('StreamManager', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    stubElementAnimate();
  });

  afterEach(() => {
    cleanup();
    restoreElementAnimate();
  });

  it('keeps the model list live while mounted, through watchModels', () => {
    renderTyped(StreamManager, {
      props: { streams: [], disabled: false, onUpdateStreams: vi.fn() },
    });

    expect(watchModels).toHaveBeenCalledTimes(1);
  });

  it('opens a single stream and keeps several closed', () => {
    renderRows([streamA]);
    expect(header('A')).toHaveAttribute('aria-expanded', 'true');
    cleanup();

    renderRows([streamA, streamB]);
    expect(header('A')).toHaveAttribute('aria-expanded', 'false');
    expect(header('B')).toHaveAttribute('aria-expanded', 'false');
  });

  it('keeps a card open when its own rename or URL apply comes back as the list', async () => {
    const { rerender, onUpdateStreams } = renderRows([streamA, streamB]);
    await fireEvent.click(header('B'));

    const name = screen.getByLabelText<HTMLInputElement>(/settings\.audio\.streams\.nameLabel/);
    await fireEvent.input(name, { target: { value: 'B2' } });
    await fireEvent.blur(name);
    const renamed = onUpdateStreams.mock.calls[0][0];
    expect(renamed.map((s: StreamConfig) => s.name)).toEqual(['A', 'B2']);
    await rerender({ streams: renamed });
    expect(header('B2')).toHaveAttribute('aria-expanded', 'true');

    // A refused rename takes the list back to the old name; the card stays too.
    await rerender({ streams: [streamA, streamB] });
    expect(header('B')).toHaveAttribute('aria-expanded', 'true');

    await fireEvent.input(screen.getByTestId(/-url$/), {
      target: { value: 'rtsp://b/other' },
    });
    await fireEvent.click(screen.getAllByTestId('stream-test-button')[0]);
    await fireEvent.click(
      screen.getByRole('button', { name: 'settings.audio.streams.applyConnection' })
    );
    const reconnected = onUpdateStreams.mock.calls.at(-1)?.[0];
    expect(reconnected?.[1].url).toBe('rtsp://b/other');
    await rerender({ streams: reconnected });
    expect(header('B')).toHaveAttribute('aria-expanded', 'true');
  });

  it('gives every row a fresh id when other streams replace the list at the same length', async () => {
    const { rerender } = renderRows([streamA, streamB]);
    await fireEvent.click(header('B'));
    await fireEvent.input(screen.getByTestId(/-url$/), {
      target: { value: 'rtsp://b/draft' },
    });
    expect(
      screen.getByRole('button', { name: 'settings.audio.streams.applyConnection' })
    ).toBeInTheDocument();

    await rerender({ streams: [streamA, streamC] });

    expect(header('C')).toHaveAttribute('aria-expanded', 'false');
    expect(
      screen.queryByRole('button', { name: 'settings.audio.streams.applyConnection' })
    ).toBeNull();
  });

  it('gives every row a fresh id when the list length changes from outside', async () => {
    const { rerender } = renderRows([streamA, streamB]);
    await fireEvent.click(header('B'));

    await rerender({ streams: [streamA, streamB, streamC] });
    expect(header('B')).toHaveAttribute('aria-expanded', 'false');
  });

  it('gives fresh ids again when an outside list swaps the streams and swaps them back', async () => {
    const { rerender } = renderRows([streamA, streamB]);

    await rerender({ streams: [streamB, streamA] });
    await fireEvent.click(header('B'));
    await fireEvent.input(screen.getByTestId(/-url$/), { target: { value: 'rtsp://b/draft' } });
    expect(
      screen.getByRole('button', { name: 'settings.audio.streams.applyConnection' })
    ).toBeInTheDocument();

    await rerender({ streams: [streamA, streamB] });

    expect(header('A')).toHaveAttribute('aria-expanded', 'false');
    expect(header('B')).toHaveAttribute('aria-expanded', 'false');
    expect(
      screen.queryByRole('button', { name: 'settings.audio.streams.applyConnection' })
    ).toBeNull();
  });

  it('keeps the rows when the same outside list arrives again', async () => {
    const streamP = { ...streamA, name: 'P', url: 'rtsp://p/live' };
    const streamQ = { ...streamA, name: 'Q', url: 'rtsp://q/live' };
    const { rerender } = renderRows([streamA, streamB]);

    await rerender({ streams: [streamP, streamQ] });
    await fireEvent.click(header('Q'));
    await rerender({ streams: [{ ...streamP }, { ...streamQ }] });

    expect(header('Q')).toHaveAttribute('aria-expanded', 'true');
  });

  it('keeps the rows when an identical echo follows the first load replacing the initial list', async () => {
    const { rerender } = renderRows([]);

    await rerender({ streams: [streamA, streamB] });
    await fireEvent.click(header('B'));
    await rerender({ streams: [{ ...streamA }, { ...streamB }] });

    expect(header('B')).toHaveAttribute('aria-expanded', 'true');
  });

  it('gives fresh ids to an outside list at the length of a pending add baseline', async () => {
    const streamP = { ...streamA, name: 'P', url: 'rtsp://p/live' };
    const streamQ = { ...streamA, name: 'Q', url: 'rtsp://q/live' };
    const { rerender } = renderRows([streamA, streamB]);
    await fireEvent.click(header('B'));

    await fireEvent.click(screen.getByRole('button', { name: ADD_KEY }));
    await fireEvent.input(screen.getByTestId('new-stream-name'), { target: { value: 'C' } });
    await fireEvent.input(screen.getByTestId('new-stream-url'), {
      target: { value: 'rtsp://c/stream' },
    });
    const testButton = screen.getAllByTestId('stream-test-button').at(-1);
    if (testButton) await fireEvent.click(testButton);
    await fireEvent.click(screen.getByRole('button', { name: ADD_KEY }));
    await rerender({ streams: [streamA, streamB, streamC] });

    // Not the list before the add: another window replaced it. The add's
    // baseline must not give its open card to the second stream.
    await rerender({ streams: [streamP, streamQ] });

    expect(header('Q')).toHaveAttribute('aria-expanded', 'false');
  });

  it('saves a change of one stream as the whole array under that row field', async () => {
    const { onUpdateStreams } = renderRows([streamA, streamB]);

    const switches = screen.getAllByRole('switch', { name: 'settings.audio.streams.enabled' });
    await fireEvent.click(switches[1]);

    expect(onUpdateStreams).toHaveBeenCalledOnce();
    const [saved, options] = onUpdateStreams.mock.calls[0];
    expect(saved).toEqual([streamA, { ...streamB, enabled: false }]);
    expect(options.field).toMatch(/^rtsp:streams:.+:enabled$/);
  });

  it('adds a tested stream with one list save, and blocks Add until the URL is tested', async () => {
    const { onUpdateStreams } = renderRows([]);

    await fireEvent.click(screen.getByRole('button', { name: ADD_KEY }));
    await fireEvent.input(screen.getByTestId('new-stream-name'), { target: { value: 'Gate' } });
    await fireEvent.input(screen.getByTestId('new-stream-url'), {
      target: { value: 'rtsp://gate/live' },
    });

    const add = screen.getByRole('button', { name: ADD_KEY });
    expect(add).toHaveAttribute('aria-disabled', 'true');
    await fireEvent.click(add);
    expect(onUpdateStreams).not.toHaveBeenCalled();

    await fireEvent.click(screen.getByTestId('stream-test-button'));
    expect(add).not.toHaveAttribute('aria-disabled');
    await fireEvent.click(add);

    expect(onUpdateStreams).toHaveBeenCalledOnce();
    const [saved, options] = onUpdateStreams.mock.calls[0];
    expect(options).toEqual({ field: STREAMS_LIST_FIELD });
    expect(saved).toHaveLength(1);
    expect(saved[0]).toMatchObject({ name: 'Gate', url: 'rtsp://gate/live', enabled: true });
  });

  it('keeps the open state of every card when a refused add takes the list back', async () => {
    const { rerender } = renderRows([streamA, streamB]);
    await fireEvent.click(header('B'));

    await fireEvent.click(screen.getByRole('button', { name: ADD_KEY }));
    await fireEvent.input(screen.getByTestId('new-stream-name'), { target: { value: 'C' } });
    await fireEvent.input(screen.getByTestId('new-stream-url'), {
      target: { value: 'rtsp://c/stream' },
    });
    // The open card has a test button of its own; the add form comes last.
    const testButton = screen.getAllByTestId('stream-test-button').at(-1);
    if (testButton) await fireEvent.click(testButton);
    await fireEvent.click(screen.getByRole('button', { name: ADD_KEY }));

    // The list grows at once, then the refusal takes it back.
    await rerender({ streams: [streamA, streamB, streamC] });
    await rerender({ streams: [streamA, streamB] });

    expect(header('A')).toHaveAttribute('aria-expanded', 'false');
    expect(header('B')).toHaveAttribute('aria-expanded', 'true');
  });

  it('brings a refused delete back with its open state', async () => {
    const { rerender } = renderRows([streamA, streamB]);
    await fireEvent.click(header('A'));
    await fireEvent.click(header('B'));

    await fireEvent.click(screen.getAllByRole('button', { name: 'common.delete' })[0]);
    const dialog = screen.getByRole('alertdialog');
    const confirm = Array.from(dialog.querySelectorAll('button')).find(
      button => button.textContent.trim() === 'common.delete'
    );
    if (confirm) await fireEvent.click(confirm);

    await rerender({ streams: [streamB] });
    await rerender({ streams: [streamA, streamB] });

    expect(header('A')).toHaveAttribute('aria-expanded', 'true');
    expect(header('B')).toHaveAttribute('aria-expanded', 'true');
  });

  it('restores the rows from before a refused delete that follows an outside length change', async () => {
    const streamP = { ...streamA, name: 'P', url: 'rtsp://p/live' };
    const streamQ = { ...streamA, name: 'Q', url: 'rtsp://q/live' };
    const streamR = { ...streamA, name: 'R', url: 'rtsp://r/live' };
    async function deleteFirstCard() {
      await fireEvent.click(screen.getAllByRole('button', { name: 'common.delete' })[0]);
      const confirm = Array.from(screen.getByRole('alertdialog').querySelectorAll('button')).find(
        button => button.textContent.trim() === 'common.delete'
      );
      expect(confirm).toBeDefined();
      if (confirm) await fireEvent.click(confirm);
    }
    const { rerender } = renderRows([streamA, streamB]);

    // A delete that is never answered leaves its baseline behind.
    await deleteFirstCard();
    await rerender({ streams: [streamB] });

    // Another window replaces the list: every row gets a fresh id and the old
    // baseline must not survive to be restored onto these streams.
    await rerender({ streams: [streamP, streamQ, streamR] });
    await fireEvent.click(header('Q'));

    await deleteFirstCard();
    await rerender({ streams: [streamQ, streamR] });
    await rerender({ streams: [streamP, streamQ, streamR] });

    expect(header('Q')).toHaveAttribute('aria-expanded', 'true');
  });

  it('deletes a stream after confirmation with one list save', async () => {
    const { onUpdateStreams } = renderRows([streamA, streamB]);

    await fireEvent.click(screen.getAllByRole('button', { name: 'common.delete' })[0]);
    const dialog = screen.getByRole('alertdialog');
    const confirm = Array.from(dialog.querySelectorAll('button')).find(
      button => button.textContent.trim() === 'common.delete'
    );
    if (confirm) await fireEvent.click(confirm);

    expect(onUpdateStreams).toHaveBeenCalledExactlyOnceWith([streamB], {
      field: STREAMS_LIST_FIELD,
    });
  });
});

describe('StreamManager add outcome', () => {
  const NAME_TESTID = 'new-stream-name';
  const SAVED = { saved: [['streams']], dropped: [], failed: [], remaining: false };
  const REFUSED = {
    saved: [],
    dropped: [],
    failed: [['streams']],
    remaining: false,
    error: new Error('no'),
  };

  beforeEach(() => {
    vi.clearAllMocks();
    resetAutoSaveForTest();
    store.stageSectionEdit.mockReset();
    store.saveStagedSection.mockReset();
    stubElementAnimate();
  });

  afterEach(() => {
    cleanup();
    resetAutoSaveForTest();
    restoreElementAnimate();
  });

  async function addGate() {
    renderTyped(StreamManager, {
      props: {
        streams: [],
        disabled: false,
        onUpdateStreams: (streams: StreamConfig[], options: CommitOptions) =>
          commitSetting('rtsp', { streams }, options),
      },
    });
    await fireEvent.click(screen.getByRole('button', { name: ADD_KEY }));
    await fireEvent.input(screen.getByTestId(NAME_TESTID), { target: { value: 'Gate' } });
    await fireEvent.input(screen.getByTestId('new-stream-url'), {
      target: { value: 'rtsp://gate/live' },
    });
    await fireEvent.click(screen.getByTestId('stream-test-button'));
    await fireEvent.click(screen.getByRole('button', { name: ADD_KEY }));
  }

  it('limits the name of a new stream to what the server accepts', async () => {
    renderTyped(StreamManager, {
      props: { streams: [], disabled: false, onUpdateStreams: vi.fn() },
    });
    await fireEvent.click(screen.getByRole('button', { name: ADD_KEY }));

    expect(screen.getByTestId(NAME_TESTID)).toHaveAttribute('maxlength', '64');
  });

  it('refuses a new name longer than the server limit in bytes and says why', async () => {
    const onUpdateStreams = vi.fn();
    renderTyped(StreamManager, { props: { streams: [], disabled: false, onUpdateStreams } });
    await fireEvent.click(screen.getByRole('button', { name: ADD_KEY }));
    // 22 euro signs are 22 characters, within maxlength, but 66 bytes.
    await fireEvent.input(screen.getByTestId(NAME_TESTID), {
      target: { value: '\u20ac'.repeat(22) },
    });
    await fireEvent.input(screen.getByTestId('new-stream-url'), {
      target: { value: 'rtsp://gate/live' },
    });
    await fireEvent.click(screen.getByTestId('stream-test-button'));
    await fireEvent.click(screen.getByRole('button', { name: ADD_KEY }));

    expect(onUpdateStreams).not.toHaveBeenCalled();
    expect(screen.getByRole('alert')).toHaveTextContent(
      'settings.audio.streams.errors.nameTooLong'
    );
  });

  it('closes the add form once the new stream is saved', async () => {
    store.saveStagedSection.mockResolvedValue(SAVED);

    await addGate();

    await waitFor(() => expect(screen.queryByTestId(NAME_TESTID)).toBeNull());
  });

  it('keeps the add form open with what was typed when the save is refused', async () => {
    store.saveStagedSection.mockResolvedValue(REFUSED);

    await addGate();

    await waitFor(() => expect(autoSaveStatus(STREAMS_LIST_FIELD).state).toBe('error'));
    expect(screen.getByTestId(NAME_TESTID)).toHaveValue('Gate');
  });

  it('shows the busy label and blocks a second add while the new stream is saved', async () => {
    const pending = deferred<typeof SAVED>();
    store.saveStagedSection.mockReturnValueOnce(pending.promise);

    await addGate();

    const busy = await screen.findByRole('button', { name: 'settings.audio.soundCards.adding' });
    expect(busy).toHaveAttribute('aria-disabled', 'true');
    await fireEvent.click(busy);
    expect(store.stageSectionEdit).toHaveBeenCalledOnce();

    pending.resolve(SAVED);
    await waitFor(() => expect(screen.queryByTestId(NAME_TESTID)).toBeNull());
  });

  it('applies a channel analysis of the current URL when Test is clicked again meanwhile', async () => {
    const pending = deferred<ChannelAnalysis>();
    vi.mocked(analyzeStreamChannels).mockReturnValue(pending.promise);
    renderTyped(StreamManager, {
      props: { streams: [], disabled: false, onUpdateStreams: vi.fn() },
    });
    await fireEvent.click(screen.getByRole('button', { name: ADD_KEY }));
    await fireEvent.input(screen.getByTestId('new-stream-url'), {
      target: { value: 'rtsp://gate/live' },
    });

    await fireEvent.click(screen.getByTestId('channel-analyze'));
    // A new test run, or a failed one, reports a cleared result for the same URL.
    await fireEvent.click(screen.getByTestId('stream-test-clear'));
    expect(screen.getByTestId('channel-controls')).toHaveAttribute('data-analyzing', 'true');

    pending.resolve(LEFT_ANALYSIS);
    await pending.promise;
    await tick();
    expect(screen.getByTestId('channel-controls')).toHaveAttribute('data-mode', 'left');
  });

  it('ignores a channel analysis of a URL the add form no longer holds', async () => {
    const pending = deferred<ChannelAnalysis>();
    vi.mocked(analyzeStreamChannels).mockReturnValue(pending.promise);
    renderTyped(StreamManager, {
      props: { streams: [], disabled: false, onUpdateStreams: vi.fn() },
    });
    await fireEvent.click(screen.getByRole('button', { name: ADD_KEY }));
    await fireEvent.input(screen.getByTestId('new-stream-url'), {
      target: { value: 'rtsp://gate/live' },
    });
    await fireEvent.click(screen.getByTestId('channel-analyze'));
    await fireEvent.input(screen.getByTestId('new-stream-url'), {
      target: { value: 'rtsp://other/live' },
    });

    pending.resolve(LEFT_ANALYSIS);
    await pending.promise;
    await tick();
    expect(screen.getByTestId('channel-controls')).toHaveAttribute('data-mode', 'downmix');
  });

  it('points the blocked Cancel at the saving explanation, and only while it is blocked', async () => {
    const pending = deferred<typeof SAVED>();
    store.saveStagedSection.mockReturnValueOnce(pending.promise);
    renderTyped(StreamManager, {
      props: {
        streams: [],
        disabled: false,
        onUpdateStreams: (streams: StreamConfig[], options: CommitOptions) =>
          commitSetting('rtsp', { streams }, options),
      },
    });
    await fireEvent.click(screen.getByRole('button', { name: ADD_KEY }));
    expect(screen.getByRole('button', { name: 'Cancel' })).not.toHaveAttribute('aria-describedby');
    await fireEvent.input(screen.getByTestId(NAME_TESTID), { target: { value: 'Gate' } });
    await fireEvent.input(screen.getByTestId('new-stream-url'), {
      target: { value: 'rtsp://gate/live' },
    });
    await fireEvent.click(screen.getByTestId('stream-test-button'));
    await fireEvent.click(screen.getByRole('button', { name: ADD_KEY }));

    const cancel = await screen.findByRole('button', { name: 'Cancel' });
    expect(cancel).toHaveAttribute('aria-disabled', 'true');
    const explanationId = cancel.getAttribute('aria-describedby');
    expect(explanationId).toBeTruthy();
    expect(document.getElementById(explanationId ?? '')).toHaveTextContent(
      'settings.actions.saving'
    );

    pending.resolve(SAVED);
    await waitFor(() => expect(screen.queryByTestId(NAME_TESTID)).toBeNull());
  });
});
