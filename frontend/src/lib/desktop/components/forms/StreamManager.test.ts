/**
 * StreamManager on the streams tab that saves with the page: UI-only row ids key
 * the cards and their open state, every change goes out as the whole array for
 * the form, health follows the saved streams, and the model list stays live while
 * mounted. The row id rules (which list keeps its cards and which gets fresh ones)
 * are pinned in full below.
 */
import { tick } from 'svelte';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/svelte';
import { deferred } from '../../../../test/async-helpers';
import { createComponentTestFactory, renderTyped } from '../../../../test/render-helpers';
import {
  restoreElementAnimate,
  stubElementAnimate,
} from '../../../../test/fixtures/elementAnimate';
import type { ChannelAnalysis, StreamConfig } from '$lib/stores/settings';

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

const sse = vi.hoisted(() => ({ opened: 0, closed: 0 }));
vi.mock('$lib/utils/ReconnectingEventSource', () => ({
  ReconnectingEventSource: class ReconnectingEventSource {
    constructor() {
      sse.opened++;
    }
    addEventListener(): void {}
    close(): void {
      sse.closed++;
    }
  },
}));

import { watchModels } from '$lib/stores/models.svelte';
import { api } from '$lib/utils/api';
import StreamManager from './StreamManager.svelte';
import { analyzeStreamChannels } from './streamOptions';

const ADD_KEY = 'settings.audio.streams.addStream';
const TEST_REQUIRED_KEY = 'settings.audio.streams.testRequired';
const NAME_TESTID = 'new-stream-name';
const URL_TESTID = 'new-stream-url';
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
const streamP: StreamConfig = { ...streamA, name: 'P', url: 'rtsp://p/live' };
const streamQ: StreamConfig = { ...streamA, name: 'Q', url: 'rtsp://q/live' };

function renderRows(streams: StreamConfig[], extra: Record<string, unknown> = {}) {
  const onUpdateStreams = vi.fn();
  const onUrlTested = vi.fn();
  const view = createComponentTestFactory(StreamManager, {
    streams,
    savedStreams: [],
    testedUrls: new Set<string>(),
    disabled: false,
    onUpdateStreams,
    onUrlTested,
    ...extra,
  }).render();
  return { ...view, onUpdateStreams, onUrlTested };
}

function header(name: string): HTMLElement {
  // The header button is named by the stream name, its status and its URL.
  return screen.getByRole('button', { name: accessible => accessible.startsWith(`${name} `) });
}

/** Sends the list the manager sent last back as the new streams, as the page does. */
async function echoLast(
  view: { rerender: (_props: { streams: StreamConfig[] }) => Promise<void> },
  onUpdateStreams: ReturnType<typeof vi.fn>
): Promise<StreamConfig[]> {
  const sent = onUpdateStreams.mock.calls.at(-1)?.[0] as StreamConfig[];
  await view.rerender({ streams: sent });
  return sent;
}

async function confirmDelete(cardIndex: number) {
  const trigger = screen.getAllByRole('button', { name: 'common.delete' }).at(cardIndex);
  expect(trigger).toBeDefined();
  if (trigger) await fireEvent.click(trigger);
  const dialog = screen.getByRole('alertdialog');
  const confirm = Array.from(dialog.querySelectorAll('button')).find(
    button => button.textContent.trim() === 'common.delete'
  );
  expect(confirm).toBeDefined();
  if (confirm) await fireEvent.click(confirm);
}

async function fillAddForm(name: string, url: string) {
  await fireEvent.click(screen.getByRole('button', { name: ADD_KEY }));
  await fireEvent.input(screen.getByTestId(NAME_TESTID), { target: { value: name } });
  await fireEvent.input(screen.getByTestId(URL_TESTID), { target: { value: url } });
}

