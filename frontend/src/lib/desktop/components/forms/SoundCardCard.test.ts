import { describe, it, expect, vi, beforeEach, afterEach, onTestFinished } from 'vitest';
import { screen, cleanup, fireEvent, waitFor } from '@testing-library/svelte';
import { renderTyped } from '../../../../test/render-helpers';
import {
  restoreElementAnimate,
  stubElementAnimate,
} from '../../../../test/fixtures/elementAnimate';
import {
  DEVICE_SELECT_TESTID,
  SAMPLE_RATE_SELECT_TESTID,
  makeAbortableProbe,
} from '../../../../test/fixtures/soundCardProbeTest';
import type { AudioSourceConfig } from '$lib/stores/settings';
import type { AcousticModelAvailability } from '$lib/types/models';
import type { BackendModel } from '$lib/stores/models.svelte';
import { AUTOSAVE_DEBOUNCE_MS } from '$lib/stores/settingsAutoSave.svelte';

// Mocks for common form dependencies to keep the render lightweight.
vi.mock('./SelectDropdown.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockSelectDropdown.svelte')).default,
}));
vi.mock('./QuietHoursEditor.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockEmpty.svelte')).default,
}));
vi.mock('$lib/desktop/features/settings/components/AudioEqualizerSettings.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockAudioEqualizerSettings.svelte')).default,
}));
vi.mock('$lib/stores/navigation.svelte', () => ({
  handleAppLinkClick: vi.fn(),
}));
vi.mock('$lib/utils/audio/sampleRate', async importActual => ({
  ...(await importActual<typeof import('$lib/utils/audio/sampleRate')>()),
  fetchDeviceCapabilities: vi.fn((deviceId: string, signal?: AbortSignal) =>
    makeAbortableProbe(
      signal,
      deviceId === 'usb-2'
        ? [
            { value: '48000', label: '48 kHz' },
            { value: '96000', label: '96 kHz' },
          ]
        : [{ value: '48000', label: '48 kHz' }]
    )
  ),
}));

// The classifier verdict decides how an empty model list is described in the
// header; default to "unknown" (nothing fetched) and override per test.
const { acousticModelAvailability } = vi.hoisted(() => ({
  acousticModelAvailability: vi.fn<() => AcousticModelAvailability>(() => ({ kind: 'unknown' })),
}));
vi.mock('$lib/stores/acousticModels.svelte', () => ({
  acousticModelAvailability,
}));

import SoundCardCard from './SoundCardCard.svelte';

const DEFAULT_PENDING_BADGE_KEY = 'settings.audio.models.defaultPendingBadge';
const DEFAULT_BADGE_KEY = 'settings.audio.models.defaultBadge';
const NONE_BADGE_KEY = 'settings.audio.models.noneBadge';
const NAME_LABEL_KEY = 'settings.audio.soundCards.nameLabel';
const APPLY_KEY = 'settings.audio.soundCards.applyCapture';
const WARNING_BADGE_KEY = 'settings.audio.models.warningBadge';

const ROW_ID = 'row-1';

const audioDevices = [
  { index: 0, name: 'Default Mic', id: 'sysdefault' },
  { index: 1, name: 'USB Mic', id: 'usb-2' },
];
const modelOptions = [
  { value: 'birdnet', label: 'BirdNET v2.4' },
  { value: 'perch_v2', label: 'Perch v2' },
];
const availableModels: BackendModel[] = [
  { id: 'birdnet', registryId: 'BirdNET_V2.4', name: 'BirdNET v2.4', category: 'bird' },
  { id: 'perch_v2', registryId: 'Perch_V2', name: 'Perch v2', category: 'wildlife' },
];

const baseSource: AudioSourceConfig = {
  name: 'Living room',
  device: 'sysdefault',
  gain: 0,
  models: ['birdnet'],
};

interface RenderOptions {
  source?: AudioSourceConfig;
  sources?: AudioSourceConfig[];
  expanded?: boolean;
  models?: BackendModel[];
}

function renderCard(options: RenderOptions = {}) {
  const source = options.source ?? baseSource;
  const onCommit = vi.fn();
  const onDelete = vi.fn();
  const onToggleExpanded = vi.fn();
  const result = renderTyped(SoundCardCard, {
    props: {
      source,
      index: 0,
      sources: options.sources ?? [source],
      rowId: ROW_ID,
      audioDevices,
      modelOptions,
      availableModels: options.models ?? availableModels,
      disabled: false,
      expanded: options.expanded ?? true,
      onToggleExpanded,
      onCommit,
      onDelete,
    },
  });
  return { ...result, onCommit, onDelete, onToggleExpanded };
}

