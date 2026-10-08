import { describe, it, expect } from 'vitest';
import type { AudioSettings, RTSPSettings, StreamConfig } from '$lib/stores/settings';
import type { AudioDevice } from '$lib/utils/audioDevices';
import {
  firstFreeStreamName,
  findDevice,
  initialAudioChoice,
  isRtspUrl,
  soundCardPayloads,
  streamPayloads,
  WIZARD_SOUND_CARD_NAME,
} from './audioSourceChoice';

type DeepPartial<T> = { [K in keyof T]?: DeepPartial<T[K]> };

function audioOf(partial: DeepPartial<AudioSettings>): AudioSettings {
  // Only the fields the module reads are seeded
  return partial as AudioSettings;
}

function rtspOf(streams: Array<Partial<StreamConfig>>): RTSPSettings {
  return { streams: streams as StreamConfig[] };
}

const usb: AudioDevice = { index: 1, name: 'USB Mic', id: 'hw:1,0', stableId: 'usb-path:bus-1' };
const plain: AudioDevice = { index: 2, name: 'Plain', id: 'hw:2,0' };

const U = 'rtsp://cam.example/u';
const V = 'rtsp://cam.example/v';

const TEMPLATE_SOURCE = {
  name: 'Sound Card 1',
  device: 'sysdefault',
  gain: 0,
  sampleRate: 48000,
  models: [],
};

describe('isRtspUrl', () => {
  it.each([
    ['rtsp://host/stream', true],
    ['  RTSPS://host/stream  ', true],
    ['rtsp://', false],
    ['rtsp://ho st', false],
    ['rtsp:///stream', false],
    ['rtsp://user:pass@host:554/stream', true],
    ['rtsp://[::1]:554/stream', true],
    ['http://host/stream', false],
    ['', false],
  ])('%j gives %s', (url, expected) => {
    expect(isRtspUrl(url)).toBe(expected);
  });
});

describe('initialAudioChoice', () => {
  it.each([
    {
      name: 'sources present: sound card with the first device, stream URL still prefilled',
      audio: audioOf({ sources: [{ device: 'usb-path:x' }, { device: 'hw:2,0' }] }),
      rtsp: rtspOf([{ type: 'rtsp', url: U }]),
      expected: {
        sourceType: 'soundcard',
        savedDevice: 'usb-path:x',
        primaryStreamUrl: U,
      },
    },
    {
      name: 'no sources, rtsp stream: stream, first rtsp stream skipping a non-rtsp one',
      audio: audioOf({ sources: [], source: 'hw:1,0' }),
      rtsp: rtspOf([
        { type: 'hls', url: 'http://h/x.m3u8' },
        { type: 'rtsp', url: V },
      ]),
      expected: { sourceType: 'rtsp', savedDevice: '', primaryStreamUrl: V },
    },
    {
      name: 'nothing configured: sound card, nothing prefilled',
      audio: audioOf({ sources: [] }),
      rtsp: rtspOf([]),
      expected: { sourceType: 'soundcard', savedDevice: '', primaryStreamUrl: null },
    },
    {
      name: 'only a non-rtsp stream: sound card',
      audio: undefined,
      rtsp: rtspOf([{ type: 'hls', url: 'http://h/x.m3u8' }]),
      expected: { sourceType: 'soundcard', savedDevice: '', primaryStreamUrl: null },
    },
  ])('$name', ({ audio, rtsp, expected }) => {
    expect(initialAudioChoice(audio, rtsp)).toEqual(expected);
  });
});

describe('findDevice', () => {
  it('matches by stable id and by legacy id', () => {
    expect(findDevice([usb, plain], 'usb-path:bus-1')).toBe(usb);
    expect(findDevice([usb, plain], 'hw:1,0')).toBe(usb);
    expect(findDevice([usb, plain], 'nope')).toBeUndefined();
  });
});

describe('firstFreeStreamName', () => {
  it('skips names that are taken, ignoring case', () => {
    const streams = rtspOf([{ name: 'stream 1' }, { name: 'Front' }]).streams;
    expect(firstFreeStreamName(streams)).toBe('Stream 2');
    expect(firstFreeStreamName([])).toBe('Stream 1');
  });
});