/** The test button of the add form: the open card has one of its own, the form's comes last. */
function addFormTestButton(): HTMLElement {
  const button = screen.getAllByTestId('stream-test-button').at(-1);
  if (!button) throw new Error('no test button');
  return button;
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
      props: {
        streams: [],
        savedStreams: [],
        testedUrls: new Set<string>(),
        disabled: false,
        onUpdateStreams: vi.fn(),
        onUrlTested: vi.fn(),
      },
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

  it('writes a change of one stream as the whole array, without touching the others', async () => {
    const { onUpdateStreams } = renderRows([streamA, streamB]);

    const switches = screen.getAllByRole('switch', { name: 'settings.audio.streams.enabled' });
    await fireEvent.click(switches[1]);

    expect(onUpdateStreams).toHaveBeenCalledExactlyOnceWith([
      streamA,
      { ...streamB, enabled: false },
    ]);
  });

  it('deletes a stream after confirmation with one write of the remaining list', async () => {
    const { onUpdateStreams } = renderRows([streamA, streamB]);

    await confirmDelete(0);

    expect(onUpdateStreams).toHaveBeenCalledExactlyOnceWith([streamB]);
  });
});

describe('StreamManager row ids', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    stubElementAnimate();
  });

  afterEach(() => {
    cleanup();
    restoreElementAnimate();
  });

  // A list the manager sent itself and got back keeps every card and its open
  // state; any other list is foreign, and every row gets a fresh id.

  it('keeps a card open while its own name and URL edits come back as the list', async () => {
    const view = renderRows([streamA, streamB]);
    await fireEvent.click(header('B'));

    const name = screen.getByLabelText<HTMLInputElement>(/settings\.audio\.streams\.nameLabel/);
    await fireEvent.input(name, { target: { value: 'B2' } });
    const renamed = await echoLast(view, view.onUpdateStreams);
    expect(renamed.map(s => s.name)).toEqual(['A', 'B2']);
    expect(header('B2')).toHaveAttribute('aria-expanded', 'true');

    await fireEvent.input(screen.getByTestId(/-url$/), { target: { value: 'rtsp://b/other' } });
    const retargeted = await echoLast(view, view.onUpdateStreams);
    expect(retargeted[1].url).toBe('rtsp://b/other');
    expect(header('B2')).toHaveAttribute('aria-expanded', 'true');
  });

  it('keeps the cards when a save returns the sent list with other keys filled in', async () => {
    const view = renderRows([streamA, streamB]);
    await fireEvent.click(header('B'));
    await fireEvent.click(
      screen.getAllByRole('switch', { name: 'settings.audio.streams.enabled' })[0]
    );
    const sent = view.onUpdateStreams.mock.calls.at(-1)?.[0] as StreamConfig[];

    // Same names and URLs; the server added defaults to the rows.
    await view.rerender({
      streams: sent.map((s): StreamConfig => ({ ...s, gain: 0, transport: 'tcp' })),
    });

    expect(header('B')).toHaveAttribute('aria-expanded', 'true');
  });

  it('gives every row a fresh id when a save returns a list with the name or URL trimmed', async () => {
    const view = renderRows([streamA, streamB]);
    await fireEvent.click(header('B'));
    const name = screen.getByLabelText<HTMLInputElement>(/settings\.audio\.streams\.nameLabel/);
    await fireEvent.input(name, { target: { value: ' B2 ' } });
    await echoLast(view, view.onUpdateStreams);
    expect(header('B2')).toHaveAttribute('aria-expanded', 'true');

    // The backend trims the name and the URL before it stores them.
    await view.rerender({ streams: [streamA, { ...streamB, name: 'B2' }] });

    expect(header('B2')).toHaveAttribute('aria-expanded', 'false');
  });

  it('gives every row a fresh id on Reset, and closes the cards of the restored list', async () => {
    const view = renderRows([streamA, streamB]);
    await fireEvent.click(header('B'));
    const name = screen.getByLabelText<HTMLInputElement>(/settings\.audio\.streams\.nameLabel/);
    await fireEvent.input(name, { target: { value: 'B2' } });
    await echoLast(view, view.onUpdateStreams);

    await view.rerender({ streams: [streamA, streamB] });

    expect(header('B')).toHaveAttribute('aria-expanded', 'false');
  });

  it('opens the only card of a list that arrives from outside', async () => {
    const view = renderRows([streamA, streamB]);

    await view.rerender({ streams: [streamP] });

    expect(header('P')).toHaveAttribute('aria-expanded', 'true');
  });

  it('gives every row a fresh id when other streams replace the list at the same length', async () => {
    const view = renderRows([streamA, streamB]);
    await fireEvent.click(header('B'));
    await fireEvent.click(screen.getAllByRole('button', { name: 'common.delete' })[1]);
    expect(screen.getByRole('alertdialog')).toHaveAccessibleDescription('Remove stream "B"?');

    await view.rerender({ streams: [streamA, streamC] });

    expect(header('C')).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByRole('alertdialog')).toBeNull();
  });

  it('gives every row a fresh id when the list length changes from outside', async () => {
    const view = renderRows([streamA, streamB]);
    await fireEvent.click(header('B'));

    await view.rerender({ streams: [streamA, streamB, streamC] });
    expect(header('B')).toHaveAttribute('aria-expanded', 'false');
  });

  it('gives fresh ids again when an outside list swaps the streams and swaps them back', async () => {
    const view = renderRows([streamA, streamB]);

    await view.rerender({ streams: [streamB, streamA] });
    await fireEvent.click(header('B'));
    expect(header('B')).toHaveAttribute('aria-expanded', 'true');

    await view.rerender({ streams: [streamA, streamB] });

    expect(header('A')).toHaveAttribute('aria-expanded', 'false');
    expect(header('B')).toHaveAttribute('aria-expanded', 'false');
  });

  it('keeps the rows when the same outside list arrives again', async () => {
    const view = renderRows([streamA, streamB]);

    await view.rerender({ streams: [streamP, streamQ] });
    await fireEvent.click(header('Q'));
    await view.rerender({ streams: [{ ...streamP }, { ...streamQ }] });

    expect(header('Q')).toHaveAttribute('aria-expanded', 'true');
  });

  it('keeps the rows when an identical echo follows the first load replacing the initial list', async () => {
    const view = renderRows([]);

    await view.rerender({ streams: [streamA, streamB] });
    await fireEvent.click(header('B'));
    await view.rerender({ streams: [{ ...streamA }, { ...streamB }] });

    expect(header('B')).toHaveAttribute('aria-expanded', 'true');
  });

  it('keeps the open cards through an add, and then through a delete above them', async () => {
    const view = renderRows([streamA, streamB]);
    await fireEvent.click(header('B'));

    await fillAddForm('C', 'rtsp://c/stream');
    await fireEvent.click(addFormTestButton());
    await fireEvent.click(screen.getByRole('button', { name: ADD_KEY }));
    await echoLast(view, view.onUpdateStreams);
    expect(header('B')).toHaveAttribute('aria-expanded', 'true');
    expect(header('C')).toHaveAttribute('aria-expanded', 'false');

    await confirmDelete(0);
    await echoLast(view, view.onUpdateStreams);

    expect(screen.queryByRole('button', { name: n => n.startsWith('A ') })).toBeNull();
    expect(header('B')).toHaveAttribute('aria-expanded', 'true');
    expect(header('C')).toHaveAttribute('aria-expanded', 'false');
  });

  it('opens the card of the first stream added to an empty list', async () => {
    const view = renderRows([]);

    await fillAddForm('Gate', 'rtsp://gate/live');
    await fireEvent.click(addFormTestButton());
    await fireEvent.click(screen.getByRole('button', { name: ADD_KEY }));
    await echoLast(view, view.onUpdateStreams);

    expect(header('Gate')).toHaveAttribute('aria-expanded', 'true');
  });

  it('brings a deleted stream back closed when Reset restores the list', async () => {
    const view = renderRows([streamA, streamB]);
    await fireEvent.click(header('A'));
    await fireEvent.click(header('B'));

    await confirmDelete(0);
    await echoLast(view, view.onUpdateStreams);
    await view.rerender({ streams: [streamA, streamB] });

    expect(header('A')).toHaveAttribute('aria-expanded', 'false');
    expect(header('B')).toHaveAttribute('aria-expanded', 'false');
  });

  it('does not move an open delete confirmation to another stream when Reset restores the list', async () => {
    const view = renderRows([streamA, streamB]);

    // Delete A, add C: the list is [B, C], as long as the saved [A, B].
    await confirmDelete(0);
    await echoLast(view, view.onUpdateStreams);
    await fillAddForm('C', 'rtsp://c/stream');
    await fireEvent.click(addFormTestButton());
    await fireEvent.click(screen.getByRole('button', { name: ADD_KEY }));
    await echoLast(view, view.onUpdateStreams);
    await fireEvent.click(screen.getAllByRole('button', { name: 'common.delete' })[1]);
    expect(screen.getByRole('alertdialog')).toHaveAccessibleDescription('Remove stream "C"?');

    await view.rerender({ streams: [streamA, streamB] });

    expect(screen.queryByRole('alertdialog')).toBeNull();
  });
});

