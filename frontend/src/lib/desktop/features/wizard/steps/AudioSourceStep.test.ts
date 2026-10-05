import { describe, it, expect, beforeEach, vi } from 'vitest';
import { fireEvent, screen, waitFor } from '@testing-library/svelte';
import { expectNoA11yViolations } from '$lib/utils/axe-utils';
import { deferred } from '../../../../../test/async-helpers';

vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string) => key),
  getLocale: vi.fn(() => 'en'),
}));

vi.mock('$lib/utils/api', async importOriginal => ({
  ...(await importOriginal<typeof import('$lib/utils/api')>()),
  api: {
    get: vi.fn().mockResolvedValue([]),
    post: vi.fn(),
  },
}));

vi.mock('$lib/stores/settings', async () => {
  const { createSettingsMock } = await import('./stepTestUtils');
  return createSettingsMock({ realtime: { audio: { source: '' }, rtsp: { streams: [] } } });
});

import AudioSourceStep from './AudioSourceStep.svelte';
import { settingsActions, settingsStore } from '$lib/stores/settings';
import type { SettingsFormData } from '$lib/stores/settings';
import { api, ApiError } from '$lib/utils/api';
import { flushAsync, renderStep } from './stepTestUtils';
import { renderTyped } from '../../../../../test/render-helpers';

const RTSP_URL = 'rtsp://camera.example/stream';
const OTHER_URL = 'rtsp://camera.example/other';
const USB = { name: 'USB Mic', index: 1, id: 'hw:1,0', stableId: 'usb-path:bus-1' };
const KEY = 'wizard.steps.audioSource';

const TEMPLATE_SOURCE = { name: 'Sound Card 1', device: 'sysdefault', gain: 0, models: [] };

// The step reads what the server holds, so seed both copies
function seed(audio: Record<string, unknown>, streams: unknown[] = []) {
  const realtime = { audio, rtsp: { streams } };
  settingsStore.update(state => ({
    ...state,
    originalData: { realtime } as unknown as SettingsFormData,
    formData: { realtime } as unknown as SettingsFormData,
  }));
}

const radio = (name: RegExp) => screen.getByRole('radio', { name });

async function chooseStream(url = RTSP_URL) {
  await fireEvent.click(radio(/wizard\.steps\.audioSource\.rtspStream/));
  const input = await screen.findByPlaceholderText(`${KEY}.rtspUrlPlaceholder`);
  await fireEvent.input(input, { target: { value: url } });
}

// The dropdown trigger has no accessible name of its own in these tests
async function deviceTrigger(): Promise<HTMLElement> {
  return waitFor(() => {
    const el = document.getElementById('wizard-audio-device');
    if (!el) throw new Error('device dropdown not rendered');
    return el;
  });
}

async function chooseUsbDevice() {
  await fireEvent.click(await deviceTrigger());
  await fireEvent.click(await screen.findByRole('option', { name: /USB Mic/ }));
}

// Applies a saved section to the stored settings the way the real store does
// (objects merge, arrays replace), and rejects the sections named in failOn once each.
function saveLikeStore(failOn: Array<'audio' | 'rtsp'> = []) {
  const pending = [...failOn];
  vi.mocked(settingsActions.saveSection).mockImplementation(async (section, partial) => {
    const failIndex = pending.indexOf(section as 'audio' | 'rtsp');
    if (failIndex >= 0) {
      pending.splice(failIndex, 1);
      throw new Error(`save ${section} failed`);
    }
    settingsStore.update(state => {
      const realtime = state.originalData.realtime as unknown as Record<string, object>;
      // eslint-disable-next-line security/detect-object-injection -- section is 'audio' or 'rtsp' in these tests
      realtime[section] = { ...realtime[section], ...partial };
      return state;
    });
  });
}

function resetStore() {
  seed({ sources: [TEMPLATE_SOURCE], source: '' });
}

