/**
 * StreamManager on the immediately saved streams tab: UI-only row ids key the
 * cards and their open state, add and delete save the whole array at once, and
 * the model list stays live while mounted.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { cleanup, fireEvent, screen } from '@testing-library/svelte';
import { createComponentTestFactory, renderTyped } from '../../../../test/render-helpers';
import {
  restoreElementAnimate,
  stubElementAnimate,
} from '../../../../test/fixtures/elementAnimate';
import type { StreamConfig } from '$lib/stores/settings';
import { STREAMS_LIST_FIELD } from './sourceFields';

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
  default: (await import('../../../../test/fixtures/MockEmpty.svelte')).default,
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

vi.mock('$lib/utils/api', () => ({
  api: { get: vi.fn(() => Promise.resolve([])) },
}));

vi.mock('$lib/utils/ReconnectingEventSource', () => ({
  ReconnectingEventSource: class ReconnectingEventSource {
    addEventListener(): void {}
    close(): void {}
  },
}));

import { watchModels } from '$lib/stores/models.svelte';
import StreamManager from './StreamManager.svelte';

const ADD_KEY = 'settings.audio.streams.addStream';

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

  it('keeps a row open when its URL changes, since rows are keyed by their own id', async () => {
    const { rerender } = renderRows([streamA, streamB]);
    await fireEvent.click(header('B'));

    await rerender({ streams: [streamA, { ...streamB, url: 'rtsp://b/other' }] });
    expect(header('B')).toHaveAttribute('aria-expanded', 'true');
  });

  it('gives every row a fresh id when the list length changes from outside', async () => {
    const { rerender } = renderRows([streamA, streamB]);
    await fireEvent.click(header('B'));

    await rerender({ streams: [streamA, streamB, streamC] });
    expect(header('B')).toHaveAttribute('aria-expanded', 'false');
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