describe('StreamManager late writes from a card', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    stubElementAnimate();
  });

  afterEach(() => {
    cleanup();
    restoreElementAnimate();
  });

  async function startAnalysisOnFirstCard(view: ReturnType<typeof renderRows>) {
    const pending = deferred<ChannelAnalysis>();
    vi.mocked(analyzeStreamChannels).mockReturnValue(pending.promise);
    await fireEvent.click(header('A'));
    await fireEvent.click(screen.getByTestId('channel-analyze'));
    view.onUpdateStreams.mockClear();
    return async () => {
      pending.resolve(LEFT_ANALYSIS);
      await pending.promise;
      await tick();
    };
  }

  it('ignores a late channel analysis of a card that was deleted', async () => {
    const view = renderRows([streamA, streamB]);
    const finish = await startAnalysisOnFirstCard(view);

    await confirmDelete(0);
    await echoLast(view, view.onUpdateStreams);
    view.onUpdateStreams.mockClear();
    await finish();

    // The result would otherwise overwrite B, which now sits at A's old index.
    expect(view.onUpdateStreams).not.toHaveBeenCalled();
  });

  it('ignores a late channel analysis of a card that a Reset replaced', async () => {
    const view = renderRows([streamA, streamB]);
    const finish = await startAnalysisOnFirstCard(view);

    await view.rerender({ streams: [streamP, streamQ] });
    await finish();

    expect(view.onUpdateStreams).not.toHaveBeenCalled();
  });

  it('writes the channel analysis of a card that is still there', async () => {
    const view = renderRows([streamA, streamB]);
    const finish = await startAnalysisOnFirstCard(view);

    await finish();

    expect(view.onUpdateStreams).toHaveBeenCalledExactlyOnceWith([
      { ...streamA, channelMode: 'left' },
      streamB,
    ]);
  });
});

