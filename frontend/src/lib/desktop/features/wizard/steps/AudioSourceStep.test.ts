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

const urlInput = () => screen.getByPlaceholderText(`${KEY}.rtspUrlPlaceholder`);
const urlAlert = () => screen.getByRole('alert');
// The device state region is the focusable one; the set up later region is sr-only
const deviceStatus = () => {
  const region = screen.getAllByRole('status').find(el => el.getAttribute('tabindex') === '-1');
  if (!region) throw new Error('device status region not rendered');
  return region;
};
const setUpLaterStatus = () => {
  const region = screen.getAllByRole('status').find(el => el.classList.contains('sr-only'));
  if (!region) throw new Error('set up later status region not rendered');
  return region;
};

async function typeUrl(value: string) {
  await fireEvent.input(urlInput(), { target: { value } });
}
async function leaveUrl() {
  await fireEvent.blur(urlInput());
}

async function chooseStream(url = RTSP_URL) {
  await fireEvent.click(radio(/wizard\.steps\.audioSource\.rtspStream/));
  await screen.findByPlaceholderText(`${KEY}.rtspUrlPlaceholder`);
  await typeUrl(url);
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
    seed({ sources: [TEMPLATE_SOURCE], source: 'hw:1,0' });
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

  it('sound card chosen after picking a stream that was already enabled turns that stream off', async () => {
    saveLikeStore();
    seed({ sources: [TEMPLATE_SOURCE], source: '' }, [
      { name: 'Cam', url: RTSP_URL, enabled: true, type: 'rtsp' },
    ]);
    vi.mocked(api.get).mockResolvedValue([USB]);
    const { leave } = renderStep(AudioSourceStep);
    await flushAsync();
    await chooseStream();
    await leave();

    await fireEvent.click(radio(/wizard\.steps\.audioSource\.soundcard/));
    await chooseUsbDevice();
    await leave();

    const calls = vi.mocked(settingsActions.saveSection).mock.calls;
    expect(calls[calls.length - 1]).toEqual([
      'rtsp',
      { streams: [{ name: 'Cam', url: RTSP_URL, enabled: false, type: 'rtsp' }] },
    ]);
  });

  it('set up later is a visible button and sends nothing', async () => {
    const { leave } = renderStep(AudioSourceStep);
    await flushAsync();
    const button = screen.getByRole('button', { name: `${KEY}.setUpLater` });
    expect(button).toHaveAttribute('aria-pressed', 'false');
    await fireEvent.click(button);
    expect(button).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getAllByText(`${KEY}.setUpLaterChosen`)).toHaveLength(2);

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
    await typeUrl(OTHER_URL);
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

    await typeUrl(OTHER_URL);
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

  it('finishes the stream change when the step unmounts during the first save', async () => {
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

    const calls = vi.mocked(settingsActions.saveSection).mock.calls;
    expect(calls.map(c => c[0])).toEqual(['rtsp', 'audio']);
    expect(calls[1][1]).toEqual({ sources: [], source: '' });
  });

  it('finishes the sound card change when the step unmounts during the first save', async () => {
    seed({ sources: [], source: '' }, [
      { name: 'Yard', url: RTSP_URL, enabled: true, type: 'rtsp' },
    ]);
    vi.mocked(api.get).mockResolvedValue([USB]);
    const firstSave = deferred();
    vi.mocked(settingsActions.saveSection).mockImplementation(section =>
      section === 'audio' ? firstSave.promise : Promise.resolve()
    );
    const { leave, unmount } = renderStep(AudioSourceStep);
    await flushAsync();
    await fireEvent.click(radio(/wizard\.steps\.audioSource\.soundcard/));
    await chooseUsbDevice();

    const leaving = leave();
    await flushAsync();
    unmount();
    firstSave.resolve();
    await leaving;

    const calls = vi.mocked(settingsActions.saveSection).mock.calls;
    expect(calls.map(c => c[0])).toEqual(['audio', 'rtsp']);
    expect(calls[1][1]).toEqual({
      streams: [{ name: 'Yard', url: RTSP_URL, enabled: false, type: 'rtsp' }],
    });
  });

  it('sends no second write when the first fails after unmount', async () => {
    const firstSave = deferred();
    vi.mocked(settingsActions.saveSection).mockImplementation(section =>
      section === 'rtsp' ? firstSave.promise : Promise.resolve()
    );
    const { leave, unmount } = renderStep(AudioSourceStep);
    await flushAsync();
    await chooseStream();

    const leaving = leave();
    // Attach the handler before the rejection so it is never reported as unhandled
    const failure = leaving.then(
      () => undefined,
      (error: unknown) => error
    );
    await flushAsync();
    unmount();
    firstSave.reject(new Error('save rtsp failed'));
    expect(await failure).toEqual(new Error('save rtsp failed'));

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

  it('leaving with a malformed stream URL sends nothing', async () => {
    const { leave } = renderStep(AudioSourceStep);
    await flushAsync();
    await chooseStream('camera.example/stream');

    await leave();

    expect(settingsActions.saveSection).not.toHaveBeenCalled();
  });

  it('leaving while the device list reloads sends nothing', async () => {
    vi.mocked(api.get).mockRejectedValueOnce(new Error('boom'));
    const { leave } = renderStep(AudioSourceStep);
    await flushAsync();
    const reload = deferred<unknown[]>();
    vi.mocked(api.get).mockReturnValueOnce(reload.promise);
    await clickRetry();
    await fireEvent.click(radio(/wizard\.steps\.audioSource\.soundcard/));

    await leave();

    expect(settingsActions.saveSection).not.toHaveBeenCalled();
    reload.resolve([USB]);
    await flushAsync();
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

async function clickRetry() {
  const retry = await screen.findByRole('button', { name: 'common.retry' });
  retry.focus();
  await fireEvent.click(retry);
}

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

    await clickRetry();

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
    retry.focus();
    retry.click();
    retry.click();
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(3));

    newer.resolve([USB]);
    await waitFor(() =>
      expect(document.activeElement).toBe(document.getElementById('wizard-audio-device'))
    );
    late(older);
    await flushAsync();

    expect(document.getElementById('wizard-audio-device')).not.toBeNull();
    expect(screen.queryAllByText(`${KEY}.${stale}`)).toHaveLength(0);
    // The stale result neither applies nor moves focus
    expect(document.activeElement).toBe(document.getElementById('wizard-audio-device'));
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

// What the URL field exposes: the alert text (the reason named, or empty), aria-invalid and what aria-describedby points at (the help text, plus the alert while it shows)
function expectUrlError(reason: 'urlScheme' | 'enterUrl' | null) {
  const alert = urlAlert();
  const input = urlInput();
  const help = screen.getByText(`${KEY}.rtspUrlHelp`);
  expect(alert.textContent.trim()).toBe(reason === null ? '' : `${KEY}.reasons.${reason}`);
  // The help text is always linked; the error is added while it shows, then the note
  // that sound cards stop (present when the seed has a saved sound card)
  const note = screen.queryByText(`${KEY}.streamReplacesSoundCards`);
  const expected = [help.id, reason === null ? '' : alert.id, note?.id ?? '']
    .filter(Boolean)
    .join(' ');
  expect(input).toHaveAttribute('aria-describedby', expected);
  if (reason === null) {
    expect(input).not.toHaveAttribute('aria-invalid');
  } else {
    expect(input).toHaveAttribute('aria-invalid', 'true');
  }
}

describe('AudioSourceStep - URL error timing', () => {
  beforeEach(() => {
    vi.mocked(api.get).mockReset().mockResolvedValue([USB]);
    resetStore();
  });

  it('keeps the URL error hidden while typing, then shows it once the field is left, while Next still names the scheme', async () => {
    const onValidChange = vi.fn();
    renderTyped(AudioSourceStep, { props: { onValidChange } });
    await flushAsync();
    await chooseStream('http://x');

    await waitFor(() =>
      expect(onValidChange).toHaveBeenLastCalledWith(false, `${KEY}.reasons.urlScheme`)
    );
    expectUrlError(null);

    await leaveUrl();
    expectUrlError('urlScheme');
    expect(onValidChange).toHaveBeenLastCalledWith(false, `${KEY}.reasons.urlScheme`);
  });

  it('hides the error while the field is corrected and shows it again on a new mistake', async () => {
    renderStep(AudioSourceStep);
    await flushAsync();
    await chooseStream('http://x');
    await leaveUrl();
    expectUrlError('urlScheme');

    await typeUrl(RTSP_URL);
    expectUrlError(null);
    await leaveUrl();
    expectUrlError(null);

    await typeUrl('rtsp://');
    expectUrlError(null);
    await leaveUrl();
    expectUrlError('urlScheme');

    await typeUrl('');
    expectUrlError(null);
    await leaveUrl();
    expectUrlError('enterUrl');
    await typeUrl('h');
    expectUrlError(null);
    await leaveUrl();
    expectUrlError('urlScheme');
    expect(urlInput()).toHaveValue('h');
  });

  it('keeps the error when switching source type away and back', async () => {
    renderStep(AudioSourceStep);
    await flushAsync();
    await chooseStream('http://x');
    await leaveUrl();
    expectUrlError('urlScheme');

    await fireEvent.click(radio(/wizard\.steps\.audioSource\.soundcard/));
    await deviceTrigger();
    await fireEvent.click(radio(/wizard\.steps\.audioSource\.rtspStream/));

    expectUrlError('urlScheme');
    expect(urlInput()).toHaveValue('http://x');
  });

  it('shows the URL error on open for a saved malformed stream URL', async () => {
    seed({ sources: [] }, [{ name: 'Old', url: 'rtsp://', enabled: true, type: 'rtsp' }]);
    renderStep(AudioSourceStep);
    await flushAsync();

    expect(await screen.findByPlaceholderText(`${KEY}.rtspUrlPlaceholder`)).toHaveValue('rtsp://');
    expectUrlError('urlScheme');
  });

  it('shows the enter URL error once an empty field is left, while Next names the same reason', async () => {
    const onValidChange = vi.fn();
    renderTyped(AudioSourceStep, { props: { onValidChange } });
    await flushAsync();
    await fireEvent.click(radio(/wizard\.steps\.audioSource\.rtspStream/));
    await screen.findByPlaceholderText(`${KEY}.rtspUrlPlaceholder`);
    expect(urlInput()).toHaveValue('');

    await leaveUrl();

    expectUrlError('enterUrl');
    expect(onValidChange).toHaveBeenLastCalledWith(false, `${KEY}.reasons.enterUrl`);
  });

  it('keeps the empty URL error hidden until the field is left', async () => {
    renderStep(AudioSourceStep);
    await flushAsync();
    await fireEvent.click(radio(/wizard\.steps\.audioSource\.rtspStream/));
    await screen.findByPlaceholderText(`${KEY}.rtspUrlPlaceholder`);
    expect(urlInput()).toHaveValue('');
    expectUrlError(null);

    await typeUrl('h');
    await typeUrl('');

    expectUrlError(null);
  });

  it('clears the empty URL error when a valid URL is typed', async () => {
    renderStep(AudioSourceStep);
    await flushAsync();
    await chooseStream('');
    await leaveUrl();
    expectUrlError('enterUrl');

    await typeUrl(RTSP_URL);
    expectUrlError(null);
    await leaveUrl();
    expectUrlError(null);
    expect(urlInput()).toHaveValue(RTSP_URL);
  });

  it('switches the message from enter URL to the scheme reason as the field is filled', async () => {
    renderStep(AudioSourceStep);
    await flushAsync();
    await chooseStream('');
    await leaveUrl();
    expectUrlError('enterUrl');

    await typeUrl('http://x');
    expectUrlError(null);
    await leaveUrl();
    expectUrlError('urlScheme');
    expect(urlInput()).toHaveValue('http://x');
  });

  it('shows no scheme error while set up later is chosen', async () => {
    renderStep(AudioSourceStep);
    await flushAsync();
    await chooseStream('http://x');
    await leaveUrl();
    expectUrlError('urlScheme');

    const setUpLater = screen.getByRole('button', { name: `${KEY}.setUpLater` });
    await fireEvent.click(setUpLater);

    expectUrlError(null);
    expect(setUpLater).toHaveAttribute('aria-pressed', 'true');
  });

  it('shows no empty URL error while set up later is chosen', async () => {
    renderStep(AudioSourceStep);
    await flushAsync();
    await chooseStream('');
    await leaveUrl();
    expectUrlError('enterUrl');

    await fireEvent.click(screen.getByRole('button', { name: `${KEY}.setUpLater` }));
    expectUrlError(null);

    await typeUrl('h');
    expectUrlError(null);
    await leaveUrl();
    expectUrlError('urlScheme');
    expect(screen.getByRole('button', { name: `${KEY}.setUpLater` })).toHaveAttribute(
      'aria-pressed',
      'false'
    );
  });

  it('shows no URL error on open for a saved stream with an empty URL', async () => {
    seed({ sources: [] }, [{ name: 'Old', url: '', enabled: true, type: 'rtsp' }]);
    renderStep(AudioSourceStep);
    await flushAsync();

    expect(await screen.findByPlaceholderText(`${KEY}.rtspUrlPlaceholder`)).toHaveValue('');
    expectUrlError(null);
  });

  it('reserves the URL error line so showing it moves nothing', async () => {
    renderStep(AudioSourceStep);
    await flushAsync();
    await chooseStream('http://x');
    const before = urlAlert().className;
    await leaveUrl();

    expect(urlAlert().className).toBe(before);
    expect(before).toContain('min-h-10');
  });
});

describe('AudioSourceStep - focus', () => {
  beforeEach(() => {
    vi.mocked(api.get).mockReset().mockResolvedValue([USB]);
    resetStore();
  });

  it.each([
    {
      name: 'the device dropdown after a successful reload',
      next: () => vi.mocked(api.get).mockResolvedValueOnce([USB]),
      target: () => document.getElementById('wizard-audio-device'),
    },
    {
      name: 'Retry when the reloaded list is empty',
      next: () => vi.mocked(api.get).mockResolvedValueOnce([]),
      target: () => screen.getByRole('button', { name: 'common.retry' }),
    },
    {
      name: 'Retry when the reload failed',
      next: () => vi.mocked(api.get).mockRejectedValueOnce(new Error('again')),
      target: () => screen.getByRole('button', { name: 'common.retry' }),
    },
  ])('Retry moves focus to $name', async ({ next, target }) => {
    vi.mocked(api.get).mockRejectedValueOnce(new Error('boom'));
    next();
    renderStep(AudioSourceStep);
    await flushAsync();
    await clickRetry();

    await waitFor(() => expect(document.activeElement).toBe(target()));
  });

  it('keeps focus inside the step while Retry reloads the list', async () => {
    const pending = deferred<unknown[]>();
    vi.mocked(api.get)
      .mockRejectedValueOnce(new Error('boom'))
      .mockReturnValueOnce(pending.promise);
    renderStep(AudioSourceStep);
    await flushAsync();
    await clickRetry();

    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(2));
    expect(document.activeElement).toBe(deviceStatus());
    expect(document.activeElement).not.toBe(document.body);
  });

  it('leaves focus where the user moved it during a Retry load', async () => {
    const pending = deferred<unknown[]>();
    vi.mocked(api.get)
      .mockRejectedValueOnce(new Error('boom'))
      .mockReturnValueOnce(pending.promise);
    renderStep(AudioSourceStep);
    await flushAsync();
    await clickRetry();
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(2));
    const streamRadio = radio(/wizard\.steps\.audioSource\.rtspStream/);
    streamRadio.focus();

    pending.resolve([USB]);
    await flushAsync();

    expect(document.activeElement).toBe(streamRadio);
  });

  it('moves no focus on the initial device load', async () => {
    renderStep(AudioSourceStep);
    await flushAsync();
    await deviceTrigger();

    expect(document.activeElement).toBe(document.body);
  });

  it('moves focus to the URL input after Use an RTSP stream instead', async () => {
    vi.mocked(api.get).mockResolvedValue([]);
    renderStep(AudioSourceStep);
    await flushAsync();
    const useStream = await screen.findByRole('button', { name: `${KEY}.useStreamInstead` });
    useStream.focus();
    await fireEvent.click(useStream);

    await waitFor(() => expect(document.activeElement).toBe(urlInput()));
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
      name: 'while devices load',
      prepare: () => {
        vi.mocked(api.get).mockReturnValue(new Promise(() => {}));
      },
      ready: async () => {
        await screen.findByText(`${KEY}.deviceLoading`);
      },
    },
    {
      name: 'with no devices',
      prepare: () => {
        vi.mocked(api.get).mockResolvedValue([]);
      },
      ready: async () => {
        await screen.findByText(`${KEY}.noDevicesFound`);
      },
    },
    {
      name: 'in the stream state',
      prepare: () => {},
      ready: async () => {
        await chooseStream('http://x');
      },
    },
    {
      name: 'with the empty URL error shown',
      prepare: () => {},
      ready: async () => {
        await chooseStream('');
        await leaveUrl();
        expect(urlAlert()).toHaveTextContent(`${KEY}.reasons.enterUrl`);
      },
    },
    {
      name: 'with the URL error shown',
      prepare: () => {},
      ready: async () => {
        await chooseStream('http://x');
        await leaveUrl();
        expect(urlAlert()).toHaveTextContent(`${KEY}.reasons.urlScheme`);
      },
    },
  ])('has no violations $name', async ({ prepare, ready }) => {
    prepare();
    const { container } = renderStep(AudioSourceStep);
    await flushAsync();
    await ready();
    await expect(expectNoA11yViolations(container)).resolves.toBeUndefined();
  });

  it.each([
    {
      name: 'while devices load',
      prepare: () => {
        vi.mocked(api.get).mockReturnValue(new Promise(() => {}));
      },
      labelled: false,
    },
    {
      name: 'when listing devices failed',
      prepare: () => {
        vi.mocked(api.get).mockRejectedValue(new Error('boom'));
      },
      labelled: false,
    },
    {
      name: 'with no devices',
      prepare: () => {
        vi.mocked(api.get).mockResolvedValue([]);
      },
      labelled: false,
    },
    { name: 'with devices listed', prepare: () => {}, labelled: true },
  ])('every label points at a rendered control $name', async ({ prepare, labelled }) => {
    prepare();
    const { container } = renderStep(AudioSourceStep);
    await flushAsync();
    await flushAsync();

    const labels = container.querySelectorAll('label[for]');
    expect(labels.length).toBe(labelled ? 1 : 0);
    for (const label of labels) {
      const target = label.getAttribute('for') ?? '';
      expect(document.getElementById(target), `label for "${target}"`).not.toBeNull();
    }
  });

  it('announces set up later in a status region that stays rendered', async () => {
    renderStep(AudioSourceStep);
    await flushAsync();
    const region = setUpLaterStatus();
    expect(region).toHaveTextContent('');

    await fireEvent.click(screen.getByRole('button', { name: `${KEY}.setUpLater` }));
    expect(setUpLaterStatus()).toBe(region);
    expect(region).toHaveTextContent(`${KEY}.setUpLaterChosen`);

    await fireEvent.click(radio(/wizard\.steps\.audioSource\.rtspStream/));
    await typeUrl(RTSP_URL);
    expect(setUpLaterStatus()).toBe(region);
    expect(region).toHaveTextContent('');
  });

  it('describes the device dropdown with the note that the stream is turned off', async () => {
    seed({ sources: [], source: '' }, [
      { name: 'Yard', url: RTSP_URL, enabled: true, type: 'rtsp' },
    ]);
    renderStep(AudioSourceStep);
    await flushAsync();
    await fireEvent.click(radio(/wizard\.steps\.audioSource\.soundcard/));
    await chooseUsbDevice();

    expect(await deviceTrigger()).toHaveAccessibleDescription(`${KEY}.soundCardReplacesStream`);
  });

  it('describes the URL input with the note that sound cards stop', async () => {
    seed({ sources: [{ ...TEMPLATE_SOURCE, device: 'usb-path:bus-1' }], source: '' });
    renderStep(AudioSourceStep);
    await flushAsync();
    await chooseStream();

    expect(urlInput()).toHaveAccessibleDescription(
      `${KEY}.rtspUrlHelp ${KEY}.streamReplacesSoundCards`
    );

    await typeUrl('http://x');
    await leaveUrl();

    expect(urlInput()).toHaveAccessibleDescription(
      `${KEY}.rtspUrlHelp ${KEY}.reasons.urlScheme ${KEY}.streamReplacesSoundCards`
    );
  });

  it('marks the URL input with the error border only while the error shows', async () => {
    renderStep(AudioSourceStep);
    await flushAsync();
    await chooseStream('http://x');
    expect(urlInput()).not.toHaveClass('input-error');

    await leaveUrl();
    expect(urlInput()).toHaveClass('input-error');

    await typeUrl(RTSP_URL);
    expect(urlInput()).not.toHaveClass('input-error');

    await typeUrl('');
    expect(urlInput()).not.toHaveClass('input-error');
    await leaveUrl();
    expect(urlInput()).toHaveClass('input-error');
  });

  it('names the open device listbox with the field label', async () => {
    renderStep(AudioSourceStep);
    await flushAsync();
    await fireEvent.click(await deviceTrigger());

    expect(await screen.findByRole('listbox', { name: `${KEY}.deviceLabel` })).toBeInTheDocument();
  });

  it('colours the device failure and URL error texts with the error text token', async () => {
    vi.mocked(api.get).mockRejectedValue(new Error('boom'));
    renderStep(AudioSourceStep);
    await flushAsync();

    const failure = await screen.findByText(`${KEY}.devicesLoadFailed`);
    expect(failure).toHaveClass('text-[var(--text-error)]');

    await chooseStream('http://x');
    await leaveUrl();
    expect(urlAlert()).toHaveClass('text-[var(--text-error)]');
  });
});