// The leave handler contract shared by every step is in stepContract.test.ts
describe('AudioSourceStep - leave handler', () => {
  beforeEach(() => {
    vi.mocked(settingsActions.saveSection).mockClear().mockResolvedValue(undefined);
    vi.mocked(api.get).mockReset().mockResolvedValue([]);
    resetStore();
  });

  it('sound card choice patches audio sources with the stable device id and clears the legacy source', async () => {
    vi.mocked(api.get).mockResolvedValue([USB]);
    const { leave } = renderStep(AudioSourceStep);
    await flushAsync();
    await chooseUsbDevice();

    await leave();
    await leave();

    expect(settingsActions.saveSection).toHaveBeenCalledTimes(1);
    expect(settingsActions.saveSection).toHaveBeenCalledWith('audio', {
      sources: [{ ...TEMPLATE_SOURCE, device: 'usb-path:bus-1' }],
      source: '',
    });
  });

  it('stream choice keeps other streams and clears sound cards', async () => {
    seed({ sources: [TEMPLATE_SOURCE], source: '' }, [
      { name: 'Hls', url: 'http://h/x.m3u8', enabled: true, type: 'hls' },
    ]);
    const { leave } = renderStep(AudioSourceStep);
    await flushAsync();
    await chooseStream();

    await leave();
    await leave();

    expect(vi.mocked(settingsActions.saveSection).mock.calls).toEqual([
      [
        'rtsp',
        {
          streams: [
            { name: 'Hls', url: 'http://h/x.m3u8', enabled: true, type: 'hls' },
            { name: 'Stream 1', url: RTSP_URL, enabled: true, type: 'rtsp', transport: 'tcp' },
          ],
        },
      ],
      ['audio', { sources: [], source: '' }],
    ]);
  });

  it('sound card chosen after the step opened on a stream turns only that stream off', async () => {
    seed({ sources: [], source: '' }, [
      { name: 'Yard', url: RTSP_URL, enabled: true, type: 'rtsp' },
      { name: 'Cam', url: OTHER_URL, enabled: true, type: 'rtsp' },
    ]);
    vi.mocked(api.get).mockResolvedValue([USB]);
    const { leave } = renderStep(AudioSourceStep);
    await flushAsync();
    await fireEvent.click(radio(/wizard\.steps\.audioSource\.soundcard/));
    await chooseUsbDevice();
    expect(screen.getByText(`${KEY}.soundCardReplacesStream`)).toBeInTheDocument();

    await leave();

    expect(vi.mocked(settingsActions.saveSection).mock.calls).toEqual([
      [
        'audio',
        {
          sources: [
            expect.objectContaining({ name: 'Sound Card 1', device: 'usb-path:bus-1', gain: 0 }),
          ],
          source: '',
        },
      ],
      [
        'rtsp',
        {
          streams: [
            { name: 'Yard', url: RTSP_URL, enabled: false, type: 'rtsp' },
            { name: 'Cam', url: OTHER_URL, enabled: true, type: 'rtsp' },
          ],
        },
      ],
    ]);
  });

  it('sound card chosen when sources exist leaves streams alone', async () => {
    seed({ sources: [TEMPLATE_SOURCE], source: '' }, [
      { name: 'Cam', url: RTSP_URL, enabled: true, type: 'rtsp' },
    ]);
    vi.mocked(api.get).mockResolvedValue([USB]);
    const { leave } = renderStep(AudioSourceStep);
    await flushAsync();
    await chooseUsbDevice();

    await leave();

    expect(vi.mocked(settingsActions.saveSection).mock.calls.map(c => c[0])).toEqual(['audio']);
  });

  it('sound card chosen after a stream save whose audio write failed turns that stream off', async () => {
    saveLikeStore(['audio']);
    vi.mocked(api.get).mockResolvedValue([USB]);
    const { leave } = renderStep(AudioSourceStep);
    await flushAsync();
    await chooseStream();
    await expect(leave()).rejects.toThrow('save audio failed');

    await fireEvent.click(radio(/wizard\.steps\.audioSource\.soundcard/));
    await chooseUsbDevice();
    await leave();

    const calls = vi.mocked(settingsActions.saveSection).mock.calls;
    expect(calls.map(c => c[0])).toEqual(['rtsp', 'audio', 'audio', 'rtsp']);
    expect(calls[3][1]).toEqual({
      streams: [
        { name: 'Stream 1', url: RTSP_URL, enabled: false, type: 'rtsp', transport: 'tcp' },
      ],
    });
  });

  it('set up later is a visible button and sends nothing', async () => {
    const { leave } = renderStep(AudioSourceStep);
    await flushAsync();
    const button = screen.getByRole('button', { name: `${KEY}.setUpLater` });
    expect(button).toHaveAttribute('aria-pressed', 'false');
    await fireEvent.click(button);
    expect(button).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByText(`${KEY}.setUpLaterChosen`)).toBeInTheDocument();

    await leave();

    expect(settingsActions.saveSection).not.toHaveBeenCalled();
  });

  it('set up later after an edit discards the edit and sends nothing', async () => {
    const { leave } = renderStep(AudioSourceStep);
    await flushAsync();
    await chooseStream();
    await fireEvent.click(screen.getByRole('button', { name: `${KEY}.setUpLater` }));

    await leave();

    expect(settingsActions.saveSection).not.toHaveBeenCalled();
  });

  it('an edit after set up later clears it and saves the edit', async () => {
    const { leave } = renderStep(AudioSourceStep);
    await flushAsync();
    await chooseStream();
    const button = screen.getByRole('button', { name: `${KEY}.setUpLater` });
    await fireEvent.click(button);
    await fireEvent.input(screen.getByPlaceholderText(`${KEY}.rtspUrlPlaceholder`), {
      target: { value: OTHER_URL },
    });
    expect(button).toHaveAttribute('aria-pressed', 'false');

    await leave();

    expect(vi.mocked(settingsActions.saveSection).mock.calls.map(c => c[0])).toEqual([
      'rtsp',
      'audio',
    ]);
  });

  it('retry after a failed audio write edits the saved stream with the corrected URL', async () => {
    saveLikeStore(['audio']);
    seed({ sources: [TEMPLATE_SOURCE], source: '' });
    const { leave } = renderStep(AudioSourceStep);
    await flushAsync();
    await chooseStream();
    await expect(leave()).rejects.toThrow('save audio failed');

    await fireEvent.input(screen.getByPlaceholderText(`${KEY}.rtspUrlPlaceholder`), {
      target: { value: OTHER_URL },
    });
    await leave();

    const calls = vi.mocked(settingsActions.saveSection).mock.calls;
    expect(calls.map(c => c[0])).toEqual(['rtsp', 'audio', 'rtsp', 'audio']);
    expect(calls[2][1]).toEqual({
      streams: [
        { name: 'Stream 1', url: OTHER_URL, enabled: true, type: 'rtsp', transport: 'tcp' },
      ],
    });
    expect(calls[3][1]).toEqual({ sources: [], source: '' });
  });

  it('sends no second section once the step is unmounted during the first save', async () => {
    const firstSave = deferred();
    vi.mocked(settingsActions.saveSection).mockImplementation(section =>
      section === 'rtsp' ? firstSave.promise : Promise.resolve()
    );
    const { leave, unmount } = renderStep(AudioSourceStep);
    await flushAsync();
    await chooseStream();

    const leaving = leave();
    await flushAsync();
    unmount();
    firstSave.resolve();
    await leaving;

    expect(vi.mocked(settingsActions.saveSection).mock.calls.map(c => c[0])).toEqual(['rtsp']);
  });

  it('retry after a failed second section re-sends only that section when nothing was edited', async () => {
    // The store keeps nothing, so the first section is rebuilt identically on the retry
    vi.mocked(settingsActions.saveSection).mockImplementation(async section => {
      if (vi.mocked(settingsActions.saveSection).mock.calls.length === 2 && section === 'audio') {
        throw new Error('save audio failed');
      }
    });
    const { leave } = renderStep(AudioSourceStep);
    await flushAsync();
    await chooseStream();
    await expect(leave()).rejects.toThrow('save audio failed');

    await leave();

    expect(vi.mocked(settingsActions.saveSection).mock.calls.map(c => c[0])).toEqual([
      'rtsp',
      'audio',
      'audio',
    ]);
  });

  it('preselects the saved device by its stable id and writes nothing', async () => {
    seed({ sources: [{ ...TEMPLATE_SOURCE, device: 'usb-path:bus-1' }], source: '' });
    vi.mocked(api.get).mockResolvedValue([USB]);
    const { leave } = renderStep(AudioSourceStep);
    await flushAsync();

    expect(await deviceTrigger()).toHaveTextContent('USB Mic');
    await leave();
    expect(settingsActions.saveSection).not.toHaveBeenCalled();
  });
});

