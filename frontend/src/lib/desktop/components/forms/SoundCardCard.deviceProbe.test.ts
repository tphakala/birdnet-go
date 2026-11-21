import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { screen, cleanup, fireEvent, waitFor } from '@testing-library/svelte';
import { createComponentTestFactory } from '../../../../test/render-helpers';
import { deferred } from '../../../../test/async-helpers';
import {
  restoreElementAnimate,
  stubElementAnimate,
} from '../../../../test/fixtures/elementAnimate';
import SoundCardCard from './SoundCardCard.svelte';
import { fetchDeviceCapabilities } from '$lib/utils/audio/sampleRate';
import type { AudioSourceConfig } from '$lib/stores/settings';
import {
  ABORT_ERROR,
  DEFAULT_MODEL,
  DEVICE_SELECT_TESTID,
  SAMPLE_RATE_SELECT_TESTID,
  RATE_48K,
  RATE_96K,
  RATE_192K,
  RATE_384K,
  makeAbortableProbe,
  settle,
  type RateOption,
} from '../../../../test/fixtures/soundCardProbeTest';

// Functional stand-in for SelectDropdown so we can drive the device dropdown and
// read the probed sample-rate options as real <option> elements.
vi.mock('./SelectDropdown.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockSelectDropdown.svelte')).default,
}));
vi.mock('./InlineSlider.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockEmpty.svelte')).default,
}));
vi.mock('./ModelToggleList.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockEmpty.svelte')).default,
}));
vi.mock('./QuietHoursEditor.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockEmpty.svelte')).default,
}));
vi.mock('$lib/desktop/features/settings/components/AudioEqualizerSettings.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockEmpty.svelte')).default,
}));
vi.mock('$lib/stores/models.svelte', () => ({
  DEFAULT_MODEL_ID: 'birdnet',
  modelsLoaded: vi.fn(() => true),
  modelsLoading: vi.fn(() => false),
  modelsFailed: vi.fn(() => false),
  refreshModels: vi.fn(),
}));

// The card reads the acoustic model verdict for its default pick and badge; the
// device probe flow under test never depends on it.
vi.mock('$lib/stores/acousticModels.svelte', () => ({
  acousticModelAvailability: vi.fn(() => ({ kind: 'unknown' })),
}));

const DEV1 = 'dev1';
const DEV2 = 'dev2';

// dev1 supports 96 kHz, dev2 does not (but supports 192/384 kHz), so we can tell
// which device's capabilities landed and exercise the unsupported-rate reset.
function ratesFor(deviceId: string): RateOption[] {
  return deviceId === DEV2 ? [RATE_48K, RATE_192K, RATE_384K] : [RATE_48K, RATE_96K];
}

// Keep the real helpers (coerceSupportedRate, etc.); only stub the network probe.
vi.mock('$lib/utils/audio/sampleRate', async importActual => ({
  ...(await importActual<typeof import('$lib/utils/audio/sampleRate')>()),
  fetchDeviceCapabilities: vi.fn((deviceId: string, signal?: AbortSignal) =>
    makeAbortableProbe(signal, ratesFor(deviceId))
  ),
}));