function nameInput(): HTMLInputElement {
  return screen.getByLabelText<HTMLInputElement>(new RegExp(NAME_LABEL_KEY));
}

describe('SoundCardCard header', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    acousticModelAvailability.mockReturnValue({ kind: 'unknown' });
  });

  afterEach(() => {
    cleanup();
  });

  function renderWithModels(models: AudioSourceConfig['models'] | undefined | null) {
    const source = { ...baseSource, models } as unknown as AudioSourceConfig;
    return renderCard({ source, expanded: false });
  }

  it('renders without throwing when source.models is undefined or null', () => {
    expect(() => renderWithModels(undefined)).not.toThrow();
    expect(screen.getByText(DEFAULT_PENDING_BADGE_KEY)).toBeInTheDocument();
    cleanup();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any -- legacy JSON deserializes omitted arrays as null
    expect(() => renderWithModels(null as any)).not.toThrow();
    expect(screen.getByText(DEFAULT_PENDING_BADGE_KEY)).toBeInTheDocument();
  });

  it('names the mapped default targets when the classifier is ready', () => {
    acousticModelAvailability.mockReturnValue({
      kind: 'ready',
      defaultTargets: ['BirdNET_V2.4', 'Perch_V2'],
    });
    renderWithModels([]);

    expect(screen.getByText(DEFAULT_BADGE_KEY)).toBeInTheDocument();
    expect(screen.queryByText(NONE_BADGE_KEY)).toBeNull();
  });

  it('shows a warning "no model loaded" pill with an explanatory tooltip at N=0', () => {
    acousticModelAvailability.mockReturnValue({ kind: 'none', reason: 'none_installed' });
    renderWithModels([]);

    const badge = screen.getByText(NONE_BADGE_KEY);
    expect(badge).toHaveAttribute('title', 'settings.audio.models.noneEnabledHelp');
  });

  it('flags a source whose selected model is missing or not loaded', () => {
    renderCard({
      source: { ...baseSource, models: ['birdnet', 'gone_model'] },
      expanded: false,
    });
    expect(screen.getByText(WARNING_BADGE_KEY)).toBeInTheDocument();
    cleanup();

    renderCard({
      models: [{ ...availableModels[0], state: 'failed' }],
      expanded: false,
    });
    expect(screen.getByText(WARNING_BADGE_KEY)).toBeInTheDocument();
    cleanup();

    renderCard({ expanded: false });
    expect(screen.queryByText(WARNING_BADGE_KEY)).toBeNull();
  });

  it('toggles the body from the header and reports the state with aria-expanded', async () => {
    const { onToggleExpanded } = renderCard({ expanded: false });
    const header = screen.getByRole('button', { expanded: false });
    expect(screen.queryByLabelText(new RegExp(NAME_LABEL_KEY))).toBeNull();

    await fireEvent.click(header);
    expect(onToggleExpanded).toHaveBeenCalledOnce();
  });
});

