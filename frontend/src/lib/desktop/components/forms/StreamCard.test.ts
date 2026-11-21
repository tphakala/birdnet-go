import { tick } from 'svelte';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
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
import type { StreamProblem } from './sourceRules';

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
const URL_LABEL_KEY = 'settings.audio.streams.urlLabel';
const NAME_LABEL_KEY = 'settings.audio.streams.nameLabel';
const TYPE_SELECT_TESTID = 'select-settings.audio.streams.typeLabel';
const TRANSPORT_SELECT_TESTID = 'select-settings.audio.streams.transportLabel';
const MEDIA_MODE_SELECT_TESTID = 'select-settings.audio.streams.mediaModeLabel';
const NEEDS_ATTENTION_KEY = 'settings.audio.validation.needsAttention';

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
  problem?: StreamProblem;
  expanded?: boolean;
  disabled?: boolean;
  disabledReason?: string;
}

function renderCard(options: RenderOptions = {}) {
  const onChange = vi.fn();
  const onUrlTested = vi.fn();
  const onDelete = vi.fn();
  // Raw render so the streamHealth context the card reads can be provided.
  let props = {
    stream: options.stream ?? baseStream,
    problem: options.problem,
    availableModels: options.models ?? availableModels,
    modelOptions: (options.models ?? availableModels).map(m => ({ value: m.id, label: m.name })),
    expanded: options.expanded ?? true,
    onToggleExpanded: vi.fn(),
    onChange,
    onUrlTested,
    onDelete,
    disabled: options.disabled ?? false,
    disabledReason: options.disabledReason,
  };
  const result = render(StreamCard, {
    context: new Map<string, unknown>([['streamHealth', {}]]),
    props,
  });
  // Sets some props and keeps the others, as a parent that re-renders would.
  const update = async (patch: Partial<typeof props>) => {
    props = { ...props, ...patch };
    await result.rerender(props);
  };
  return { ...result, onChange, onUrlTested, onDelete, update };
}

function urlInput(): HTMLInputElement {
  return screen.getByLabelText<HTMLInputElement>(new RegExp(URL_LABEL_KEY));
}

function nameInput(): HTMLInputElement {
  return screen.getByLabelText<HTMLInputElement>(new RegExp(NAME_LABEL_KEY));
}

