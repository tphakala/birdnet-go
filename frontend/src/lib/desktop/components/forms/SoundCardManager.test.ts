import { describe, it, expect, vi, beforeEach, afterEach, onTestFinished } from 'vitest';
import { screen, cleanup, fireEvent, waitFor, within } from '@testing-library/svelte';
import { createComponentTestFactory, renderTyped } from '../../../../test/render-helpers';
import { deferred } from '../../../../test/async-helpers';
import {
  restoreElementAnimate,
  stubElementAnimate,
} from '../../../../test/fixtures/elementAnimate';
import SoundCardManager from './SoundCardManager.svelte';
import { AUDIO_SOURCES_LIST_FIELD } from './sourceFields';
import type { AudioSourceConfig } from '$lib/stores/settings';
import {
  ADD_SOURCE_KEY,
  ABORT_ERROR,
  DEVICE_SELECT_TESTID,
  SAMPLE_RATE_SELECT_TESTID,
  RATE_48K,
  RATE_96K,
  RATE_192K,
  makeAbortableProbe,
  settle,
  type RateOption,
} from '../../../../test/fixtures/soundCardProbeTest';

// Functional stand-in for SelectDropdown: renders a native <select> wired to the
// same `onChange` prop and exposes `options` as real <option> elements. This lets
// us drive device selection and assert on the probed sample-rate options without
// driving the real portal-based dropdown UI.
vi.mock('./SelectDropdown.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockSelectDropdown.svelte')).default,
}));

// The name field stays functional (bind:value) so the add-flow save test can
// type a name and enable the Add button; other heavy children are inert.
vi.mock('./TextInput.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockTextInput.svelte')).default,
}));
vi.mock('./InlineSlider.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockEmpty.svelte')).default,
}));
// The model list renders the current selection as text so the default model
// pre-selection of the add form can be asserted without the real checkbox UI.
vi.mock('./ModelToggleList.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockModelCheckboxList.svelte')).default,
}));
vi.mock('./QuietHoursEditor.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockEmpty.svelte')).default,
}));
vi.mock('$lib/desktop/features/settings/components/AudioEqualizerSettings.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockAudioEqualizerSettings.svelte')).default,
}));

// Avoid the models store touching the network during mount.
vi.mock('$lib/stores/models.svelte', () => ({
  getAvailableModels: vi.fn(() => []),
  DEFAULT_MODEL_ID: 'birdnet',
  watchModels: vi.fn(() => () => {}),
  modelsLoaded: vi.fn(() => true),
  modelsLoading: vi.fn(() => false),
  modelsFailed: vi.fn(() => false),
  refreshModels: vi.fn(),
}));