describe('AudioSourceStep - device loading', () => {
  beforeEach(() => {
    vi.mocked(settingsActions.saveSection).mockClear().mockResolvedValue(undefined);
    vi.mocked(api.get).mockReset().mockResolvedValue([]);
    resetStore();
  });

  it('device listing failure shows an error with Retry and keeps the sound card option', async () => {
    const response = new Response(null, { status: 500 });
    vi.mocked(api.get).mockRejectedValueOnce(new ApiError('Server error', 500, response));
    renderStep(AudioSourceStep);
    await flushAsync();

    expect(await screen.findAllByText(`${KEY}.devicesLoadFailed`)).not.toHaveLength(0);
    expect(screen.getByText('Server error')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'common.retry' })).toBeInTheDocument();
    expect(radio(/wizard\.steps\.audioSource\.soundcard/)).toHaveAttribute('aria-checked', 'true');
  });

  it('Retry reloads the device list', async () => {
    vi.mocked(api.get).mockRejectedValueOnce(new Error('boom')).mockResolvedValueOnce([USB]);
    renderStep(AudioSourceStep);
    await flushAsync();

    await fireEvent.click(await screen.findByRole('button', { name: 'common.retry' }));

    expect(await deviceTrigger()).toBeVisible();
    expect(api.get).toHaveBeenCalledTimes(2);
  });

  it('no devices shows Retry and the stream option without switching to the stream', async () => {
    renderStep(AudioSourceStep);
    await flushAsync();

    expect(await screen.findAllByText(`${KEY}.noDevicesFound`)).not.toHaveLength(0);
    expect(radio(/wizard\.steps\.audioSource\.soundcard/)).toHaveAttribute('aria-checked', 'true');
    await fireEvent.click(screen.getByRole('button', { name: `${KEY}.useStreamInstead` }));
    expect(radio(/wizard\.steps\.audioSource\.rtspStream/)).toHaveAttribute('aria-checked', 'true');
  });

  it.each([
    {
      name: 'a stale device load failure is ignored',
      late: (older: { resolve: (v: unknown[]) => void; reject: (e: Error) => void }) =>
        older.reject(new Error('late failure')),
      stale: 'devicesLoadFailed',
    },
    {
      name: 'a stale device list that arrives late does not replace the newer one',
      late: (older: { resolve: (v: unknown[]) => void; reject: (e: Error) => void }) =>
        older.resolve([]),
      stale: 'noDevicesFound',
    },
  ])('$name', async ({ late, stale }) => {
    const older = deferred<unknown[]>();
    const newer = deferred<unknown[]>();
    vi.mocked(api.get)
      .mockRejectedValueOnce(new Error('first load failed'))
      .mockReturnValueOnce(older.promise)
      .mockReturnValueOnce(newer.promise);
    renderStep(AudioSourceStep);
    await flushAsync();
    // Two clicks before Svelte re-renders start two overlapping loads
    const retry = await screen.findByRole('button', { name: 'common.retry' });
    retry.click();
    retry.click();
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(3));

    newer.resolve([USB]);
    await flushAsync();
    late(older);
    await flushAsync();

    expect(document.getElementById('wizard-audio-device')).not.toBeNull();
    expect(screen.queryAllByText(`${KEY}.${stale}`)).toHaveLength(0);
  });
});

