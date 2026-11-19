import { tick } from 'svelte';
import { describe, it, expect, vi, beforeEach, afterEach, onTestFinished } from 'vitest';
import { screen, cleanup, fireEvent, render } from '@testing-library/svelte';
import StreamCard from './StreamCard.svelte';
import { analyzeStreamChannels } from './streamOptions';
import { deferred } from '../../../../test/async-helpers';
import {
  restoreElementAnimate,
  stubElementAnimate,
} from '../../../../test/fixtures/elementAnimate';
import type { ChannelAnalysis, StreamConfig } from '$lib/stores/settings';
import type { BackendModel } from '$lib/stores/models.svelte';
import { AUTOSAVE_DEBOUNCE_MS } from '$lib/stores/settingsAutoSave.svelte';

// SelectDropdown is a native <select> stand-in; the test button reports a test
// of its URL when clicked, so each test decides when a URL counts as tested.
vi.mock('./SelectDropdown.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockSelectDropdown.svelte')).default,
}));
vi.mock('./StreamTestButton.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockStreamTestButton.svelte')).default,
}));
vi.mock('./QuietHoursEditor.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockEmpty.svelte')).default,
}));
vi.mock('./StreamTimeline.svelte', async () => ({
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
vi.mock('$lib/stores/navigation.svelte', () => ({
  handleAppLinkClick: vi.fn(),
}));
vi.mock('$lib/stores/acousticModels.svelte', () => ({
  acousticModelAvailability: vi.fn(() => ({ kind: 'unknown' })),
}));

const LEFT_ANALYSIS: ChannelAnalysis = { channels: 2, energy: [], recommended: 'left' };
const ROW_ID = 'row-1';
const FIELD_PREFIX = `rtsp:streams:${ROW_ID}:`;
const URL_LABEL_KEY = 'settings.audio.streams.urlLabel';
const NAME_LABEL_KEY = 'settings.audio.streams.nameLabel';
const APPLY_KEY = 'settings.audio.streams.applyConnection';
const TEST_FIRST_KEY = 'settings.audio.streams.testBeforeApply';
const TYPE_SELECT_TESTID = 'select-settings.audio.streams.typeLabel';

const availableModels: BackendModel[] = [
  { id: 'birdnet', registryId: 'BirdNET_V2.4', name: 'BirdNET v2.4', category: 'bird' },
  { id: 'perch_v2', registryId: 'Perch_V2', name: 'Perch v2', category: 'wildlife' },
];

const baseStream: StreamConfig = {
  name: 'Front Yard',
  url: 'rtsp://host/stream',
  enabled: true,
  type: 'rtsp',
  transport: 'tcp',
  channelMode: 'downmix',
  mediaMode: 'full-stream',
  gain: 0,
  models: ['birdnet'],
};

interface RenderOptions {
  stream?: StreamConfig;
  models?: BackendModel[];
  streams?: StreamConfig[];
  expanded?: boolean;
  disabled?: boolean;
}

function renderCard(options: RenderOptions = {}) {
  const stream = options.stream ?? baseStream;
  const onCommit = vi.fn();
  const onDelete = vi.fn();
  // Raw render so the streamHealth context the card reads can be provided.
  const result = render(StreamCard, {
    context: new Map<string, unknown>([['streamHealth', {}]]),
    props: {
      stream,
      index: 0,
      streams: options.streams ?? [stream],
      rowId: ROW_ID,
      availableModels: options.models ?? availableModels,
      modelOptions: (options.models ?? availableModels).map(m => ({ value: m.id, label: m.name })),
      expanded: options.expanded ?? true,
      onToggleExpanded: vi.fn(),
      onCommit,
      onDelete,
      disabled: options.disabled ?? false,
    },
  });
  return { ...result, onCommit, onDelete };
}

function urlInput(): HTMLInputElement {
  return screen.getByLabelText<HTMLInputElement>(new RegExp(URL_LABEL_KEY));
}

function applyButton(): HTMLElement {
  return screen.getByRole('button', { name: APPLY_KEY });
}

describe('StreamCard immediate save', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
  });

  it('saves the enabled switch in the header at once, also while collapsed', async () => {
    const { onCommit } = renderCard({ expanded: false });

    await fireEvent.click(screen.getByRole('switch', { name: 'settings.audio.streams.enabled' }));
    expect(onCommit).toHaveBeenCalledExactlyOnceWith(
      { ...baseStream, enabled: false },
      { field: `${FIELD_PREFIX}enabled` }
    );
  });

  it('commits a name on blur and refuses a duplicate with the reason at the field', async () => {
    const other: StreamConfig = { ...baseStream, name: 'Porch', url: 'rtsp://porch/stream' };
    const { onCommit } = renderCard({ streams: [baseStream, other] });
    const input = screen.getByLabelText<HTMLInputElement>(new RegExp(NAME_LABEL_KEY));

    await fireEvent.input(input, { target: { value: 'porch' } });
    await fireEvent.blur(input);
    expect(onCommit).not.toHaveBeenCalled();
    expect(input).toHaveAccessibleDescription('settings.audio.streams.errors.duplicateName');

    await fireEvent.input(input, { target: { value: 'Back Yard' } });
    expect(onCommit).not.toHaveBeenCalled();
    await fireEvent.blur(input);
    expect(onCommit).toHaveBeenCalledExactlyOnceWith(
      { ...baseStream, name: 'Back Yard' },
      { field: `${FIELD_PREFIX}name` }
    );
  });

  it('keeps a URL edit as a draft and blocks Apply with the reason until that URL is tested', async () => {
    const { onCommit } = renderCard();
    expect(screen.queryByRole('button', { name: APPLY_KEY })).toBeNull();

    await fireEvent.input(urlInput(), { target: { value: 'rtsp://other/stream' } });
    expect(onCommit).not.toHaveBeenCalled();
    expect(applyButton()).toHaveAttribute('aria-disabled', 'true');
    expect(applyButton()).toHaveAccessibleDescription(TEST_FIRST_KEY);
    await fireEvent.click(applyButton());
    expect(onCommit).not.toHaveBeenCalled();

    await fireEvent.click(screen.getByTestId('stream-test-button'));
    expect(applyButton()).not.toHaveAttribute('aria-disabled');

    // A test of one URL does not cover the next edit.
    await fireEvent.input(urlInput(), { target: { value: 'rtsp://third/stream' } });
    expect(applyButton()).toHaveAttribute('aria-disabled', 'true');
    await fireEvent.click(screen.getByTestId('stream-test-button'));

    await fireEvent.click(applyButton());
    expect(onCommit).toHaveBeenCalledExactlyOnceWith(
      { ...baseStream, url: 'rtsp://third/stream' },
      { field: `${FIELD_PREFIX}connection` }
    );
  });

  it('blocks Apply for a URL another stream already uses and says why', async () => {
    const other: StreamConfig = { ...baseStream, name: 'Porch', url: 'rtsp://porch/stream' };
    const { onCommit } = renderCard({ streams: [baseStream, other] });

    await fireEvent.input(urlInput(), { target: { value: 'rtsp://porch/stream' } });
    await fireEvent.click(screen.getByTestId('stream-test-button'));

    expect(applyButton()).toHaveAttribute('aria-disabled', 'true');
    expect(applyButton()).toHaveAccessibleDescription('settings.audio.streams.errors.duplicate');
    await fireEvent.click(applyButton());
    expect(onCommit).not.toHaveBeenCalled();
  });

  it('applies a type change that fits the URL without a test, drops the RTSP-only fields, and Cancel restores', async () => {
    const httpStream: StreamConfig = {
      ...baseStream,
      type: 'http',
      url: 'http://host/stream.m3u8',
    };
    const { onCommit } = renderCard({ stream: httpStream });

    await fireEvent.change(screen.getByTestId(TYPE_SELECT_TESTID), { target: { value: 'hls' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.getByTestId<HTMLSelectElement>(TYPE_SELECT_TESTID).value).toBe('http');
    expect(screen.queryByRole('button', { name: APPLY_KEY })).toBeNull();

    await fireEvent.change(screen.getByTestId(TYPE_SELECT_TESTID), { target: { value: 'hls' } });
    await fireEvent.click(applyButton());
    expect(onCommit).toHaveBeenCalledOnce();
    const [committed, options] = onCommit.mock.calls[0];
    expect(options).toEqual({ field: `${FIELD_PREFIX}connection` });
    expect(committed).toEqual({
      name: 'Front Yard',
      url: 'http://host/stream.m3u8',
      enabled: true,
      type: 'hls',
      channelMode: 'downmix',
      gain: 0,
      models: ['birdnet'],
    });
  });

  it('blocks Apply when only the type changes to one the saved URL does not fit', async () => {
    const { onCommit } = renderCard();

    await fireEvent.change(screen.getByTestId(TYPE_SELECT_TESTID), { target: { value: 'http' } });
    expect(applyButton()).toHaveAttribute('aria-disabled', 'true');
    expect(applyButton()).toHaveAccessibleDescription('settings.audio.streams.errors.invalidUrl');
    await fireEvent.click(applyButton());
    expect(onCommit).not.toHaveBeenCalled();
  });

  it('commits gain once on release with the debounce', async () => {
    const { onCommit } = renderCard();
    const slider = screen.getByRole('slider');

    await fireEvent.input(slider, { target: { value: '4' } });
    expect(onCommit).not.toHaveBeenCalled();
    await fireEvent.change(slider, { target: { value: '4' } });
    expect(onCommit).toHaveBeenCalledExactlyOnceWith(
      { ...baseStream, gain: 4 },
      { field: `${FIELD_PREFIX}gain`, debounceMs: AUTOSAVE_DEBOUNCE_MS }
    );
  });

  it('limits the name to what the server accepts', () => {
    renderCard();

    const input = screen.getByLabelText<HTMLInputElement>(new RegExp(NAME_LABEL_KEY));
    expect(input).toHaveAttribute('maxlength', '64');
  });

  it('refuses a name longer than the server limit in bytes, with the reason at the field', async () => {
    const { onCommit } = renderCard();
    const input = screen.getByLabelText<HTMLInputElement>(new RegExp(NAME_LABEL_KEY));

    // 22 euro signs are 22 characters, within maxlength, but 66 bytes.
    await fireEvent.input(input, { target: { value: '\u20ac'.repeat(22) } });
    await fireEvent.blur(input);

    expect(onCommit).not.toHaveBeenCalled();
    expect(input).toHaveAccessibleDescription('settings.audio.streams.errors.nameTooLong');
  });

  it('applies a channel analysis of the current URL when Test is clicked again meanwhile', async () => {
    const pending = deferred<ChannelAnalysis>();
    vi.mocked(analyzeStreamChannels).mockReturnValue(pending.promise);
    renderCard();

    await fireEvent.input(urlInput(), { target: { value: 'rtsp://other/stream' } });
    await fireEvent.click(screen.getByTestId('channel-analyze'));

    // A new test run, or a failed one, reports a cleared result for the same URL.
    await fireEvent.click(screen.getByTestId('stream-test-clear'));
    expect(screen.getByTestId('channel-controls')).toHaveAttribute('data-analyzing', 'true');

    pending.resolve(LEFT_ANALYSIS);
    await pending.promise;
    await tick();
    expect(screen.getByTestId('channel-controls')).toHaveAttribute('data-mode', 'left');
    expect(screen.getByTestId('channel-controls')).toHaveAttribute('data-analyzing', 'false');
  });

  it('ignores a channel analysis of a URL the draft no longer holds', async () => {
    const pending = deferred<ChannelAnalysis>();
    vi.mocked(analyzeStreamChannels).mockReturnValue(pending.promise);
    renderCard();

    await fireEvent.input(urlInput(), { target: { value: 'rtsp://other/stream' } });
    await fireEvent.click(screen.getByTestId('channel-analyze'));
    await fireEvent.input(urlInput(), { target: { value: 'rtsp://third/stream' } });

    pending.resolve(LEFT_ANALYSIS);
    await pending.promise;
    await tick();
    expect(screen.getByTestId('channel-controls')).toHaveAttribute('data-mode', 'downmix');
  });

  it('warns about a bat model on a stream', () => {
    const bat: BackendModel = { id: 'bat', registryId: 'Bat', name: 'Bat model', category: 'bat' };
    renderCard({ models: [...availableModels, bat], stream: { ...baseStream, models: ['bat'] } });

    expect(screen.getByText('settings.audio.streams.batWarning')).toBeInTheDocument();
  });

  it('commits a classifier switch at once with the complete list', async () => {
    const { onCommit } = renderCard();

    await fireEvent.click(screen.getByRole('switch', { name: 'Perch v2' }));
    expect(onCommit).toHaveBeenCalledExactlyOnceWith(
      { ...baseStream, models: ['birdnet', 'perch_v2'] },
      { field: `${FIELD_PREFIX}models` }
    );
  });

  it('previews a stream equalizer without a sampleRate, even after a test probed one', async () => {
    stubElementAnimate();
    onTestFinished(restoreElementAnimate);
    renderCard();

    await fireEvent.click(screen.getByTestId('stream-test-button'));
    await fireEvent.click(
      screen.getByRole('button', { name: 'settings.audio.audioFilters.title' })
    );

    expect(screen.getByTestId('audio-equalizer-settings')).toHaveTextContent('none');
  });
});

describe('StreamCard delete and groups', () => {
  it('names the stream in the delete confirmation', async () => {
    renderCard({ expanded: false });

    await fireEvent.click(screen.getByRole('button', { name: 'common.delete' }));
    expect(screen.getByRole('alertdialog')).toHaveAccessibleDescription(
      'Remove stream "Front Yard"?'
    );
  });

  it('says why Delete is blocked while the settings load', () => {
    renderCard({ expanded: false, disabled: true });

    const trigger = screen.getByRole('button', { name: 'common.delete' });
    expect(trigger).toHaveAttribute('aria-disabled', 'true');
    expect(trigger).toHaveAccessibleDescription('common.loading');
  });

  it('shows body group titles as group labels and single-control titles as field labels', () => {
    renderCard();

    for (const name of [
      'settings.audio.streams.connectionTitle',
      'settings.audio.soundCards.modelLabel',
      'settings.audio.quietHours.title',
    ]) {
      expect(screen.getByRole('heading', { level: 4, name })).toHaveClass('uppercase');
    }
    for (const name of [
      'settings.audio.audioFilters.title',
      'settings.audio.streams.diagnostics.title',
    ]) {
      expect(screen.getByRole('button', { name })).toHaveClass(
        'uppercase',
        'flex',
        'items-center',
        'gap-2'
      );
    }
    expect(
      screen.getByRole('heading', { level: 4, name: 'settings.audio.soundCards.levelTitle' })
    ).not.toHaveClass('uppercase');
    expect(
      screen.getByText('settings.audio.streams.nameLabel', { selector: 'label' })
    ).not.toHaveClass('uppercase');
  });

  it('names the audio filters region after its disclosure button', () => {
    renderCard();

    expect(
      screen.getByRole('region', { name: 'settings.audio.audioFilters.title' })
    ).toBeInTheDocument();
  });
});
