import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { screen, cleanup, fireEvent, waitFor } from '@testing-library/svelte';
import { renderTyped } from '../../../../test/render-helpers';
import SoundCardCard from './SoundCardCard.svelte';
import type { AudioSourceConfig } from '$lib/stores/settings';

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

// Per-device probe results, so we can tell which device's capabilities landed in
// the dropdown. Mirrors the real utility's contract: it honours the AbortSignal
// and only surfaces AbortError to the caller.
function ratesFor(deviceId: string) {
  if (deviceId === 'dev2') {
    return [
      { value: '48000', label: '48 kHz' },
      { value: '192000', label: '192 kHz' },
      { value: '384000', label: '384 kHz' },
    ];
  }
  // dev1
  return [
    { value: '48000', label: '48 kHz' },
    { value: '96000', label: '96 kHz' },
  ];
}

vi.mock('$lib/utils/audio/sampleRate', () => ({
  fetchDeviceCapabilities: vi.fn(
    (deviceId: string, signal?: AbortSignal) =>
      new Promise((resolve, reject) => {
        if (signal?.aborted) {
          reject(new DOMException('Aborted', 'AbortError'));
          return;
        }
        const timer = setTimeout(() => {
          signal?.removeEventListener('abort', onAbort);
          resolve({ options: ratesFor(deviceId), verified: true });
        }, 0);
        function onAbort() {
          clearTimeout(timer);
          signal?.removeEventListener('abort', onAbort);
          reject(new DOMException('Aborted', 'AbortError'));
        }
        signal?.addEventListener('abort', onAbort);
      })
  ),
}));

describe('SoundCardCard device capability probe (issue #3593, edit form)', () => {
  const audioDevices = [
    { index: 0, name: 'Mic One', id: 'dev1' },
    { index: 1, name: 'Mic Two', id: 'dev2' },
  ];
  const modelOptions = [{ value: 'birdnet', label: 'BirdNET' }];
  const availableModels = [{ id: 'birdnet', name: 'BirdNET', category: 'bird' }];

  function renderCard() {
    const source: AudioSourceConfig = {
      name: 'Living room',
      device: 'dev1',
      sampleRate: 48000,
      gain: 0,
      models: ['birdnet'],
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
    return screen.getByTestId('select-settings.audio.soundCards.sampleRateLabel');
  }

  async function openEdit() {
    await fireEvent.click(screen.getByRole('button', { name: 'common.edit' }));
  }

  it('probes the current device when the edit form opens', async () => {
    renderCard();
    await openEdit();

    // dev1 supports 96 kHz; opening edit must surface it (the documented workaround).
    await waitFor(() => {
      expect(rateSelect().querySelector('option[value="96000"]')).not.toBeNull();
    });
  });

  it('replaces the sample-rate options when changing device while editing', async () => {
    renderCard();
    await openEdit();
    // Wait for dev1's probe to land first so the switch is a genuine replacement.
    await waitFor(() => {
      expect(rateSelect().querySelector('option[value="96000"]')).not.toBeNull();
    });

    const deviceSelect = screen.getByTestId('select-settings.audio.soundCards.deviceLabel');
    await fireEvent.change(deviceSelect, { target: { value: 'dev2' } });

    // dev2's rates must replace dev1's: the new rates appear and dev1's exclusive
    // 96 kHz is gone (wholesale replacement, not a merge).
    await waitFor(() => {
      expect(rateSelect().querySelector('option[value="192000"]')).not.toBeNull();
    });
    expect(rateSelect().querySelector('option[value="384000"]')).not.toBeNull();
    expect(rateSelect().querySelector('option[value="96000"]')).toBeNull();
  });
});