describe('StreamManager add form', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    stubElementAnimate();
  });

  afterEach(() => {
    cleanup();
    restoreElementAnimate();
  });

  it('adds a tested stream with one write, reports its URL as tested and closes the form', async () => {
    const { onUpdateStreams, onUrlTested } = renderRows([]);
    await fillAddForm('Gate', 'rtsp://gate/live');

    const add = screen.getByRole('button', { name: ADD_KEY });
    expect(add).toHaveAttribute('aria-disabled', 'true');
    await fireEvent.click(add);
    expect(onUpdateStreams).not.toHaveBeenCalled();

    await fireEvent.click(screen.getByTestId('stream-test-button'));
    expect(add).not.toHaveAttribute('aria-disabled');
    await fireEvent.click(add);

    expect(onUpdateStreams).toHaveBeenCalledOnce();
    const [list] = onUpdateStreams.mock.calls[0];
    expect(list).toHaveLength(1);
    expect(list[0]).toMatchObject({ name: 'Gate', url: 'rtsp://gate/live', enabled: true });
    expect(onUrlTested).toHaveBeenCalledExactlyOnceWith('rtsp://gate/live');
    expect(screen.queryByTestId(NAME_TESTID)).toBeNull();
  });

  it('explains a blocked Add by the missing test and links the reason', async () => {
    renderRows([]);
    await fillAddForm('Gate', 'rtsp://gate/live');

    const add = screen.getByRole('button', { name: ADD_KEY });
    expect(add).toHaveAccessibleDescription(TEST_REQUIRED_KEY);

    await fireEvent.click(screen.getByTestId('stream-test-button'));
    expect(add).not.toHaveAttribute('aria-describedby');
  });

  it('explains a blocked Add while the form is disabled and links the reason', async () => {
    const view = renderRows([], { disabledReason: 'settings.actions.saving' });
    await fillAddForm('Gate', 'rtsp://gate/live');
    await fireEvent.click(screen.getByTestId('stream-test-button'));
    await view.rerender({ streams: [], disabled: true, disabledReason: 'settings.actions.saving' });

    const add = screen.getByRole('button', { name: ADD_KEY });
    expect(add).toHaveAttribute('aria-disabled', 'true');
    expect(add).toHaveAccessibleDescription('settings.actions.saving');
  });

  it('counts a test for the URL that was sent, also when the field changed before the result arrived', async () => {
    const { onUpdateStreams } = renderRows([]);
    await fillAddForm('Gate', 'rtsp://gate/live');

    await fireEvent.click(screen.getByTestId('stream-test-start'));
    await fireEvent.input(screen.getByTestId(URL_TESTID), {
      target: { value: 'rtsp://gate/other' },
    });
    await fireEvent.click(screen.getByTestId('stream-test-finish'));

    // The result belongs to the first URL, so the second still needs a test.
    const add = screen.getByRole('button', { name: ADD_KEY });
    expect(add).toHaveAttribute('aria-disabled', 'true');
    await fireEvent.click(add);
    expect(onUpdateStreams).not.toHaveBeenCalled();

    await fireEvent.input(screen.getByTestId(URL_TESTID), {
      target: { value: 'rtsp://gate/live' },
    });
    expect(add).not.toHaveAttribute('aria-disabled');
  });

  it('refuses a name another stream has, describes the input with why, and clears it when the field is edited', async () => {
    const { onUpdateStreams } = renderRows([streamA]);
    await fillAddForm('a', 'rtsp://gate/live');
    await fireEvent.click(addFormTestButton());

    await fireEvent.click(screen.getByRole('button', { name: ADD_KEY }));

    expect(onUpdateStreams).not.toHaveBeenCalled();
    const name = screen.getByTestId(NAME_TESTID);
    expect(name).toHaveAttribute('aria-invalid', 'true');
    expect(name).toHaveAccessibleDescription('settings.audio.streams.errors.duplicateName');

    await fireEvent.input(name, { target: { value: 'Gate' } });
    expect(name).not.toHaveAttribute('aria-invalid');
  });

  it('refuses a URL another stream has, describes the input with why, and clears it when the field is edited', async () => {
    const { onUpdateStreams } = renderRows([streamA]);
    await fillAddForm('Gate', 'rtsp://a/stream');
    await fireEvent.click(addFormTestButton());

    await fireEvent.click(screen.getByRole('button', { name: ADD_KEY }));

    expect(onUpdateStreams).not.toHaveBeenCalled();
    const url = screen.getByTestId(URL_TESTID);
    expect(url).toHaveAttribute('aria-invalid', 'true');
    expect(url).toHaveAccessibleDescription('settings.audio.streams.errors.duplicate');

    await fireEvent.input(url, { target: { value: 'rtsp://gate/live' } });
    expect(url).not.toHaveAttribute('aria-invalid');
  });

  it('limits the name of a new stream to what the server accepts', async () => {
    renderRows([]);
    await fireEvent.click(screen.getByRole('button', { name: ADD_KEY }));

    expect(screen.getByTestId(NAME_TESTID)).toHaveAttribute('maxlength', '64');
  });

  it('refuses a new name longer than the server limit in bytes and describes the input with why', async () => {
    const { onUpdateStreams } = renderRows([]);
    // 22 euro signs are 22 characters, within maxlength, but 66 bytes.
    await fillAddForm('€'.repeat(22), 'rtsp://gate/live');
    await fireEvent.click(screen.getByTestId('stream-test-button'));
    await fireEvent.click(screen.getByRole('button', { name: ADD_KEY }));

    expect(onUpdateStreams).not.toHaveBeenCalled();
    const name = screen.getByTestId(NAME_TESTID);
    expect(name).toHaveAttribute('aria-invalid', 'true');
    expect(name).toHaveAccessibleDescription('settings.audio.streams.errors.nameTooLong');
    // The reason is shown once, by the input itself, not by a separate alert.
    const alerts = screen.getAllByRole('alert');
    expect(alerts).toHaveLength(1);
    expect(alerts[0]).toHaveTextContent('settings.audio.streams.errors.nameTooLong');
    expect(name.getAttribute('aria-describedby')).toContain(alerts[0].id);

    await fireEvent.input(name, { target: { value: 'Gate' } });
    expect(name).not.toHaveAttribute('aria-invalid');
  });

  it('applies a channel analysis of the current URL when Test is clicked again meanwhile', async () => {
    const pending = deferred<ChannelAnalysis>();
    vi.mocked(analyzeStreamChannels).mockReturnValue(pending.promise);
    renderRows([]);
    await fireEvent.click(screen.getByRole('button', { name: ADD_KEY }));
    await fireEvent.input(screen.getByTestId(URL_TESTID), {
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
    renderRows([]);
    await fireEvent.click(screen.getByRole('button', { name: ADD_KEY }));
    await fireEvent.input(screen.getByTestId(URL_TESTID), {
      target: { value: 'rtsp://gate/live' },
    });
    await fireEvent.click(screen.getByTestId('channel-analyze'));
    await fireEvent.input(screen.getByTestId(URL_TESTID), {
      target: { value: 'rtsp://other/live' },
    });

    pending.resolve(LEFT_ANALYSIS);
    await pending.promise;
    await tick();
    expect(screen.getByTestId('channel-controls')).toHaveAttribute('data-mode', 'downmix');
  });
});