describe('StreamCard editing', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    stubElementAnimate();
  });

  afterEach(() => {
    cleanup();
    restoreElementAnimate();
  });

  it('writes the enabled switch in the header at once, also while collapsed', async () => {
    const { onChange } = renderCard({ expanded: false });

    await fireEvent.click(screen.getByRole('switch', { name: 'settings.audio.streams.enabled' }));
    expect(onChange).toHaveBeenCalledExactlyOnceWith({ ...baseStream, enabled: false });
  });

  it('writes the whole stream with the name as typed and the trimmed name when the field is left', async () => {
    const { onChange } = renderCard();
    const input = nameInput();

    await fireEvent.input(input, { target: { value: ' Back Yard ' } });
    expect(onChange).toHaveBeenLastCalledWith({ ...baseStream, name: ' Back Yard ' });

    await fireEvent.blur(input);
    expect(onChange).toHaveBeenLastCalledWith({ ...baseStream, name: 'Back Yard' });
  });

  it('writes nothing on blur when the name is already trimmed and unchanged', async () => {
    const { onChange } = renderCard();

    await fireEvent.blur(nameInput());

    expect(onChange).not.toHaveBeenCalled();
  });

  it('marks the name and URL fields required for assistive technology without the native attribute', () => {
    renderCard();

    for (const input of [nameInput(), urlInput()]) {
      expect(input).toHaveAttribute('aria-required', 'true');
      expect(input).not.toHaveAttribute('required');
    }
  });

  it('labels the URL field like the other fields of its group, a real label that says required', () => {
    renderCard();

    const label = screen.getByText(URL_LABEL_KEY, { selector: 'label span' });
    expect(label).toHaveClass('label-text');
    expect(label.closest('label')).not.toHaveClass('font-semibold');
    expect(screen.getByLabelText(new RegExp(URL_LABEL_KEY))).toBe(urlInput());
    expect(urlInput()).toHaveAttribute('aria-required', 'true');
    expect(label.closest('label')).toHaveTextContent('*');
  });

  it('limits the name to what the server accepts', () => {
    renderCard();

    expect(nameInput()).toHaveAttribute('maxlength', '64');
  });

  it.each([
    ['required', 'settings.audio.streams.errors.nameRequired'],
    ['tooLong', 'settings.audio.streams.errors.nameTooLong'],
    ['duplicate', 'settings.audio.streams.errors.duplicateName'],
  ] as const)(
    'shows a %s name problem at once on a card that opens with it, and flags the header',
    (name, message) => {
      renderCard({ problem: { name } });

      expect(nameInput()).toHaveAttribute('aria-invalid', 'true');
      expect(nameInput()).toHaveAccessibleDescription(message);
      expect(screen.getByText(NEEDS_ATTENTION_KEY)).toBeInTheDocument();
    }
  );

  it('shows no message and no attention badge without a problem', () => {
    renderCard();

    expect(nameInput()).not.toHaveAttribute('aria-invalid');
    expect(urlInput()).not.toHaveAttribute('aria-invalid');
    expect(screen.queryByText(NEEDS_ATTENTION_KEY)).toBeNull();
  });

  it('hides a shown name error while the field is edited and shows it again when left', async () => {
    renderCard({ problem: { name: 'duplicate' } });

    await fireEvent.input(nameInput(), { target: { value: 'Porc' } });
    expect(nameInput()).not.toHaveAttribute('aria-invalid');

    await fireEvent.blur(nameInput());
    expect(nameInput()).toHaveAttribute('aria-invalid', 'true');
  });

  it('shows the name error again when a save ends an edit that hid it', async () => {
    const { update } = renderCard({ problem: { name: 'duplicate' } });
    await fireEvent.input(nameInput(), { target: { value: 'Porc' } });
    expect(nameInput()).not.toHaveAttribute('aria-invalid');

    // A running save disables the card: the field loses focus without a blur event.
    await update({ disabled: true });

    expect(nameInput()).toHaveAttribute('aria-invalid', 'true');
  });

  it('shows the name error again when a collapse ends an edit that hid it', async () => {
    const { update } = renderCard({ problem: { name: 'duplicate' } });
    await fireEvent.input(nameInput(), { target: { value: 'Porc' } });

    await update({ expanded: false });
    await update({ expanded: true });

    expect(nameInput()).toHaveAttribute('aria-invalid', 'true');
  });

  it('shows a duplicate made on another card at once, without the field being left', async () => {
    const { update } = renderCard();
    expect(nameInput()).not.toHaveAttribute('aria-invalid');

    await update({ problem: { name: 'duplicate' } });

    expect(nameInput()).toHaveAttribute('aria-invalid', 'true');
  });

  it('writes the URL as typed with the type its scheme names, and the trimmed URL when left', async () => {
    const { onChange } = renderCard();
    const input = urlInput();

    await fireEvent.input(input, { target: { value: 'rtmp://host/live ' } });
    expect(onChange).toHaveBeenLastCalledWith({
      ...baseStream,
      url: 'rtmp://host/live ',
      type: 'rtmp',
    });

    await fireEvent.blur(input);
    expect(onChange).toHaveBeenLastCalledWith({ ...baseStream, url: 'rtmp://host/live' });
  });

  it('keeps the type while the typed URL names no scheme yet', async () => {
    const { onChange } = renderCard();

    await fireEvent.input(urlInput(), { target: { value: 'host/stream' } });

    expect(onChange).toHaveBeenCalledExactlyOnceWith({ ...baseStream, url: 'host/stream' });
  });

  it('says an untested URL needs its test at once, marks the field and flags the header', () => {
    renderCard({ problem: { url: 'untested' } });

    expect(urlInput()).toHaveAttribute('aria-invalid', 'true');
    expect(urlInput()).toHaveAccessibleDescription('settings.audio.streams.testRequired');
    expect(screen.getByText(NEEDS_ATTENTION_KEY)).toBeInTheDocument();
  });

  it('keeps saying an untested URL needs its test while the field is edited', async () => {
    renderCard({ problem: { url: 'untested' } });

    await fireEvent.input(urlInput(), { target: { value: 'rtsp://host/other' } });

    expect(urlInput()).toHaveAccessibleDescription('settings.audio.streams.testRequired');
  });

  it.each([
    ['required', 'settings.audio.streams.errors.urlRequired'],
    ['invalid', 'settings.audio.streams.errors.invalidUrl'],
    ['duplicate', 'settings.audio.streams.errors.duplicate'],
  ] as const)(
    'shows a %s URL problem once the field was left, and hides it while it is edited',
    async (url, message) => {
      const { update } = renderCard();
      await fireEvent.input(urlInput(), { target: { value: 'rtsp://host/x' } });
      expect(urlInput()).not.toHaveAttribute('aria-invalid');

      // The page evaluates the rules and hands the problem to the card.
      await update({ problem: { url } });
      expect(urlInput()).not.toHaveAttribute('aria-invalid');

      await fireEvent.blur(urlInput());
      expect(urlInput()).toHaveAttribute('aria-invalid', 'true');
      expect(urlInput()).toHaveAccessibleDescription(message);
    }
  );

  it('shows a URL duplicate made on another card at once', async () => {
    const { update } = renderCard();

    await update({ problem: { url: 'duplicate' } });

    expect(urlInput()).toHaveAttribute('aria-invalid', 'true');
  });

  it('shows the URL error again when a save ends an edit that hid it', async () => {
    const { update } = renderCard({ problem: { url: 'duplicate' } });
    await fireEvent.input(urlInput(), { target: { value: 'rtsp://host/x' } });
    expect(urlInput()).not.toHaveAttribute('aria-invalid');

    await update({ disabled: true });

    expect(urlInput()).toHaveAttribute('aria-invalid', 'true');
  });

  it('reports a successful test with the URL it tested', async () => {
    const { onUrlTested } = renderCard();

    await fireEvent.click(screen.getByTestId('stream-test-button'));

    expect(onUrlTested).toHaveBeenCalledExactlyOnceWith('rtsp://host/stream');
  });

  it('reports the URL that was tested, not the one the field holds when the result arrives', async () => {
    const { onUrlTested, update } = renderCard();
    await fireEvent.click(screen.getByTestId('stream-test-start'));

    await update({ stream: { ...baseStream, url: 'rtsp://host/other' } });
    await fireEvent.click(screen.getByTestId('stream-test-finish'));

    expect(onUrlTested).toHaveBeenCalledExactlyOnceWith('rtsp://host/stream');
  });

  it('keeps the detected channels of the current URL when a probe of an earlier URL finishes late', async () => {
    const { onUrlTested, update } = renderCard();
    await fireEvent.click(screen.getByTestId('stream-test-start'));

    await update({ stream: { ...baseStream, url: 'rtsp://host/other' } });
    await fireEvent.click(screen.getByTestId('stream-test-finish'));

    expect(screen.getByTestId('channel-controls')).toHaveAttribute('data-channels', '0');
    expect(onUrlTested).toHaveBeenCalledOnce();
  });

  it('shows the detected channels of a test of the current URL', async () => {
    renderCard();

    await fireEvent.click(screen.getByTestId('stream-test-button'));

    expect(screen.getByTestId('channel-controls')).toHaveAttribute('data-channels', '1');
  });

  it('does not report a test that was cleared or failed', async () => {
    const { onUrlTested } = renderCard();

    await fireEvent.click(screen.getByTestId('stream-test-clear'));

    expect(onUrlTested).not.toHaveBeenCalled();
  });

  it.each([
    ['type', TYPE_SELECT_TESTID, 'http', { type: 'http' }],
    ['transport', TRANSPORT_SELECT_TESTID, 'udp', { transport: 'udp' }],
    ['media mode', MEDIA_MODE_SELECT_TESTID, 'audio-only', { mediaMode: 'audio-only' }],
  ])(
    'writes only the %s key of the stream when it is picked',
    async (_label, testId, value, patch) => {
      const { onChange } = renderCard();

      await fireEvent.change(screen.getByTestId(testId), { target: { value } });

      expect(onChange).toHaveBeenCalledExactlyOnceWith({ ...baseStream, ...patch });
    }
  );

  it('writes only the type of a stream that stores no transport or media mode', async () => {
    const httpStream: StreamConfig = {
      name: 'Garden',
      url: 'http://host/live.m3u8',
      enabled: true,
      type: 'http',
      gain: 0,
      models: [],
    };
    const { onChange } = renderCard({ stream: httpStream });

    await fireEvent.change(screen.getByTestId(TYPE_SELECT_TESTID), { target: { value: 'hls' } });

    expect(onChange).toHaveBeenCalledExactlyOnceWith({ ...httpStream, type: 'hls' });
  });

  it('writes the gain once on release, not while dragging', async () => {
    const { onChange } = renderCard();
    const slider = screen.getByRole('slider');

    await fireEvent.input(slider, { target: { value: '4' } });
    expect(onChange).not.toHaveBeenCalled();
    await fireEvent.change(slider, { target: { value: '4' } });
    expect(onChange).toHaveBeenCalledExactlyOnceWith({ ...baseStream, gain: 4 });
  });

  it('writes a classifier switch with the complete list', async () => {
    const { onChange } = renderCard();

    await fireEvent.click(screen.getByRole('switch', { name: 'Perch v2' }));
    expect(onChange).toHaveBeenCalledExactlyOnceWith({
      ...baseStream,
      models: ['birdnet', 'perch_v2'],
    });
  });

  it('writes nothing when it opens or collapses', async () => {
    const { onChange, update } = renderCard({ expanded: false });
    await update({ expanded: true });
    await update({ expanded: false });

    expect(onChange).not.toHaveBeenCalled();
  });

  it('writes the recommended channel mode of an analysis of the current URL', async () => {
    const pending = deferred<ChannelAnalysis>();
    vi.mocked(analyzeStreamChannels).mockReturnValue(pending.promise);
    const { onChange } = renderCard();

    await fireEvent.click(screen.getByTestId('channel-analyze'));
    // A new test run, or a failed one, reports a cleared result for the same URL.
    await fireEvent.click(screen.getByTestId('stream-test-clear'));
    expect(screen.getByTestId('channel-controls')).toHaveAttribute('data-analyzing', 'true');

    pending.resolve(LEFT_ANALYSIS);
    await pending.promise;
    await tick();
    expect(onChange).toHaveBeenCalledExactlyOnceWith({ ...baseStream, channelMode: 'left' });
  });

  it.each([
    ['the card is disabled by a running save', { disabled: true }],
    ['the card is collapsed', { expanded: false }],
  ])('drops a late channel analysis when %s meanwhile', async (_label, patch) => {
    const pending = deferred<ChannelAnalysis>();
    vi.mocked(analyzeStreamChannels).mockReturnValue(pending.promise);
    const { onChange, update } = renderCard();
    await fireEvent.click(screen.getByTestId('channel-analyze'));

    await update(patch);
    await update({ disabled: false, expanded: true });
    pending.resolve(LEFT_ANALYSIS);
    await pending.promise;
    await tick();

    expect(onChange).not.toHaveBeenCalled();
  });

  it('ignores a channel analysis of a URL the stream no longer holds', async () => {
    const pending = deferred<ChannelAnalysis>();
    vi.mocked(analyzeStreamChannels).mockReturnValue(pending.promise);
    const { onChange, update } = renderCard();
    await fireEvent.click(screen.getByTestId('channel-analyze'));

    await update({ stream: { ...baseStream, url: 'rtsp://host/other' } });
    pending.resolve(LEFT_ANALYSIS);
    await pending.promise;
    await tick();

    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByTestId('channel-controls')).toHaveAttribute('data-mode', 'downmix');
  });

  it('warns about a bat model on a stream', () => {
    const bat: BackendModel = { id: 'bat', registryId: 'Bat', name: 'Bat model', category: 'bat' };
    renderCard({ models: [...availableModels, bat], stream: { ...baseStream, models: ['bat'] } });

    expect(screen.getByText('settings.audio.streams.batWarning')).toBeInTheDocument();
  });

  it('previews a stream equalizer without a sampleRate, even after a test probed one', async () => {
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

  it('says why Delete is blocked while a save runs', () => {
    renderCard({ expanded: false, disabled: true, disabledReason: 'settings.actions.saving' });

    expect(screen.getByRole('button', { name: 'common.delete' })).toHaveAccessibleDescription(
      'settings.actions.saving'
    );
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
    expect(
      screen.getByText('settings.audio.streams.urlLabel', { selector: 'label span' })
    ).not.toHaveClass('uppercase');
  });

  it('names the audio filters region after its disclosure button', () => {
    renderCard();

    expect(
      screen.getByRole('region', { name: 'settings.audio.audioFilters.title' })
    ).toBeInTheDocument();
  });
});