// The acoustic model verdict drives the add form's default pick; the pure
// defaultModelSelection helper stays real so the mapping itself is exercised.
vi.mock('$lib/stores/acousticModels.svelte', () => ({
  subscribeAcousticModels: vi.fn(() => () => {}),
  acousticModelAvailability: vi.fn(() => ({ kind: 'unknown' })),
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

import { getAvailableModels, watchModels } from '$lib/stores/models.svelte';
import {
  autoSaveStatus,
  commitSetting,
  resetAutoSaveForTest,
  type CommitOptions,
} from '$lib/stores/settingsAutoSave.svelte';
import { acousticModelAvailability } from '$lib/stores/acousticModels.svelte';

const USB_MIC = 'usb-mic';
const SLOW_DEV = 'slow-dev';
const FAST_DEV = 'fast-dev';

// The slow probe resolves later than the fast one and IGNORES its abort,
// simulating a response that was already in flight when its controller was
// aborted (the stale-result race the supersession guard defends against).
const SLOW_PROBE_MS = 40;
const STALE_SETTLE_MS = 80;

// Per-device probe behaviour, mirroring the real utility's contract.
function probeFor(deviceId: string, signal: AbortSignal | undefined) {
  if (deviceId === SLOW_DEV) {
    return new Promise<{ options: RateOption[]; verified: boolean }>((resolve, reject) => {
      if (signal?.aborted) {
        reject(ABORT_ERROR());
        return;
      }
      setTimeout(() => resolve({ options: [RATE_48K], verified: true }), SLOW_PROBE_MS);
    });
  }
  if (deviceId === FAST_DEV) {
    return makeAbortableProbe(signal, [RATE_48K, RATE_96K]);
  }
  return makeAbortableProbe(signal, [RATE_48K, RATE_96K, RATE_192K]);
}

// Keep the real helpers (coerceSupportedRate, etc.); only stub the network probe.
vi.mock('$lib/utils/audio/sampleRate', async importActual => ({
  ...(await importActual<typeof import('$lib/utils/audio/sampleRate')>()),
  fetchDeviceCapabilities: vi.fn((deviceId: string, signal?: AbortSignal) =>
    probeFor(deviceId, signal)
  ),
}));

describe('SoundCardManager sample rate probe (issue #3593)', () => {
  function renderManager(
    audioDevices: Array<{
      index: number;
      name: string;
      id: string;
      stableId?: string;
      busPath?: string;
    }>,
    sources: AudioSourceConfig[] = []
  ) {
    return renderTyped(SoundCardManager, {
      props: {
        sources,
        audioDevices,
        audioDevicesLoading: false,
        disabled: false,
        onUpdateSources: vi.fn(),
        onRefreshDevices: vi.fn(),
      },
    });
  }

  async function openAddForm() {
    await fireEvent.click(screen.getByText(ADD_SOURCE_KEY));
  }

  function rateSelect() {
    return screen.getByTestId(SAMPLE_RATE_SELECT_TESTID);
  }

  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
  });

  it('populates the sample-rate dropdown after selecting a device for a new source', async () => {
    renderManager([{ index: 0, name: 'USB Mic', id: USB_MIC }]);
    await openAddForm();

    const deviceSelect = await screen.findByTestId(DEVICE_SELECT_TESTID);
    await fireEvent.change(deviceSelect, { target: { value: USB_MIC } });

    // The probed rates beyond the 48 kHz default must become selectable.
    await waitFor(() => {
      expect(rateSelect().querySelector(`option[value="${RATE_96K.value}"]`)).not.toBeNull();
    });
    expect(rateSelect().querySelector(`option[value="${RATE_192K.value}"]`)).not.toBeNull();
  });

  it('passes the selected sample rate to the equalizer preview', async () => {
    stubElementAnimate();
    onTestFinished(restoreElementAnimate);
    renderManager([{ index: 0, name: 'USB Mic', id: USB_MIC }]);
    await openAddForm();
    await fireEvent.click(
      screen.getByRole('button', { name: 'settings.audio.audioFilters.title' })
    );
    expect(screen.getByTestId('audio-equalizer-settings')).toHaveTextContent('48000');

    const deviceSelect = await screen.findByTestId(DEVICE_SELECT_TESTID);
    await fireEvent.change(deviceSelect, { target: { value: USB_MIC } });
    await waitFor(() => {
      expect(rateSelect().querySelector(`option[value="${RATE_96K.value}"]`)).not.toBeNull();
    });
    await fireEvent.change(rateSelect(), { target: { value: RATE_96K.value } });

    expect(screen.getByTestId('audio-equalizer-settings')).toHaveTextContent('96000');
  });

  it('persists the stable USB token, not the unstable ALSA index (GH #3651)', async () => {
    renderManager([
      {
        index: 0,
        name: 'USB Audio Device',
        id: ':1,0',
        stableId: 'usb-path:usb-xhci-hcd.0-1',
        busPath: 'usb-xhci-hcd.0-1',
      },
    ]);
    await openAddForm();

    const deviceSelect = await screen.findByTestId(DEVICE_SELECT_TESTID);
    // The option value must be the reboot-stable token, not the unstable :1,0 index.
    const opt = deviceSelect.querySelector('option[value="usb-path:usb-xhci-hcd.0-1"]');
    expect(opt).not.toBeNull();
    expect(deviceSelect.querySelector('option[value=":1,0"]')).toBeNull();
    // A single uniquely-named device shows just its name, not the bus path jargon.
    expect(opt?.textContent).toContain('USB Audio Device');
    expect(opt?.textContent).not.toContain('usb-xhci-hcd.0-1');
  });

  it('appends the bus path only to disambiguate identical device names', async () => {
    renderManager([
      {
        index: 0,
        name: 'USB Audio Device',
        id: ':1,0',
        stableId: 'usb-path:portA',
        busPath: 'portA',
      },
      {
        index: 1,
        name: 'USB Audio Device',
        id: ':2,0',
        stableId: 'usb-path:portB',
        busPath: 'portB',
      },
    ]);
    await openAddForm();

    const deviceSelect = await screen.findByTestId(DEVICE_SELECT_TESTID);
    const optA = deviceSelect.querySelector('option[value="usb-path:portA"]');
    const optB = deviceSelect.querySelector('option[value="usb-path:portB"]');
    expect(optA?.textContent).toContain('(portA)');
    expect(optB?.textContent).toContain('(portB)');
  });

  it('hides a device already configured by either its legacy id or stable token', async () => {
    renderManager(
      [
        {
          index: 0,
          name: 'USB Audio A',
          id: ':1,0',
          stableId: 'usb-path:portA',
          busPath: 'portA',
        },
        {
          index: 1,
          name: 'USB Audio B',
          id: ':2,0',
          stableId: 'usb-path:portB',
          busPath: 'portB',
        },
      ],
      // A is configured under its legacy index; B is unconfigured.
      [{ name: 'mic-a', device: ':1,0', gain: 1, models: [] }]
    );
    await openAddForm();

    // The configured source's card is open too, so read the add form's own dropdown.
    const addForm = screen.getByRole('heading', { name: ADD_SOURCE_KEY }).parentElement;
    expect(addForm).not.toBeNull();
    const deviceSelect = within(addForm ?? document.body).getByTestId(DEVICE_SELECT_TESTID);
    // Device A is fully excluded under BOTH identifier forms (it must not reappear
    // as its legacy id either), while the unconfigured B remains selectable.
    expect(deviceSelect.querySelector('option[value="usb-path:portA"]')).toBeNull();
    expect(deviceSelect.querySelector('option[value=":1,0"]')).toBeNull();
    expect(deviceSelect.querySelector('option[value="usb-path:portB"]')).not.toBeNull();
  });

  it('ignores a stale probe that resolves after a newer device was selected', async () => {
    renderManager([
      { index: 0, name: 'Slow Mic', id: SLOW_DEV },
      { index: 1, name: 'Fast Mic', id: FAST_DEV },
    ]);
    await openAddForm();

    const deviceSelect = await screen.findByTestId(DEVICE_SELECT_TESTID);
    // Select the slow device, then immediately switch to the fast one. The slow
    // probe resolves later and ignores its abort, so without the supersession
    // guard it would clobber the fast device's options.
    await fireEvent.change(deviceSelect, { target: { value: SLOW_DEV } });
    await fireEvent.change(deviceSelect, { target: { value: FAST_DEV } });

    await waitFor(() => {
      expect(rateSelect().querySelector(`option[value="${RATE_96K.value}"]`)).not.toBeNull();
    });

    // Let the stale slow-dev probe resolve; the fast device's options must survive.
    await settle(STALE_SETTLE_MS);
    expect(rateSelect().querySelector(`option[value="${RATE_96K.value}"]`)).not.toBeNull();
  });
});

describe('SoundCardManager default model pre-selection (model de-privilege, N=0)', () => {
  const MODEL_LIST_TESTID = 'model-checkbox-list';

  // Listed Perch-first so registry-ID order (v2.4 first) is distinguishable
  // from option order in the mapped selection.
  const ENABLED_MODELS = [
    { id: 'perch_v2', registryId: 'Perch_V2', name: 'Perch v2', category: 'bird' },
    { id: 'birdnet', registryId: 'BirdNET_V2.4', name: 'BirdNET v2.4', category: 'bird' },
  ];

  function renderAndOpen() {
    renderTyped(SoundCardManager, {
      props: {
        sources: [],
        audioDevices: [{ index: 0, name: 'USB Mic', id: USB_MIC }],
        audioDevicesLoading: false,
        disabled: false,
        onUpdateSources: vi.fn(),
        onRefreshDevices: vi.fn(),
      },
    });
    return fireEvent.click(screen.getByText(ADD_SOURCE_KEY));
  }

  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
    vi.mocked(getAvailableModels).mockReturnValue([]);
    vi.mocked(acousticModelAvailability).mockReturnValue({ kind: 'unknown' });
  });

  it('pre-selects the mapped default targets, v2.4 first, when the classifier is ready', async () => {
    vi.mocked(getAvailableModels).mockReturnValue(ENABLED_MODELS);
    vi.mocked(acousticModelAvailability).mockReturnValue({
      kind: 'ready',
      defaultTargets: ['BirdNET_V2.4', 'Perch_V2'],
    });

    await renderAndOpen();

    expect(screen.getByTestId(MODEL_LIST_TESTID)).toHaveTextContent('birdnet,perch_v2');
  });

  it('keeps the model list live while mounted, through watchModels', () => {
    renderTyped(SoundCardManager, {
      props: {
        sources: [],
        audioDevices: [],
        audioDevicesLoading: false,
        disabled: false,
        onUpdateSources: vi.fn(),
        onRefreshDevices: vi.fn(),
      },
    });

    expect(watchModels).toHaveBeenCalledTimes(1);
  });

  it('labels the classifier summary of a source with the live backend', () => {
    vi.mocked(getAvailableModels).mockReturnValue([
      {
        id: 'birdnet',
        registryId: 'BirdNET_V2.4',
        name: 'BirdNET v2.4',
        category: 'bird',
        backend: 'ONNX',
      },
    ]);

    renderTyped(SoundCardManager, {
      props: {
        sources: [{ name: 'Garden', device: USB_MIC, gain: 0, models: ['birdnet'] }],
        audioDevices: [{ index: 0, name: 'USB Mic', id: USB_MIC }],
        audioDevicesLoading: false,
        disabled: false,
        onUpdateSources: vi.fn(),
        onRefreshDevices: vi.fn(),
      },
    });

    expect(screen.getByText('BirdNET v2.4 (ONNX)')).toBeInTheDocument();
  });

  it('pre-selects nothing at N=0 and never invents a phantom BirdNET', async () => {
    vi.mocked(getAvailableModels).mockReturnValue([]);
    vi.mocked(acousticModelAvailability).mockReturnValue({
      kind: 'none',
      reason: 'none_installed',
    });

    await renderAndOpen();

    expect(screen.getByTestId(MODEL_LIST_TESTID).textContent).toBe('');
  });

  it('completing the add flow at N=0 saves models:[] with no phantom BirdNET', async () => {
    vi.mocked(getAvailableModels).mockReturnValue([]);
    vi.mocked(acousticModelAvailability).mockReturnValue({
      kind: 'none',
      reason: 'none_installed',
    });

    const onUpdateSources = vi.fn();
    renderTyped(SoundCardManager, {
      props: {
        sources: [],
        audioDevices: [{ index: 0, name: 'USB Mic', id: USB_MIC }],
        audioDevicesLoading: false,
        disabled: false,
        onUpdateSources,
        onRefreshDevices: vi.fn(),
      },
    });

    // Open the add form; the pre-selection must be empty at N=0.
    await fireEvent.click(screen.getByText(ADD_SOURCE_KEY));
    expect(screen.getByTestId(MODEL_LIST_TESTID).textContent).toBe('');

    // Pick a device and name the source so the Add button enables.
    const deviceSelect = await screen.findByTestId(DEVICE_SELECT_TESTID);
    await fireEvent.change(deviceSelect, { target: { value: USB_MIC } });
    await fireEvent.input(screen.getByTestId('new-soundcard-name'), {
      target: { value: 'Mic 1' },
    });

    // Complete the add flow via the Add button (not the heading of the same text).
    await fireEvent.click(screen.getByRole('button', { name: ADD_SOURCE_KEY }));

    // One save, under the list field, carrying the whole array with the new source.
    expect(onUpdateSources).toHaveBeenCalledTimes(1);
    expect(onUpdateSources.mock.calls[0][1]).toEqual({ field: AUDIO_SOURCES_LIST_FIELD });
    const savedSources = onUpdateSources.mock.calls[0][0] as AudioSourceConfig[];
    expect(savedSources).toHaveLength(1);
    // The saved source carries an empty model list; no default is invented.
    expect(savedSources[0].models).toEqual([]);
  });

  it('falls back to the legacy BirdNET pick when no verdict is available', async () => {
    vi.mocked(getAvailableModels).mockReturnValue(ENABLED_MODELS);
    vi.mocked(acousticModelAvailability).mockReturnValue({ kind: 'unknown' });

    await renderAndOpen();

    expect(screen.getByTestId(MODEL_LIST_TESTID)).toHaveTextContent('birdnet');
    expect(screen.getByTestId(MODEL_LIST_TESTID)).not.toHaveTextContent('perch_v2');
  });
});