describe('soundCardPayloads', () => {
  it('replaces the template device with the stable id and drops sampleRate', () => {
    const audio = audioOf({ sources: [{ ...TEMPLATE_SOURCE, gain: 3 }], source: '' });
    const result = soundCardPayloads(audio, rtspOf([]), usb, false, null);
    expect(result.audio).toEqual({
      sources: [{ name: 'Sound Card 1', device: 'usb-path:bus-1', gain: 3, models: [] }],
      source: '',
    });
    expect(result.rtsp).toBeNull();
    expect(audio.sources[0].device).toBe('sysdefault');
  });

  it('sends nothing for a device that is already a later source', () => {
    const audio = audioOf({
      sources: [{ ...TEMPLATE_SOURCE }, { name: 'B', device: 'usb-path:bus-1', gain: 0 }],
    });
    expect(soundCardPayloads(audio, rtspOf([]), usb, false, null).audio).toBeNull();
  });

  it('clears a stale legacy source when the device is already configured', () => {
    const audio = audioOf({
      sources: [{ name: 'B', device: 'usb-path:bus-1', gain: 0 }],
      source: 'hw:9,0',
    });
    expect(soundCardPayloads(audio, rtspOf([]), usb, false, null).audio).toEqual({ source: '' });
  });

  it('creates the named default entry when no source exists', () => {
    const result = soundCardPayloads(audioOf({ sources: [] }), rtspOf([]), plain, false, null);
    expect(result.audio).toEqual({
      sources: [
        {
          name: WIZARD_SOUND_CARD_NAME,
          device: 'hw:2,0',
          gain: 0,
          models: [],
          quietHours: expect.objectContaining({ enabled: false, mode: 'fixed' }),
        },
      ],
      source: '',
    });
  });

  it('rewrites a legacy id to the stable id in place without a second entry', () => {
    const audio = audioOf({ sources: [{ name: 'Mine', device: 'hw:1,0', gain: 2 }] });
    const result = soundCardPayloads(audio, rtspOf([]), usb, false, null);
    expect(result.audio).toEqual({
      sources: [{ name: 'Mine', device: 'usb-path:bus-1', gain: 2 }],
      source: '',
    });
  });

  it('turns off only the prefilled stream when the step opened in stream mode', () => {
    const rtsp = rtspOf([
      { name: 'Stream 1', url: U, enabled: true, type: 'rtsp', gain: 4 },
      { name: 'Cam', url: V, enabled: true, type: 'rtsp' },
    ]);
    const result = soundCardPayloads(audioOf({ sources: [] }), rtsp, usb, true, U);
    expect(result.rtsp).toEqual({
      streams: [
        { name: 'Stream 1', url: U, enabled: false, type: 'rtsp', gain: 4 },
        { name: 'Cam', url: V, enabled: true, type: 'rtsp' },
      ],
    });
  });

  it('never touches streams when the step opened in sound card mode', () => {
    const rtsp = rtspOf([{ name: 'Cam', url: U, enabled: true, type: 'rtsp' }]);
    const audio = audioOf({ sources: [{ ...TEMPLATE_SOURCE }] });
    expect(soundCardPayloads(audio, rtsp, usb, false, U).rtsp).toBeNull();
  });
});

describe('streamPayloads', () => {
  it('creates one enabled tcp stream and clears the template sound card on a fresh install', () => {
    const audio = audioOf({ sources: [{ ...TEMPLATE_SOURCE }], source: '' });
    const result = streamPayloads(audio, rtspOf([]), U, null);
    expect(result.rtsp).toEqual({
      streams: [{ name: 'Stream 1', url: U, enabled: true, type: 'rtsp', transport: 'tcp' }],
    });
    expect(result.audio).toEqual({ sources: [], source: '' });
  });

  it('updates the prefilled stream in place and keeps its other fields', () => {
    const rtsp = rtspOf([
      { name: 'Yard', url: U, enabled: true, type: 'rtsp', gain: 5, models: ['perch_v2'] },
      { name: 'Other', url: V, enabled: true, type: 'rtsp' },
    ]);
    const result = streamPayloads(audioOf({ sources: [] }), rtsp, 'rtsp://new/x', U);
    expect(result.rtsp).toEqual({
      streams: [
        {
          name: 'Yard',
          url: 'rtsp://new/x',
          enabled: true,
          type: 'rtsp',
          gain: 5,
          models: ['perch_v2'],
        },
        { name: 'Other', url: V, enabled: true, type: 'rtsp' },
      ],
    });
  });

  it('only flips enabled when the URL matches a disabled stream', () => {
    const rtsp = rtspOf([{ name: 'A', url: V, enabled: false, type: 'rtsp' }]);
    expect(streamPayloads(audioOf({ sources: [] }), rtsp, V, null).rtsp).toEqual({
      streams: [{ name: 'A', url: V, enabled: true, type: 'rtsp' }],
    });
  });

  it('sends no rtsp payload when the URL matches an enabled stream', () => {
    const rtsp = rtspOf([{ name: 'A', url: V, enabled: true, type: 'rtsp' }]);
    expect(streamPayloads(audioOf({ sources: [] }), rtsp, V, V).rtsp).toBeNull();
  });

  it('names an appended stream around a taken name', () => {
    const rtsp = rtspOf([{ name: 'Stream 1', url: 'http://h/x', enabled: true, type: 'hls' }]);
    const result = streamPayloads(audioOf({ sources: [] }), rtsp, U, null);
    expect(result.rtsp?.streams?.map(s => s.name)).toEqual(['Stream 1', 'Stream 2']);
  });

  it('sends no audio payload without sources or legacy source', () => {
    expect(
      streamPayloads(audioOf({ sources: [], source: '' }), rtspOf([]), U, null).audio
    ).toBeNull();
  });

  it('clears a stale legacy source even with no sources', () => {
    const result = streamPayloads(audioOf({ sources: [], source: 'hw:1,0' }), rtspOf([]), U, null);
    expect(result.audio).toEqual({ sources: [], source: '' });
  });
});

