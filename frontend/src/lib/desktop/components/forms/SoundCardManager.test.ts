import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { screen, cleanup, fireEvent, waitFor } from '@testing-library/svelte';
import { renderTyped } from '../../../../test/render-helpers';
import SoundCardManager from './SoundCardManager.svelte';
import type { AudioSourceConfig } from '$lib/stores/settings';

// Functional stand-in for SelectDropdown: renders a native <select> wired to the
// same `onChange` prop and exposes `options` as real <option> elements. This lets
// us drive device selection and assert on the probed sample-rate options without
// driving the real portal-based dropdown UI.
vi.mock('./SelectDropdown.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockSelectDropdown.svelte')).default,
}));

// Heavy children irrelevant to this flow are replaced with inert components.
vi.mock('./TextInput.svelte', async () => ({
  default: (await import('../../../../test/fixtures/MockEmpty.svelte')).default,
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

// Avoid the models store touching the network during mount.
vi.mock('$lib/stores/models.svelte', () => ({
  getAvailableModels: vi.fn(() => []),
  DEFAULT_MODEL_ID: 'birdnet',
  fetchModels: vi.fn(() => () => {}),
}));

const ABORT = () => new DOMException('Aborted', 'AbortError');

// Per-device probe behaviour, mirroring the real utility's contract (only
// AbortError is surfaced to the caller; everything else returns options).
//  - 'fast-dev' resolves immediately and honours abort.
//  - 'slow-dev' resolves late and IGNORES abort, simulating a response that was
//    already in flight when its controller was aborted (the stale-result race).
//  - default device resolves immediately with the full rate set and honours abort.
function probeFor(deviceId: string, signal: AbortSignal | undefined) {
  if (deviceId === 'slow-dev') {
    return new Promise((resolve, reject) => {
      if (signal?.aborted) {
        reject(ABORT());
        return;
      }
      // Deliberately ignores the abort signal and resolves later than fast-dev.
      setTimeout(
        () => resolve({ options: [{ value: '48000', label: '48 kHz' }], verified: true }),
        40
      );
    });
  }
  if (deviceId === 'fast-dev') {
    return makeAbortableProbe(signal, [
      { value: '48000', label: '48 kHz' },
      { value: '96000', label: '96 kHz' },
    ]);
  }
  return makeAbortableProbe(signal, [
    { value: '48000', label: '48 kHz' },
    { value: '96000', label: '96 kHz' },
    { value: '192000', label: '192 kHz' },
  ]);
}

function makeAbortableProbe(
  signal: AbortSignal | undefined,
  options: { value: string; label: string }[]
) {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(ABORT());
      return;
    }
    const timer = setTimeout(() => {
      signal?.removeEventListener('abort', onAbort);
      resolve({ options, verified: true });
    }, 0);
    function onAbort() {
      clearTimeout(timer);
      signal?.removeEventListener('abort', onAbort);
      reject(ABORT());
    }
    signal?.addEventListener('abort', onAbort);
  });
}

vi.mock('$lib/utils/audio/sampleRate', () => ({
  fetchDeviceCapabilities: vi.fn((deviceId: string, signal?: AbortSignal) =>
    probeFor(deviceId, signal)
  ),
}));

const settle = (ms: number) => new Promise(resolve => setTimeout(resolve, ms));

describe('SoundCardManager sample rate probe (issue #3593)', () => {
  function renderManager(audioDevices: Array<{ index: number; name: string; id: string }>) {
    return renderTyped(SoundCardManager, {
      props: {
        sources: [] as AudioSourceConfig[],
        audioDevices,
        audioDevicesLoading: false,
        disabled: false,
        onUpdateSources: vi.fn(),
        onRefreshDevices: vi.fn(),
      },
    });
  }

  async function openAddForm() {
    await fireEvent.click(screen.getByText('settings.audio.soundCards.addSource'));
  }

  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
  });

  it('populates the sample-rate dropdown after selecting a device for a new source', async () => {
    renderManager([{ index: 0, name: 'USB Mic', id: 'usb-mic' }]);
    await openAddForm();

    const deviceSelect = await screen.findByTestId('select-settings.audio.soundCards.deviceLabel');
    await fireEvent.change(deviceSelect, { target: { value: 'usb-mic' } });

    const rateSelect = screen.getByTestId('select-settings.audio.soundCards.sampleRateLabel');

    // The probed rates beyond the 48 kHz default must become selectable.
    await waitFor(() => {
      expect(rateSelect.querySelector('option[value="96000"]')).not.toBeNull();
    });
    expect(rateSelect.querySelector('option[value="192000"]')).not.toBeNull();
  });

  it('ignores a stale probe that resolves after a newer device was selected', async () => {
    renderManager([
      { index: 0, name: 'Slow Mic', id: 'slow-dev' },
      { index: 1, name: 'Fast Mic', id: 'fast-dev' },
    ]);
    await openAddForm();

    const deviceSelect = await screen.findByTestId('select-settings.audio.soundCards.deviceLabel');
    // Select the slow device, then immediately switch to the fast one. The slow
    // probe resolves later and ignores its abort, so without the supersession
    // guard it would clobber the fast device's options.
    await fireEvent.change(deviceSelect, { target: { value: 'slow-dev' } });
    await fireEvent.change(deviceSelect, { target: { value: 'fast-dev' } });

    const rateSelect = screen.getByTestId('select-settings.audio.soundCards.sampleRateLabel');
    await waitFor(() => {
      expect(rateSelect.querySelector('option[value="96000"]')).not.toBeNull();
    });

    // Let the stale slow-dev probe resolve; the fast device's options must survive.
    await settle(80);
    expect(rateSelect.querySelector('option[value="96000"]')).not.toBeNull();
  });
});