describe('SoundCardCard immediate save', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    acousticModelAvailability.mockReturnValue({ kind: 'unknown' });
  });

  afterEach(() => {
    cleanup();
  });

  it('commits a name on blur, not on every keystroke', async () => {
    const { onCommit } = renderCard();
    const input = nameInput();

    await fireEvent.input(input, { target: { value: 'Back yard' } });
    expect(onCommit).not.toHaveBeenCalled();

    await fireEvent.blur(input);
    expect(onCommit).toHaveBeenCalledExactlyOnceWith(
      { ...baseSource, name: 'Back yard' },
      { field: `audio:sources:${ROW_ID}:name` }
    );
  });

  it('commits a name on Enter and restores it on Escape', async () => {
    const { onCommit } = renderCard();
    const input = nameInput();

    await fireEvent.input(input, { target: { value: 'Typo' } });
    await fireEvent.keyDown(input, { key: 'Escape' });
    expect(input.value).toBe('Living room');
    await fireEvent.blur(input);
    expect(onCommit).not.toHaveBeenCalled();

    await fireEvent.input(input, { target: { value: 'Back yard' } });
    await fireEvent.keyDown(input, { key: 'Enter' });
    expect(onCommit).toHaveBeenCalledOnce();
  });

  it('does not commit an empty or duplicate name and says why at the field', async () => {
    const other: AudioSourceConfig = { ...baseSource, name: 'Porch', device: 'usb-2' };
    const { onCommit } = renderCard({ sources: [baseSource, other] });
    const input = nameInput();

    await fireEvent.input(input, { target: { value: '   ' } });
    await fireEvent.blur(input);
    expect(onCommit).not.toHaveBeenCalled();
    expect(input).toHaveAttribute('aria-invalid', 'true');
    expect(input).toHaveAccessibleDescription('settings.audio.soundCards.errors.nameRequired');

    await fireEvent.input(input, { target: { value: 'porch' } });
    // Editing clears the message until the field is left again.
    expect(input).not.toHaveAttribute('aria-invalid');
    await fireEvent.blur(input);
    expect(onCommit).not.toHaveBeenCalled();
    expect(input).toHaveAccessibleDescription('settings.audio.soundCards.errors.duplicateName');
  });

  it('keeps a device change as a draft until Apply, and Cancel restores it', async () => {
    const { onCommit } = renderCard();
    const device = screen.getByTestId<HTMLSelectElement>(DEVICE_SELECT_TESTID);
    expect(screen.queryByRole('button', { name: APPLY_KEY })).toBeNull();

    await fireEvent.change(device, { target: { value: 'usb-2' } });
    expect(onCommit).not.toHaveBeenCalled();

    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.getByTestId<HTMLSelectElement>(DEVICE_SELECT_TESTID).value).toBe('sysdefault');
    expect(screen.queryByRole('button', { name: APPLY_KEY })).toBeNull();

    await fireEvent.change(screen.getByTestId(DEVICE_SELECT_TESTID), {
      target: { value: 'usb-2' },
    });
    await waitFor(() => {
      expect(
        screen.getByTestId(SAMPLE_RATE_SELECT_TESTID).querySelector('option[value="96000"]')
      ).not.toBeNull();
    });
    await fireEvent.change(screen.getByTestId(SAMPLE_RATE_SELECT_TESTID), {
      target: { value: '96000' },
    });
    await fireEvent.click(screen.getByRole('button', { name: APPLY_KEY }));
    expect(onCommit).toHaveBeenCalledExactlyOnceWith(
      { ...baseSource, device: 'usb-2', sampleRate: 96000 },
      { field: `audio:sources:${ROW_ID}:capture` }
    );
  });

  it('commits gain with the debounce of a continuous control', async () => {
    const { onCommit } = renderCard();
    const slider = screen.getByRole('slider');

    await fireEvent.input(slider, { target: { value: '6' } });
    expect(onCommit).toHaveBeenLastCalledWith(
      { ...baseSource, gain: 6 },
      { field: `audio:sources:${ROW_ID}:gain`, debounceMs: AUTOSAVE_DEBOUNCE_MS }
    );
  });

  it('commits a model toggle at once with the complete list', async () => {
    const { onCommit } = renderCard();

    await fireEvent.click(screen.getByRole('switch', { name: 'Perch v2' }));
    expect(onCommit).toHaveBeenLastCalledWith(
      { ...baseSource, models: ['birdnet', 'perch_v2'] },
      { field: `audio:sources:${ROW_ID}:models` }
    );
  });

  it('asks for confirmation before deleting', async () => {
    const { onDelete } = renderCard({ expanded: false });

    await fireEvent.click(screen.getByRole('button', { name: 'common.delete' }));
    expect(onDelete).not.toHaveBeenCalled();
    const dialog = screen.getByRole('alertdialog');
    expect(dialog).toBeInTheDocument();

    const confirm = Array.from(dialog.querySelectorAll('button')).find(
      button => button.textContent.trim() === 'common.delete'
    );
    expect(confirm).toBeDefined();
    if (confirm) await fireEvent.click(confirm);
    expect(onDelete).toHaveBeenCalledOnce();
  });

  it('passes the draft rate, not the saved one, to the equalizer preview', async () => {
    stubElementAnimate();
    onTestFinished(restoreElementAnimate);
    renderCard();

    await fireEvent.click(
      screen.getByRole('button', { name: 'settings.audio.audioFilters.title' })
    );
    expect(screen.getByTestId('audio-equalizer-settings')).toHaveTextContent('48000');

    await fireEvent.change(screen.getByTestId(DEVICE_SELECT_TESTID), {
      target: { value: 'usb-2' },
    });
    await waitFor(() => {
      expect(
        screen.getByTestId(SAMPLE_RATE_SELECT_TESTID).querySelector('option[value="96000"]')
      ).not.toBeNull();
    });
    await fireEvent.change(screen.getByTestId(SAMPLE_RATE_SELECT_TESTID), {
      target: { value: '96000' },
    });
    expect(screen.getByTestId('audio-equalizer-settings')).toHaveTextContent('96000');
  });
});