// Transition table: each row is a sequence of wizard visits applied to a store
// that mirrors the server (objects merge, arrays replace).
interface SimStore {
  audio: { sources: AudioSettings['sources']; source: string };
  rtsp: { streams: StreamConfig[] };
}

function freshStore(): SimStore {
  return {
    audio: audioOf({ sources: [{ ...TEMPLATE_SOURCE }], source: '' }),
    rtsp: { streams: [] },
  };
}

function view(store: SimStore) {
  return { audio: audioOf(store.audio), rtsp: store.rtsp as RTSPSettings };
}

function apply(store: SimStore, payloads: { audio: unknown; rtsp: unknown }): boolean {
  let changed = false;
  if (payloads.audio !== null) {
    Object.assign(store.audio, payloads.audio);
    changed = true;
  }
  if (payloads.rtsp !== null) {
    Object.assign(store.rtsp, payloads.rtsp);
    changed = true;
  }
  return changed;
}

function chooseStream(store: SimStore, url: string, primary: string | null): boolean {
  const v = view(store);
  return apply(store, streamPayloads(v.audio, v.rtsp, url, primary));
}

function chooseCard(
  store: SimStore,
  device: AudioDevice,
  streamOwned: boolean,
  primary: string | null
) {
  const v = view(store);
  return apply(store, soundCardPayloads(v.audio, v.rtsp, device, streamOwned, primary));
}

function mount(store: SimStore) {
  const v = view(store);
  const initial = initialAudioChoice(v.audio, v.rtsp);
  return { streamOwned: initial.sourceType === 'rtsp', primary: initial.primaryStreamUrl };
}

function assertInvariants(store: SimStore) {
  const names = store.rtsp.streams.map(s => s.name.toLowerCase());
  expect(new Set(names).size).toBe(names.length);
  const urls = store.rtsp.streams.map(s => s.url);
  expect(new Set(urls).size).toBe(urls.length);
  for (const s of store.rtsp.streams) {
    if (s.type === 'rtsp') expect(isRtspUrl(s.url)).toBe(true);
  }
  const devices = store.audio.sources.map(s => s.device);
  expect(new Set(devices).size).toBe(devices.length);
}

describe('wizard audio transitions', () => {
  it('(d) a reorder between mount and commit does not redirect the edit', () => {
    const store = freshStore();
    store.audio.sources = [];
    const A = { name: 'A', url: 'http://h/a', enabled: true, type: 'hls' } as const;
    const B = { name: 'B', url: 'rtsp://b/x', enabled: true, type: 'rtsp' } as const;
    const C = { name: 'C', url: 'rtsp://c/x', enabled: true, type: 'rtsp' } as const;
    store.rtsp.streams = [A, B, C];
    const m = mount(store);
    expect(m.primary).toBe(B.url);
    store.rtsp.streams = [C, A, B];
    chooseStream(store, 'rtsp://new/x', m.primary);
    expect(store.rtsp.streams).toEqual([C, A, { ...B, url: 'rtsp://new/x' }]);
    assertInvariants(store);
  });

  it('(e) choosing the same sound card on a rerun sends nothing', () => {
    const store = freshStore();
    chooseCard(store, usb, false, null);
    const m = mount(store);
    expect(chooseCard(store, usb, m.streamOwned, m.primary)).toBe(false);
    assertInvariants(store);
  });

  it('(f) stream, Back, sound card, Back, stream round trip', () => {
    const store = freshStore();
    chooseStream(store, U, null);
    let m = mount(store);
    expect(m.streamOwned).toBe(true);
    chooseCard(store, usb, m.streamOwned, m.primary);
    expect(store.audio.sources).toEqual([
      expect.objectContaining({ name: 'Sound Card 1', device: 'usb-path:bus-1' }),
    ]);
    expect(store.rtsp.streams).toEqual([
      { name: 'Stream 1', url: U, enabled: false, type: 'rtsp', transport: 'tcp' },
    ]);
    m = mount(store);
    expect(m.streamOwned).toBe(false);
    chooseStream(store, U, m.primary);
    expect(store.audio.sources).toEqual([]);
    expect(store.rtsp.streams[0].enabled).toBe(true);
    assertInvariants(store);
  });

  it('(g) a corrected URL after a partial failure edits the same stream', () => {
    const store = freshStore();
    // First attempt: the rtsp save succeeded, the audio save failed
    const v = view(store);
    const first = streamPayloads(v.audio, v.rtsp, U, null);
    apply(store, { audio: null, rtsp: first.rtsp });
    // The step remembers the URL it saved as the primary stream
    chooseStream(store, V, U);
    expect(store.rtsp.streams).toHaveLength(1);
    expect(store.rtsp.streams[0]).toMatchObject({ name: 'Stream 1', url: V });
    assertInvariants(store);
  });
});