describe('SoundCardCard device capability probe (issue #3593, open card)', () => {
  const audioDevices = [
    { index: 0, name: 'Mic One', id: DEV1 },
    { index: 1, name: 'Mic Two', id: DEV2 },
  ];
  const modelOptions = [{ value: DEFAULT_MODEL, label: 'BirdNET' }];
  const availableModels = [{ id: DEFAULT_MODEL, name: 'BirdNET', category: 'bird' }];

  // The card is controlled: what it writes goes back in as its source, as the
  // manager and the form do.
  function renderCard(sampleRate = Number(RATE_48K.value), device = DEV1) {
    const source: AudioSourceConfig = {
      name: 'Living room',
      device,
      sampleRate,
      gain: 0,
      models: [DEFAULT_MODEL],
    };
    const onChange = vi.fn();
    const view = createComponentTestFactory(SoundCardCard, {
      source,
      sources: [source],
      audioDevices,
      modelOptions,
      availableModels,
      disabled: false,
      expanded: false,
      onToggleExpanded: vi.fn(),
      onChange,
      onDelete: vi.fn(),
    }).render();
    // rerender merges over the initial props, so it must carry the open state.
    const state = { expanded: false };
    onChange.mockImplementation((next: AudioSourceConfig) => {
      void view.rerender({ source: next, sources: [next], expanded: state.expanded });
    });
    return { ...view, onChange, source, state };
  }

  beforeEach(() => {
    vi.clearAllMocks();
    // The body opens with a slide transition
    stubElementAnimate();
  });

  afterEach(() => {
    cleanup();
    restoreElementAnimate();
  });

  function rateSelect() {
    return screen.getByTestId<HTMLSelectElement>(SAMPLE_RATE_SELECT_TESTID);
  }

  function hasRate(value: string) {
    return rateSelect().querySelector(`option[value="${value}"]`) !== null;
  }

  async function pickDevice(device: string) {
    await fireEvent.change(screen.getByTestId(DEVICE_SELECT_TESTID), { target: { value: device } });
  }

  /** Makes the next probe wait until the test releases it; an abort rejects it. */
  function holdNextProbe(options: RateOption[]) {
    const held = deferred<{ options: RateOption[]; verified: boolean }>();
    vi.mocked(fetchDeviceCapabilities).mockImplementationOnce((_id, signal) => {
      signal?.addEventListener('abort', () => held.reject(ABORT_ERROR()));
      return held.promise;
    });
    return { release: () => held.resolve({ options, verified: true }) };
  }

  const settled = () => settle(10);

  // The card is controlled: the manager opens it by passing expanded.
  async function openCard(view: ReturnType<typeof renderCard>) {
    view.state.expanded = true;
    await view.rerender({ expanded: true });
  }

  it('probes the current device when the card opens and writes nothing', async () => {
    const view = renderCard();
    await openCard(view);

    // dev1 supports 96 kHz; opening the card must surface it (the documented workaround).
    await waitFor(() => expect(hasRate(RATE_96K.value)).toBe(true));
    await settled();
    expect(view.onChange).not.toHaveBeenCalled();
  });

  it('replaces the sample-rate options when changing device while the card is open', async () => {
    await openCard(renderCard());
    // Wait for dev1's probe to land first so the switch is a genuine replacement.
    await waitFor(() => expect(hasRate(RATE_96K.value)).toBe(true));

    await pickDevice(DEV2);

    // dev2's rates must replace dev1's: the new rates appear and dev1's exclusive
    // 96 kHz is gone (wholesale replacement, not a merge).
    await waitFor(() => expect(hasRate(RATE_192K.value)).toBe(true));
    expect(hasRate(RATE_384K.value)).toBe(true);
    expect(hasRate(RATE_96K.value)).toBe(false);
  });

  it('writes the device and a rate it supports in one change once its probe returns', async () => {
    // Source saved at 96 kHz, which dev1 supports but dev2 does not.
    const view = renderCard(Number(RATE_96K.value));
    await openCard(view);
    await waitFor(() => expect(rateSelect().value).toBe(RATE_96K.value));
    const probe = holdNextProbe([RATE_48K, RATE_192K, RATE_384K]);

    await pickDevice(DEV2);
    await settled();
    // The form keeps its device until the probe has returned.
    expect(view.onChange).not.toHaveBeenCalled();

    probe.release();
    await waitFor(() => expect(view.onChange).toHaveBeenCalledOnce());
    expect(view.onChange).toHaveBeenCalledWith({
      ...view.source,
      device: DEV2,
      sampleRate: Number(RATE_48K.value),
    });
    await waitFor(() => expect(rateSelect().value).toBe(RATE_48K.value));
  });

  it('keeps the current rate when the new device supports it', async () => {
    // 192 kHz is on dev2's list, so the pick keeps it.
    const view = renderCard(Number(RATE_192K.value));
    await openCard(view);
    await waitFor(() => expect(hasRate(RATE_96K.value)).toBe(true));

    await pickDevice(DEV2);

    await waitFor(() => expect(view.onChange).toHaveBeenCalledOnce());
    expect(view.onChange).toHaveBeenCalledWith({
      ...view.source,
      device: DEV2,
      sampleRate: Number(RATE_192K.value),
    });
  });

  it('writes the picked rate as a change of the rate alone', async () => {
    const view = renderCard();
    await openCard(view);
    await waitFor(() => expect(hasRate(RATE_96K.value)).toBe(true));

    await fireEvent.change(rateSelect(), { target: { value: RATE_96K.value } });

    expect(view.onChange).toHaveBeenCalledExactlyOnceWith({
      ...view.source,
      sampleRate: Number(RATE_96K.value),
    });
  });

  it('disables the rate select with a visible reason while a probe runs', async () => {
    const probe = holdNextProbe([RATE_48K, RATE_96K]);
    await openCard(renderCard());

    await waitFor(() => expect(rateSelect()).toBeDisabled());
    expect(screen.getByText('settings.audio.soundCards.sampleRateProbing')).toBeInTheDocument();

    probe.release();
    await waitFor(() => expect(rateSelect()).not.toBeDisabled());
  });

  it('drops a pending pick when the form device is picked again before the probe returns', async () => {
    const view = renderCard();
    await openCard(view);
    await waitFor(() => expect(hasRate(RATE_96K.value)).toBe(true));
    const probe = holdNextProbe([RATE_48K, RATE_192K]);

    await pickDevice(DEV2);
    await pickDevice(DEV1);
    probe.release();
    await settled();

    expect(view.onChange).not.toHaveBeenCalled();
    expect(screen.getByTestId<HTMLSelectElement>(DEVICE_SELECT_TESTID).value).toBe(DEV1);
  });

  it('writes nothing, now or on reopening, when the card collapses before the probe returns', async () => {
    const view = renderCard();
    await openCard(view);
    await waitFor(() => expect(hasRate(RATE_96K.value)).toBe(true));
    const probe = holdNextProbe([RATE_48K, RATE_192K]);

    await pickDevice(DEV2);
    view.state.expanded = false;
    await view.rerender({ expanded: false });
    probe.release();
    await settled();

    // Opening the card again shows the form's device and still writes nothing.
    await openCard(view);
    await waitFor(() => expect(hasRate(RATE_96K.value)).toBe(true));
    await settled();
    expect(view.onChange).not.toHaveBeenCalled();
    expect(screen.getByTestId<HTMLSelectElement>(DEVICE_SELECT_TESTID).value).toBe(DEV1);
  });

  it('writes nothing when the card unmounts before the probe returns', async () => {
    const view = renderCard();
    await openCard(view);
    await waitFor(() => expect(hasRate(RATE_96K.value)).toBe(true));
    const probe = holdNextProbe([RATE_48K, RATE_192K]);

    await pickDevice(DEV2);
    view.unmount();
    probe.release();
    await settled();

    expect(view.onChange).not.toHaveBeenCalled();
  });

  it('writes nothing when the form device changes from outside while a pick is pending', async () => {
    const view = renderCard();
    await openCard(view);
    await waitFor(() => expect(hasRate(RATE_96K.value)).toBe(true));
    const probe = holdNextProbe([RATE_48K, RATE_192K]);

    await pickDevice(DEV2);
    // Reset or a reload moves the form's device to a third value.
    const outside = { ...view.source, device: 'dev3' };
    await view.rerender({ source: outside, sources: [outside], expanded: true });
    probe.release();
    await settled();

    expect(view.onChange).not.toHaveBeenCalled();
  });
});