describe('SoundCardManager rows', () => {
  const devices = [
    { index: 0, name: 'Mic A', id: 'dev-a' },
    { index: 1, name: 'Mic B', id: 'dev-b' },
    { index: 2, name: 'Mic C', id: 'dev-c' },
  ];
  const sourceA: AudioSourceConfig = { name: 'A', device: 'dev-a', gain: 0, models: [] };
  const sourceB: AudioSourceConfig = { name: 'B', device: 'dev-b', gain: 0, models: [] };
  const sourceC: AudioSourceConfig = { name: 'C', device: 'dev-c', gain: 0, models: [] };

  function renderRows(sources: AudioSourceConfig[]) {
    const onUpdateSources = vi.fn();
    const view = createComponentTestFactory(SoundCardManager, {
      sources,
      audioDevices: devices,
      audioDevicesLoading: false,
      disabled: false,
      onUpdateSources,
      onRefreshDevices: vi.fn(),
    }).render();
    return { ...view, onUpdateSources };
  }

  function header(name: string): HTMLElement {
    // The header button is named by the source name followed by its device.
    return screen.getByRole('button', { name: accessible => accessible.startsWith(`${name} `) });
  }

  beforeEach(() => {
    vi.clearAllMocks();
    stubElementAnimate();
    vi.mocked(getAvailableModels).mockReturnValue([]);
    vi.mocked(acousticModelAvailability).mockReturnValue({
      kind: 'none',
      reason: 'none_installed',
    });
  });

  afterEach(() => {
    cleanup();
    restoreElementAnimate();
  });

  it('opens a single source and keeps several closed', () => {
    renderRows([sourceA]);
    expect(header('A')).toHaveAttribute('aria-expanded', 'true');
    cleanup();

    renderRows([sourceA, sourceB]);
    expect(header('A')).toHaveAttribute('aria-expanded', 'false');
    expect(header('B')).toHaveAttribute('aria-expanded', 'false');
  });

  it('keeps a row open when its source changes, since rows are keyed by their own id', async () => {
    const { rerender } = renderRows([sourceA, sourceB]);
    await fireEvent.click(header('B'));
    expect(header('B')).toHaveAttribute('aria-expanded', 'true');

    // A saved device change replaces the source object; the row id stays.
    await rerender({ sources: [sourceA, { ...sourceB, device: 'dev-c' }] });
    expect(header('B')).toHaveAttribute('aria-expanded', 'true');
  });

  it('gives every row a fresh id when the list length changes from outside', async () => {
    const { rerender } = renderRows([sourceA, sourceB]);
    await fireEvent.click(header('B'));

    await rerender({ sources: [sourceA, sourceB, sourceC] });
    expect(header('B')).toHaveAttribute('aria-expanded', 'false');
  });

  it('keeps the open state of every card when a refused add takes the list back', async () => {
    const { rerender } = renderRows([sourceA, sourceB]);
    await fireEvent.click(header('B'));

    await fireEvent.click(screen.getByText(ADD_SOURCE_KEY));
    // The open card has a device select of its own; the add form comes last.
    const deviceSelect = (await screen.findAllByTestId(DEVICE_SELECT_TESTID)).at(-1);
    if (deviceSelect) await fireEvent.change(deviceSelect, { target: { value: 'dev-c' } });
    await fireEvent.input(screen.getByTestId('new-soundcard-name'), { target: { value: 'C' } });
    await fireEvent.click(screen.getByRole('button', { name: ADD_SOURCE_KEY }));

    // The list grows at once, then the refusal takes it back.
    await rerender({ sources: [sourceA, sourceB, sourceC] });
    await rerender({ sources: [sourceA, sourceB] });

    expect(header('A')).toHaveAttribute('aria-expanded', 'false');
    expect(header('B')).toHaveAttribute('aria-expanded', 'true');
  });

  it('brings a refused delete back with its open state', async () => {
    const { rerender } = renderRows([sourceA, sourceB]);
    await fireEvent.click(header('A'));
    await fireEvent.click(header('B'));

    const deleteButtons = screen.getAllByRole('button', { name: 'common.delete' });
    await fireEvent.click(deleteButtons[0]);
    const dialog = screen.getByRole('alertdialog');
    const confirm = Array.from(dialog.querySelectorAll('button')).find(
      button => button.textContent.trim() === 'common.delete'
    );
    if (confirm) await fireEvent.click(confirm);

    await rerender({ sources: [sourceB] });
    await rerender({ sources: [sourceA, sourceB] });

    expect(header('A')).toHaveAttribute('aria-expanded', 'true');
    expect(header('B')).toHaveAttribute('aria-expanded', 'true');
  });

  it('brings the original rows back when a refused save undoes two deletes made before its result', async () => {
    const { rerender } = renderRows([sourceA, sourceB, sourceC]);
    await fireEvent.click(header('A'));
    await fireEvent.click(header('B'));

    async function deleteFirstCard() {
      const deleteButtons = screen.getAllByRole('button', { name: 'common.delete' });
      await fireEvent.click(deleteButtons[0]);
      const dialog = screen.getByRole('alertdialog');
      const confirm = Array.from(dialog.querySelectorAll('button')).find(
        button => button.textContent.trim() === 'common.delete'
      );
      if (confirm) await fireEvent.click(confirm);
    }

    // The engine coalesces the second delete into the save that is still running.
    await deleteFirstCard();
    await rerender({ sources: [sourceB, sourceC] });
    await deleteFirstCard();
    await rerender({ sources: [sourceC] });

    // The refusal takes the list back to where it was before the first delete.
    await rerender({ sources: [sourceA, sourceB, sourceC] });

    expect(header('A')).toHaveAttribute('aria-expanded', 'true');
    expect(header('B')).toHaveAttribute('aria-expanded', 'true');
    expect(header('C')).toHaveAttribute('aria-expanded', 'false');
  });

  it('deletes a source after confirmation with one list save', async () => {
    const { onUpdateSources } = renderRows([sourceA, sourceB]);

    const deleteButtons = screen.getAllByRole('button', { name: 'common.delete' });
    await fireEvent.click(deleteButtons[0]);
    const dialog = screen.getByRole('alertdialog');
    const confirm = Array.from(dialog.querySelectorAll('button')).find(
      button => button.textContent.trim() === 'common.delete'
    );
    if (confirm) await fireEvent.click(confirm);

    expect(onUpdateSources).toHaveBeenCalledExactlyOnceWith([sourceB], {
      field: AUDIO_SOURCES_LIST_FIELD,
    });
  });
});

