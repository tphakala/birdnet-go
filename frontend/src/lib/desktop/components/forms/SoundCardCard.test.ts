import { describe, it, expect, vi, beforeEach, afterEach, onTestFinished } from 'vitest';
import { screen, cleanup, fireEvent } from '@testing-library/svelte';
import { createComponentTestFactory } from '../../../../test/render-helpers';
import {
  restoreElementAnimate,
  stubElementAnimate,
} from '../../../../test/fixtures/elementAnimate';
import {
  DEVICE_SELECT_TESTID,
  makeAbortableProbe,
} from '../../../../test/fixtures/soundCardProbeTest';
import type { AudioSourceConfig } from '$lib/stores/settings';
import type { SoundCardProblem } from './sourceRules';
import type { AcousticModelAvailability } from '$lib/types/models';
import type { BackendModel } from '$lib/stores/models.svelte';

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
const WARNING_BADGE_KEY = 'settings.audio.models.warningBadge';

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
  problem?: SoundCardProblem;
}

function renderCard(options: RenderOptions = {}) {
  const source = options.source ?? baseSource;
  const onCommit = vi.fn();
  const onDelete = vi.fn();
  const onToggleExpanded = vi.fn();
  const result = createComponentTestFactory(SoundCardCard, {
    source,
    sources: options.sources ?? [source],
    audioDevices,
    modelOptions,
    availableModels: options.models ?? availableModels,
    disabled: false,
    problem: options.problem,
    expanded: options.expanded ?? true,
    onToggleExpanded,
    onChange: onCommit,
    onDelete,
  }).render();
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

describe('SoundCardCard editing', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    acousticModelAvailability.mockReturnValue({ kind: 'unknown' });
  });

  afterEach(() => {
    cleanup();
  });

  it('writes the whole source with the name as typed and the trimmed name when the field is left', async () => {
    const { onCommit } = renderCard();
    const input = nameInput();

    await fireEvent.input(input, { target: { value: 'Back yard ' } });
    expect(onCommit).toHaveBeenLastCalledWith({ ...baseSource, name: 'Back yard ' });

    await fireEvent.blur(input);
    expect(onCommit).toHaveBeenCalledTimes(2);
    expect(onCommit).toHaveBeenLastCalledWith({ ...baseSource, name: 'Back yard' });
  });

  it('writes nothing on blur when the name is already trimmed and unchanged', async () => {
    const { onCommit } = renderCard();

    await fireEvent.blur(nameInput());
    expect(onCommit).not.toHaveBeenCalled();
  });

  it('marks the name field required for assistive technology without the native attribute', () => {
    renderCard();

    expect(nameInput()).toHaveAttribute('aria-required', 'true');
    expect(nameInput()).not.toHaveAttribute('required');
  });

  it.each([
    ['required', 'settings.audio.soundCards.errors.nameRequired'],
    ['duplicate', 'settings.audio.soundCards.errors.duplicateName'],
  ] as const)(
    'shows a %s name problem that arrives while typing only once the field was left',
    async (problem, message) => {
      const view = renderCard();
      const input = nameInput();

      await fireEvent.input(input, { target: { value: 'Typed' } });
      await view.rerender({ problem: { name: problem } });
      expect(input).not.toHaveAttribute('aria-invalid');

      await fireEvent.blur(input);
      expect(input).toHaveAttribute('aria-invalid', 'true');
      expect(input).toHaveAccessibleDescription(message);

      await view.rerender({ problem: {} });
      expect(input).not.toHaveAttribute('aria-invalid');
    }
  );

  it('shows a duplicate made on another card at once, without the field being left', async () => {
    const view = renderCard();

    await view.rerender({ problem: { name: 'duplicate' } });

    expect(nameInput()).toHaveAttribute('aria-invalid', 'true');
    expect(nameInput()).toHaveAccessibleDescription(
      'settings.audio.soundCards.errors.duplicateName'
    );
  });

  it('hides a shown name error while the field is edited and shows it again when left', async () => {
    renderCard({ problem: { name: 'duplicate' } });
    const input = nameInput();
    expect(input).toHaveAttribute('aria-invalid', 'true');

    await fireEvent.input(input, { target: { value: 'Still taken' } });
    expect(input).not.toHaveAttribute('aria-invalid');

    await fireEvent.blur(input);
    expect(input).toHaveAttribute('aria-invalid', 'true');
  });

  it('shows a duplicate from another card after a save interrupted an edit of the name', async () => {
    const view = renderCard();
    // Typing, then a save disables the field: focus leaves without a blur event.
    await fireEvent.input(nameInput(), { target: { value: 'Typed' } });
    await view.rerender({ disabled: true });
    await view.rerender({ disabled: false });

    await view.rerender({ problem: { name: 'duplicate' } });

    expect(nameInput()).toHaveAttribute('aria-invalid', 'true');
  });

  it('shows a problem at once on a card that opens with one, and flags the header', async () => {
    renderCard({ problem: { name: 'duplicate' } });

    expect(nameInput()).toHaveAttribute('aria-invalid', 'true');
    expect(screen.getByText('settings.audio.validation.needsAttention')).toBeInTheDocument();
  });

  it.each([
    ['required', 'settings.audio.soundCards.errors.deviceRequired'],
    ['duplicate', 'settings.audio.soundCards.errors.duplicateDevice'],
  ] as const)('shows the %s device problem text in the card', (problem, message) => {
    renderCard({ problem: { device: problem } });

    expect(screen.getByRole('alert')).toHaveTextContent(message);
    const device = screen.getByTestId(DEVICE_SELECT_TESTID);
    expect(device).toHaveAttribute('aria-invalid', 'true');
    expect(device).toHaveAccessibleDescription(message);
  });

  it('shows no device message without a device problem', () => {
    renderCard({ problem: { name: 'required' } });

    expect(screen.queryByText(/soundCards\.errors\.(deviceRequired|duplicateDevice)/)).toBeNull();
    expect(screen.getByTestId(DEVICE_SELECT_TESTID)).not.toHaveAttribute('aria-invalid');
  });

  it('shows no attention badge without a problem', () => {
    renderCard();

    expect(screen.queryByText('settings.audio.validation.needsAttention')).toBeNull();
  });

  it('writes the gain once on release, not while dragging, with the whole source', async () => {
    const { onCommit } = renderCard();
    const slider = screen.getByRole('slider');

    await fireEvent.input(slider, { target: { value: '3' } });
    await fireEvent.input(slider, { target: { value: '6' } });
    expect(onCommit).not.toHaveBeenCalled();

    await fireEvent.change(slider, { target: { value: '6' } });
    expect(onCommit).toHaveBeenCalledExactlyOnceWith({ ...baseSource, gain: 6 });
  });

  it('writes a model toggle with the complete list', async () => {
    const { onCommit } = renderCard();

    await fireEvent.click(screen.getByRole('switch', { name: 'Perch v2' }));
    expect(onCommit).toHaveBeenLastCalledWith({ ...baseSource, models: ['birdnet', 'perch_v2'] });
  });

  it('asks for confirmation before deleting', async () => {
    const { onDelete } = renderCard({ expanded: false });

    await fireEvent.click(screen.getByRole('button', { name: 'common.delete' }));
    expect(onDelete).not.toHaveBeenCalled();
    const dialog = screen.getByRole('alertdialog');
    expect(dialog).toBeInTheDocument();
    expect(dialog).toHaveAccessibleDescription('Remove sound card source "Living room"?');

    const confirm = Array.from(dialog.querySelectorAll('button')).find(
      button => button.textContent.trim() === 'common.delete'
    );
    expect(confirm).toBeDefined();
    if (confirm) await fireEvent.click(confirm);
    expect(onDelete).toHaveBeenCalledOnce();
  });

  it('passes the source rate to the equalizer preview', async () => {
    stubElementAnimate();
    onTestFinished(restoreElementAnimate);
    renderCard({ source: { ...baseSource, sampleRate: 96000 } });

    await fireEvent.click(
      screen.getByRole('button', { name: 'settings.audio.audioFilters.title' })
    );
    expect(screen.getByTestId('audio-equalizer-settings')).toHaveTextContent('96000');
  });

  it('shows body group titles as group labels and single-control titles as field labels', () => {
    renderCard();

    for (const name of [
      'settings.audio.soundCards.captureTitle',
      'settings.audio.soundCards.modelLabel',
      'settings.audio.quietHours.title',
    ]) {
      expect(screen.getByRole('heading', { level: 4, name })).toHaveClass('uppercase');
    }
    expect(screen.getByRole('button', { name: 'settings.audio.audioFilters.title' })).toHaveClass(
      'uppercase',
      'flex',
      'items-center',
      'gap-2'
    );
    expect(
      screen.getByRole('heading', { level: 4, name: 'settings.audio.soundCards.levelTitle' })
    ).not.toHaveClass('uppercase');
    expect(
      screen.getByText('settings.audio.soundCards.nameLabel', { selector: 'label' })
    ).not.toHaveClass('uppercase');
  });

  it('names the audio filters region after its disclosure button', () => {
    renderCard();

    expect(
      screen.getByRole('region', { name: 'settings.audio.audioFilters.title' })
    ).toBeInTheDocument();
  });
});