describe('StreamManager blocked actions', () => {
  const REFRESH_KEY = 'settings.audio.streams.refresh';
  const SAVING_KEY = 'settings.actions.saving';

  beforeEach(() => {
    vi.clearAllMocks();
    stubElementAnimate();
  });

  afterEach(() => {
    cleanup();
    restoreElementAnimate();
  });

  it('says why Add stream and Refresh are blocked while a save runs, and ignores a Refresh click', async () => {
    const view = renderRows([streamA, streamB]);
    // The first health load keeps Refresh inert too, so let it finish before the block starts.
    await waitFor(() =>
      expect(screen.getByRole('button', { name: REFRESH_KEY })).not.toHaveAttribute('aria-disabled')
    );
    expect(api.get).toHaveBeenCalledOnce();

    await view.rerender({
      streams: [streamA, streamB],
      disabled: true,
      disabledReason: SAVING_KEY,
    });

    for (const name of [ADD_KEY, REFRESH_KEY]) {
      const button = screen.getByRole('button', { name });
      expect(button).toHaveAttribute('aria-disabled', 'true');
      expect(button).toHaveAccessibleDescription(SAVING_KEY);
    }
    await fireEvent.click(screen.getByRole('button', { name: REFRESH_KEY }));
    await fireEvent.click(screen.getByRole('button', { name: ADD_KEY }));

    expect(api.get).toHaveBeenCalledOnce();
    expect(screen.queryByTestId(NAME_TESTID)).toBeNull();
  });

  it('shows the reason with an empty list instead of an Add stream button that would do nothing', async () => {
    const view = renderRows([]);
    expect(screen.getByRole('button', { name: ADD_KEY })).toBeInTheDocument();

    await view.rerender({ streams: [], disabled: true, disabledReason: SAVING_KEY });

    expect(screen.getByText(SAVING_KEY)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: ADD_KEY })).toBeNull();
  });

  it('falls back to the loading reason when the caller gives none, for the add form too', async () => {
    const view = renderRows([]);
    await fillAddForm('Gate', 'rtsp://gate/live');
    await fireEvent.click(screen.getByTestId('stream-test-button'));

    await view.rerender({ streams: [], disabled: true });

    expect(screen.getByRole('button', { name: ADD_KEY })).toHaveAccessibleDescription(
      'common.loading'
    );
  });

  it('shows no reason and keeps the buttons usable while nothing is blocked', async () => {
    renderRows([streamA, streamB], { disabledReason: SAVING_KEY });

    expect(screen.queryByText(SAVING_KEY)).toBeNull();
    expect(screen.getByRole('button', { name: ADD_KEY })).not.toHaveAttribute('aria-disabled');
    // Refresh is inert only while the first health load runs.
    await waitFor(() =>
      expect(screen.getByRole('button', { name: REFRESH_KEY })).not.toHaveAttribute('aria-disabled')
    );
  });
});

