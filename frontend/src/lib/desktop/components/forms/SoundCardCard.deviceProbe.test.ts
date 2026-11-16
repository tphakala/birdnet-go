import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { screen, cleanup, fireEvent, waitFor } from '@testing-library/svelte';
import { createComponentTestFactory } from '../../../../test/render-helpers';
import {
  restoreElementAnimate,
  stubElementAnimate,
} from '../../../../test/fixtures/elementAnimate';
import SoundCardCard from './SoundCardCard.svelte';
import type { AudioSourceConfig } from '$lib/stores/settings';
import {
  DEFAULT_MODEL,
  DEVICE_SELECT_TESTID,
  SAMPLE_RATE_SELECT_TESTID,
  RATE_48K,
  RATE_96K,
  RATE_192K,
  RATE_384K,
  makeAbortableProbe,
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

  function renderCard(sampleRate = Number(RATE_48K.value), device = DEV1) {
    const source: AudioSourceConfig = {
      name: 'Living room',
      device,
      sampleRate,
      gain: 0,
      models: [DEFAULT_MODEL],
    };
    return createComponentTestFactory(SoundCardCard, {
      source,
      index: 0,
      sources: [source],
      audioDevices,
      modelOptions,
      availableModels,
      disabled: false,
      rowId: 'row-1',
      expanded: false,
      onToggleExpanded: vi.fn(),
      onCommit: vi.fn(),
      onDelete: vi.fn(),
    }).render();
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

  // The card is controlled: the manager opens it by passing expanded.
  async function openCard(view: ReturnType<typeof renderCard>) {
    await view.rerender({ expanded: true });
  }

  it('probes the current device when the card opens', async () => {
    await openCard(renderCard());

    // dev1 supports 96 kHz; opening the card must surface it (the documented workaround).
    await waitFor(() => expect(hasRate(RATE_96K.value)).toBe(true));
  });

  it('replaces the sample-rate options when changing device while the card is open', async () => {
    await openCard(renderCard());
    // Wait for dev1's probe to land first so the switch is a genuine replacement.
    await waitFor(() => expect(hasRate(RATE_96K.value)).toBe(true));

    const deviceSelect = screen.getByTestId(DEVICE_SELECT_TESTID);
    await fireEvent.change(deviceSelect, { target: { value: DEV2 } });

    // dev2's rates must replace dev1's: the new rates appear and dev1's exclusive
    // 96 kHz is gone (wholesale replacement, not a merge).
    await waitFor(() => expect(hasRate(RATE_192K.value)).toBe(true));
    expect(hasRate(RATE_384K.value)).toBe(true);
    expect(hasRate(RATE_96K.value)).toBe(false);
  });

  it('resets a selected rate the new device cannot honor back to 48 kHz', async () => {
    // Source saved at 96 kHz, which dev1 supports but dev2 does not.
    await openCard(renderCard(Number(RATE_96K.value)));
    await waitFor(() => expect(rateSelect().value).toBe(RATE_96K.value));

    const deviceSelect = screen.getByTestId(DEVICE_SELECT_TESTID);
    await fireEvent.change(deviceSelect, { target: { value: DEV2 } });

    // dev2 lacks 96 kHz, so the selection must fall back to 48 kHz rather than
    // persist an unsupported rate.
    await waitFor(() => expect(rateSelect().value).toBe(RATE_48K.value));
  });

  it('coerces a rate the saved device cannot honor when the user returns to it after another pick', async () => {
    // Saved on dev2 at 48 kHz; dev2 supports 48, 192 and 384 kHz but not 96 kHz.
    await openCard(renderCard(Number(RATE_48K.value), DEV2));
    await waitFor(() => expect(hasRate(RATE_192K.value)).toBe(true));

    const deviceSelect = screen.getByTestId(DEVICE_SELECT_TESTID);
    await fireEvent.change(deviceSelect, { target: { value: DEV1 } });
    await waitFor(() => expect(hasRate(RATE_96K.value)).toBe(true));
    await fireEvent.change(screen.getByTestId(SAMPLE_RATE_SELECT_TESTID), {
      target: { value: RATE_96K.value },
    });
    expect(rateSelect().value).toBe(RATE_96K.value);

    // Back on the saved device: the draft differs from the saved values, so the
    // 96 kHz pick must not survive on a device that lacks it.
    await fireEvent.change(deviceSelect, { target: { value: DEV2 } });
    await waitFor(() => expect(hasRate(RATE_192K.value)).toBe(true));
    await waitFor(() => expect(rateSelect().value).toBe(RATE_48K.value));
  });

  it('restores the saved rate when the draft returns to the saved device', async () => {
    // Saved on dev1 at 96 kHz, which dev2 lacks, so picking dev2 coerces the draft to 48 kHz.
    await openCard(renderCard(Number(RATE_96K.value)));
    await waitFor(() => expect(rateSelect().value).toBe(RATE_96K.value));

    const deviceSelect = screen.getByTestId(DEVICE_SELECT_TESTID);
    await fireEvent.change(deviceSelect, { target: { value: DEV2 } });
    await waitFor(() => expect(rateSelect().value).toBe(RATE_48K.value));

    // Back on dev1: the draft equals the saved values again, so nothing is left to apply.
    await fireEvent.change(deviceSelect, { target: { value: DEV1 } });
    await waitFor(() => expect(hasRate(RATE_96K.value)).toBe(true));
    expect(rateSelect().value).toBe(RATE_96K.value);
  });
});