describe('AudioSourceStep - Next reason', () => {
  beforeEach(() => {
    vi.mocked(api.get).mockReset().mockResolvedValue([USB]);
    resetStore();
  });

  it('names the missing choice', async () => {
    const onValidChange = vi.fn();
    renderTyped(AudioSourceStep, { props: { onValidChange } });
    await flushAsync();
    await waitFor(() =>
      expect(onValidChange).toHaveBeenLastCalledWith(false, `${KEY}.reasons.chooseDevice`)
    );

    await chooseStream('');
    await waitFor(() =>
      expect(onValidChange).toHaveBeenLastCalledWith(false, `${KEY}.reasons.enterUrl`)
    );

    await chooseStream('http://x');
    await waitFor(() =>
      expect(onValidChange).toHaveBeenLastCalledWith(false, `${KEY}.reasons.urlScheme`)
    );
    expect(screen.getByRole('alert')).toHaveTextContent(`${KEY}.reasons.urlScheme`);

    await chooseStream(RTSP_URL);
    await waitFor(() => expect(onValidChange).toHaveBeenLastCalledWith(true, undefined));
  });
});

describe('AudioSourceStep - device state reasons', () => {
  beforeEach(() => {
    vi.mocked(api.get).mockReset().mockResolvedValue([]);
    resetStore();
  });

  it.each([
    {
      name: 'loading while devices load',
      setup: () => {
        vi.mocked(api.get).mockReturnValueOnce(deferred<unknown[]>().promise);
      },
      reason: `${KEY}.deviceLoading`,
    },
    {
      name: 'failed when listing devices failed',
      setup: () => {
        vi.mocked(api.get).mockRejectedValueOnce(new Error('boom'));
      },
      reason: `${KEY}.reasons.devicesFailed`,
    },
    { name: 'no devices for an empty list', setup: () => {}, reason: `${KEY}.reasons.noDevices` },
  ])('reports $name', async ({ setup, reason }) => {
    setup();
    const onValidChange = vi.fn();
    renderTyped(AudioSourceStep, { props: { onValidChange } });

    await waitFor(() => expect(onValidChange).toHaveBeenLastCalledWith(false, reason));
  });
});

describe('AudioSourceStep Accessibility', () => {
  beforeEach(() => {
    vi.mocked(api.get).mockReset().mockResolvedValue([USB]);
    resetStore();
  });

  it.each([
    {
      name: 'with devices listed',
      prepare: () => {},
      ready: async () => {
        await deviceTrigger();
      },
    },
    {
      name: 'when listing devices failed',
      prepare: () => {
        vi.mocked(api.get).mockRejectedValue(new Error('boom'));
      },
      ready: async () => {
        await screen.findByRole('button', { name: 'common.retry' });
      },
    },
    {
      name: 'in the stream state',
      prepare: () => {},
      ready: async () => {
        await chooseStream('http://x');
      },
    },
  ])('has no violations $name', async ({ prepare, ready }) => {
    prepare();
    const { container } = renderStep(AudioSourceStep);
    await flushAsync();
    await ready();
    await expect(expectNoA11yViolations(container)).resolves.toBeUndefined();
  });
});