describe('StreamManager health follows the saved streams', () => {
  const healthOf = (stream: StreamConfig) => ({
    name: stream.name,
    type: stream.type,
    url: stream.url,
    is_healthy: true,
    process_state: 'running',
    last_data_received: null,
    restart_count: 0,
    total_bytes_received: 0,
    bytes_per_second: 0,
    is_receiving_data: true,
  });
  const CONNECTED_KEY = 'settings.audio.streams.status.connected';
  const UNKNOWN_KEY = 'settings.audio.streams.status.unknown';

  beforeEach(() => {
    vi.clearAllMocks();
    sse.opened = 0;
    sse.closed = 0;
    vi.mocked(api.get).mockResolvedValue([healthOf(streamA), healthOf(streamB)]);
    stubElementAnimate();
  });

  afterEach(() => {
    cleanup();
    restoreElementAnimate();
  });

  it('shows the health of a saved stream, and none for a URL the form changed', async () => {
    const edited = { ...streamA, url: 'rtsp://a/new' };
    renderRows([edited, streamB], { savedStreams: [streamA, streamB] });

    await waitFor(() => expect(within(header('B')).getByText(CONNECTED_KEY)).toBeInTheDocument());
    expect(within(header('A')).getByText(UNKNOWN_KEY)).toBeInTheDocument();
  });

  it.each([
    ['named', (stream: StreamConfig) => healthOf(stream)],
    // Older responses carry no name and are matched by URL.
    ['unnamed', (stream: StreamConfig) => ({ ...healthOf(stream), name: undefined })],
  ])(
    'keeps the health of a stream that is deleted in the form until Reset, for %s entries',
    async (_label, entry) => {
      vi.mocked(api.get).mockResolvedValue([entry(streamA), entry(streamB)]);
      // A is deleted in the form before the health arrives; it is still saved.
      const view = renderRows([streamB], { savedStreams: [streamA, streamB] });
      await waitFor(() => expect(within(header('B')).getByText(CONNECTED_KEY)).toBeInTheDocument());

      await view.rerender({ streams: [streamA, streamB], savedStreams: [streamA, streamB] });

      expect(within(header('A')).getByText(CONNECTED_KEY)).toBeInTheDocument();
      expect(api.get).toHaveBeenCalledOnce();
    }
  );

  it('asks for the health again when the saved list changes, not when the form does', async () => {
    const view = renderRows([streamA], { savedStreams: [streamA] });
    await waitFor(() => expect(api.get).toHaveBeenCalledOnce());

    await view.rerender({ streams: [streamA, streamB], savedStreams: [streamA] });
    expect(api.get).toHaveBeenCalledOnce();

    await view.rerender({ streams: [streamA, streamB], savedStreams: [streamA, streamB] });
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(2));
  });

  it('listens for live health while a stream is saved, and not when none is', async () => {
    const view = renderRows([streamA], { savedStreams: [] });
    await tick();
    expect(sse.opened).toBe(0);

    await view.rerender({ streams: [streamA], savedStreams: [streamA] });
    expect(sse.opened).toBe(1);

    await view.rerender({ streams: [streamA], savedStreams: [] });
    expect(sse.closed).toBe(1);
  });
});
