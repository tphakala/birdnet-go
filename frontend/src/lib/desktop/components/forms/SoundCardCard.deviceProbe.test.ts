import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { screen, cleanup, fireEvent, waitFor } from '@testing-library/svelte';
import { renderTyped } from '../../../../test/render-helpers';
import SoundCardCard from './SoundCardCard.svelte';
import type { AudioSourceConfig } from '$lib/stores/settings';
import {
  DEFAULT_MODEL,
  DEVICE_SELECT_TESTID,
  EDIT_BUTTON_NAME,
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
vi.mock('./ModelCheckboxList.svelte', async () => ({
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
}));

const DEV1 = 'dev1';
const DEV2 = 'dev2';

// dev1 supports 96 kHz, dev2 does not (but supports 192/384 kHz), so we can tell
// which device's capabilities landed and exercise the unsupported-rate reset.
function ratesFor(deviceId: string): RateOption[] {
  return deviceId === DEV2 ? [RATE_48K, RATE_192K, RATE_384K] : [RATE_48K, RATE_96K];
}

vi.mock('$lib/utils/audio/sampleRate', () => ({
  fetchDeviceCapabilities: vi.fn((deviceId: string, signal?: AbortSignal) =>
    makeAbortableProbe(signal, ratesFor(deviceId))
  ),
}));

describe('SoundCardCard device capability probe (issue #3593, edit form)', () => {
  const audioDevices = [
    { index: 0, name: 'Mic One', id: DEV1 },
    { index: 1, name: 'Mic Two', id: DEV2 },
  ];
  const modelOptions = [{ value: DEFAULT_MODEL, label: 'BirdNET' }];
  const availableModels = [{ id: DEFAULT_MODEL, name: 'BirdNET', category: 'bird' }];

  function renderCard(sampleRate = Number(RATE_48K.value)) {
    const source: AudioSourceConfig = {
      name: 'Living room',
      device: DEV1,
      sampleRate,
      gain: 0,
      models: [DEFAULT_MODEL],
    };
    return renderTyped(SoundCardCard, {
      props: {
        source,
        index: 0,
        sources: [source],
        audioDevices,
        modelOptions,
        availableModels,
        disabled: false,
        onUpdate: vi.fn(() => true),
        onDelete: vi.fn(),
      },
    });
  }

  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
  });

  function rateSelect() {
    return screen.getByTestId(SAMPLE_RATE_SELECT_TESTID) as HTMLSelectElement;
  }

  function hasRate(value: string) {
    return rateSelect().querySelector(`option[value="${value}"]`) !== null;
  }

  async function openEdit() {
    await fireEvent.click(screen.getByRole('button', { name: EDIT_BUTTON_NAME }));
  }

  it('probes the current device when the edit form opens', async () => {
    renderCard();
    await openEdit();

    // dev1 supports 96 kHz; opening edit must surface it (the documented workaround).
    await waitFor(() => expect(hasRate(RATE_96K.value)).toBe(true));
  });

  it('replaces the sample-rate options when changing device while editing', async () => {
    renderCard();
    await openEdit();
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
    renderCard(Number(RATE_96K.value));
    await openEdit();
    await waitFor(() => expect(rateSelect().value).toBe(RATE_96K.value));

    const deviceSelect = screen.getByTestId(DEVICE_SELECT_TESTID);
    await fireEvent.change(deviceSelect, { target: { value: DEV2 } });

    // dev2 lacks 96 kHz, so the selection must fall back to 48 kHz rather than
    // persist an unsupported rate.
    await waitFor(() => expect(rateSelect().value).toBe(RATE_48K.value));
  });
});