describe('SoundCardManager add outcome', () => {
  const NAME_TESTID = 'new-soundcard-name';
  const SAVED = { saved: [['sources']], dropped: [], failed: [], remaining: false };
  const REFUSED = {
    saved: [],
    dropped: [],
    failed: [['sources']],
    remaining: false,
    error: new Error('no'),
  };

  beforeEach(() => {
    vi.clearAllMocks();
    resetAutoSaveForTest();
    store.stageSectionEdit.mockReset();
    store.saveStagedSection.mockReset();
    vi.mocked(getAvailableModels).mockReturnValue([]);
    vi.mocked(acousticModelAvailability).mockReturnValue({
      kind: 'none',
      reason: 'none_installed',
    });
  });

  afterEach(() => {
    cleanup();
    resetAutoSaveForTest();
  });

  async function addMic() {
    renderTyped(SoundCardManager, {
      props: {
        sources: [],
        audioDevices: [{ index: 0, name: 'USB Mic', id: USB_MIC }],
        audioDevicesLoading: false,
        disabled: false,
        onUpdateSources: (sources: AudioSourceConfig[], options: CommitOptions) =>
          commitSetting('audio', { sources }, options),
        onRefreshDevices: vi.fn(),
      },
    });
    await fireEvent.click(screen.getByText(ADD_SOURCE_KEY));
    await fireEvent.change(await screen.findByTestId(DEVICE_SELECT_TESTID), {
      target: { value: USB_MIC },
    });
    await fireEvent.input(screen.getByTestId(NAME_TESTID), { target: { value: 'Mic 1' } });
    await fireEvent.click(screen.getByRole('button', { name: ADD_SOURCE_KEY }));
  }

  it('shows the busy label and blocks a second add while the new source is saved', async () => {
    const pending = deferred<typeof SAVED>();
    store.saveStagedSection.mockReturnValueOnce(pending.promise);

    await addMic();

    const busy = await screen.findByRole('button', { name: 'settings.audio.soundCards.adding' });
    expect(busy).toHaveAttribute('aria-disabled', 'true');
    await fireEvent.click(busy);
    expect(store.stageSectionEdit).toHaveBeenCalledOnce();

    pending.resolve(SAVED);
    await waitFor(() => expect(screen.queryByTestId(NAME_TESTID)).toBeNull());
  });

  it('closes the add form once the new source is saved', async () => {
    store.saveStagedSection.mockResolvedValue(SAVED);

    await addMic();

    await waitFor(() => expect(screen.queryByTestId(NAME_TESTID)).toBeNull());
  });

  it('keeps the add form open with what was typed when the save is refused', async () => {
    store.saveStagedSection.mockResolvedValue(REFUSED);

    await addMic();

    await waitFor(() => expect(autoSaveStatus(AUDIO_SOURCES_LIST_FIELD).state).toBe('error'));
    expect(screen.getByTestId(NAME_TESTID)).toHaveValue('Mic 1');
  });
});
