import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { screen, cleanup, fireEvent, waitFor } from '@testing-library/svelte';
import { renderTyped } from '../../../../test/render-helpers';
import SoundCardManager from './SoundCardManager.svelte';
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
      [{ name: 'mic-a', device: ':1,0', gain: 1, models: [] } as AudioSourceConfig]
    );
    await openAddForm();

    const deviceSelect = await screen.findByTestId(DEVICE_SELECT_TESTID);
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
