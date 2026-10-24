import { describe, expect, it } from 'vitest';
import { coerceSettings } from './settingsCoercion';

describe('settingsCoercion realtime rtsp streams', () => {
  it('defaults missing stream enabled flag to true', () => {
    const result = coerceSettings('realtime', {
      rtsp: {
        streams: [
          {
            name: 'Legacy Stream',
            url: 'rtsp://cam1',
            type: 'rtsp',
          },
        ],
      },
    });

    expect(result).toMatchObject({
      rtsp: {
        streams: [
          {
            name: 'Legacy Stream',
            url: 'rtsp://cam1',
            enabled: true,
            type: 'rtsp',
          },
        ],
      },
    });
  });

  it('defaults null stream enabled flag to true', () => {
    const result = coerceSettings('realtime', {
      rtsp: {
        streams: [
          {
            name: 'Null Stream',
            url: 'rtsp://cam3',
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
            enabled: null as any,
            type: 'rtsp',
          },
        ],
      },
    });

    expect(result).toMatchObject({
      rtsp: {
        streams: [{ enabled: true }],
      },
    });
  });

  it('preserves explicit disabled streams', () => {
    const result = coerceSettings('realtime', {
      rtsp: {
        streams: [
          {
            name: 'Disabled Stream',
            url: 'rtsp://cam2',
            enabled: false,
            type: 'rtsp',
          },
        ],
      },
    });

    expect(result).toMatchObject({
      rtsp: {
        streams: [
          {
            name: 'Disabled Stream',
            url: 'rtsp://cam2',
            enabled: false,
            type: 'rtsp',
          },
        ],
      },
    });
  });

  it('preserves an explicit stream gain value on save round-trip', () => {
    const result = coerceSettings('realtime', {
      rtsp: {
        streams: [
          {
            name: 'Gain Stream',
            url: 'rtsp://cam4',
            type: 'rtsp',
            gain: 12,
          },
        ],
      },
    });

    expect(result).toMatchObject({
      rtsp: {
        streams: [
          {
            name: 'Gain Stream',
            url: 'rtsp://cam4',
            gain: 12,
          },
        ],
      },
    });
  });

  it('clamps an out-of-range stream gain to the -40..+40 dB bounds', () => {
    const result = coerceSettings('realtime', {
      rtsp: {
        streams: [
          {
            name: 'Loud Stream',
            url: 'rtsp://cam5',
            type: 'rtsp',
            gain: 100,
          },
        ],
      },
    });

    expect(result).toMatchObject({
      rtsp: {
        streams: [{ gain: 40 }],
      },
    });
  });

  it('leaves stream gain undefined when not provided', () => {
    const result = coerceSettings('realtime', {
      rtsp: {
        streams: [
          {
            name: 'No Gain Stream',
            url: 'rtsp://cam6',
            type: 'rtsp',
          },
        ],
      },
    }) as { rtsp: { streams: Array<Record<string, unknown>> } };

    expect(result.rtsp.streams[0]?.gain).toBeUndefined();
  });

  it('preserves valid media modes', () => {
    for (const mode of ['auto', 'audio-only', 'full-stream']) {
      const result = coerceSettings('realtime', {
        rtsp: {
          streams: [{ name: 'Cam', url: 'rtsp://cam7', type: 'rtsp', mediaMode: mode }],
        },
      }) as { rtsp: { streams: Array<Record<string, unknown>> } };

      expect(result.rtsp.streams[0]?.mediaMode).toBe(mode);
    }
  });

  it('drops an invalid media mode', () => {
    const result = coerceSettings('realtime', {
      rtsp: {
        streams: [{ name: 'Cam', url: 'rtsp://cam8', type: 'rtsp', mediaMode: 'video-only' }],
      },
    }) as { rtsp: { streams: Array<Record<string, unknown>> } };

    expect(result.rtsp.streams[0]?.mediaMode).toBeUndefined();
  });
});

describe('settingsCoercion realtime privacyFilter and VAD', () => {
  it('coerces privacyFilter with default vad settings when vad is absent', () => {
    const result = coerceSettings('realtime', {
      privacyFilter: {
        enabled: true,
        confidence: 0.05,
      },
    }) as {
      privacyFilter: {
        enabled: boolean;
        confidence: number;
        debug: boolean;
        vad: { enabled: boolean; threshold: number; modelPath: string };
      };
    };

    expect(result.privacyFilter).toEqual({
      enabled: true,
      confidence: 0.05,
      debug: false,
      vad: {
        enabled: false,
        threshold: 0.6,
        modelPath: '',
      },
    });
  });

  it('coerces string values and clamps vad threshold to valid bounds', () => {
    const result = coerceSettings('realtime', {
      privacyFilter: {
        enabled: 'true',
        confidence: '0.8',
        debug: 'false',
        vad: {
          enabled: 'true',
          threshold: '1.5', // should clamp to 1.0
          modelPath: '/path/to/model.onnx',
        },
      },
    }) as {
      privacyFilter: {
        enabled: boolean;
        confidence: number;
        debug: boolean;
        vad: { enabled: boolean; threshold: number; modelPath: string };
      };
    };

    expect(result.privacyFilter).toEqual({
      enabled: true,
      confidence: 0.8,
      debug: false,
      vad: {
        enabled: true,
        threshold: 1.0,
        modelPath: '/path/to/model.onnx',
      },
    });
  });

  it('clamps vad threshold below minimum to 0.01', () => {
    const result = coerceSettings('realtime', {
      privacyFilter: {
        vad: {
          threshold: -0.5,
        },
      },
    }) as { privacyFilter: { vad: { threshold: number } } };

    expect(result.privacyFilter.vad.threshold).toBe(0.01);
  });
});

describe('settingsCoercion realtime mqtt tls settings', () => {
  it('coerces mqtt tls with insecureSkipVerify correctly', () => {
    const result = coerceSettings('realtime', {
      mqtt: {
        enabled: true,
        tls: {
          enabled: true,
          insecureSkipVerify: true,
        },
      },
    }) as {
      mqtt: {
        enabled: boolean;
        tls: {
          enabled: boolean;
          insecureSkipVerify: boolean;
        };
      };
    };

    expect(result.mqtt.tls).toEqual({
      enabled: true,
      insecureSkipVerify: true,
    });
  });

  it('coerces legacy skipVerify to insecureSkipVerify as fallback', () => {
    const result = coerceSettings('realtime', {
      mqtt: {
        enabled: true,
        tls: {
          enabled: true,
          skipVerify: true,
        },
      },
    }) as {
      mqtt: {
        enabled: boolean;
        tls: {
          enabled: boolean;
          insecureSkipVerify: boolean;
        };
      };
    };

    expect(result.mqtt.tls).toEqual({
      enabled: true,
      insecureSkipVerify: true,
    });
  });

  it('prefers insecureSkipVerify over skipVerify when both are present', () => {
    const result = coerceSettings('realtime', {
      mqtt: {
        enabled: true,
        tls: {
          enabled: true,
          insecureSkipVerify: false,
          skipVerify: true,
        },
      },
    }) as {
      mqtt: {
        enabled: boolean;
        tls: {
          enabled: boolean;
          insecureSkipVerify: boolean;
        };
      };
    };

    expect(result.mqtt.tls).toEqual({
      enabled: true,
      insecureSkipVerify: false,
    });
  });

  it('defaults insecureSkipVerify to false when tls is missing or default', () => {
    const result = coerceSettings('realtime', {
      mqtt: {
        enabled: true,
      },
    }) as {
      mqtt: {
        enabled: boolean;
        tls: {
          enabled: boolean;
          insecureSkipVerify: boolean;
        };
      };
    };

    expect(result.mqtt.tls).toEqual({
      enabled: false,
      insecureSkipVerify: false,
    });
  });
});

describe('settingsCoercion realtime audio export ultrasonicType', () => {
  it('preserves a valid wav ultrasonic export format', () => {
    const result = coerceSettings('realtime', {
      audio: { export: { enabled: true, type: 'mp3', ultrasonicType: 'wav' } },
    });

    expect(result).toMatchObject({
      audio: { export: { ultrasonicType: 'wav' } },
    });
  });

  it('preserves a valid flac ultrasonic export format', () => {
    const result = coerceSettings('realtime', {
      audio: { export: { enabled: true, type: 'wav', ultrasonicType: 'flac' } },
    });

    expect(result).toMatchObject({
      audio: { export: { ultrasonicType: 'flac' } },
    });
  });

  it('backfills a missing ultrasonic export format to flac (legacy config)', () => {
    const result = coerceSettings('realtime', {
      audio: { export: { enabled: true, type: 'mp3' } },
    });

    expect(result).toMatchObject({
      audio: { export: { ultrasonicType: 'flac' } },
    });
  });

  it('replaces an out-of-range ultrasonic export format with flac', () => {
    const result = coerceSettings('realtime', {
      audio: { export: { enabled: true, type: 'mp3', ultrasonicType: 'mp3' } },
    });

    expect(result).toMatchObject({
      audio: { export: { ultrasonicType: 'flac' } },
    });
  });
});

describe('settingsCoercion security trustedProxies', () => {
  it('keeps a trusted proxy list in order', () => {
    const result = coerceSettings('security', {
      trustedProxies: ['172.18.0.1', 'Cloudflare', '10.0.0.0/8'],
    });

    expect(result).toMatchObject({ trustedProxies: ['172.18.0.1', 'Cloudflare', '10.0.0.0/8'] });
  });

  it('returns an already clean list unchanged', () => {
    const list = ['172.18.0.1', 'cloudflare'];
    const result = coerceSettings('security', { trustedProxies: list });

    expect(result.trustedProxies).toBe(list);
  });

  it('coerces a null list to an empty list', () => {
    const result = coerceSettings('security', { trustedProxies: null });

    expect(result).toMatchObject({ trustedProxies: [] });
  });

  it('adds an empty list when the key is absent, so saved and edited copies agree', () => {
    const result = coerceSettings('security', { host: 'example.com' });

    expect(result).toMatchObject({ host: 'example.com', trustedProxies: [] });
  });

  it('drops non-string entries', () => {
    const result = coerceSettings('security', { trustedProxies: ['10.0.0.1', 5, null, {}] });

    expect(result).toMatchObject({ trustedProxies: ['10.0.0.1'] });
  });
});

describe('settingsCoercion realtime audio equalizer passes', () => {
  it('resolves a missing passes to 0 (no filtering) for every filter type', () => {
    const result = coerceSettings('realtime', {
      audio: {
        equalizer: {
          enabled: true,
          filters: [
            { type: 'HighPass', frequency: 100 },
            { type: 'LowPass', frequency: 15000 },
            { type: 'BandReject', frequency: 1000 },
            { type: 'BandPass', frequency: 1000 },
          ],
        },
      },
    });

    const filters = (result as { audio: { equalizer: { filters: { passes: number }[] } } }).audio
      .equalizer.filters;
    expect(filters.map(f => f.passes)).toEqual([0, 0, 0, 0]);
  });

  it('keeps an explicit passes of 0 and of 2', () => {
    const result = coerceSettings('realtime', {
      audio: {
        equalizer: {
          enabled: true,
          filters: [
            { type: 'HighPass', frequency: 100, passes: 0 },
            { type: 'LowPass', frequency: 15000, passes: 2 },
          ],
        },
      },
    });

    const filters = (result as { audio: { equalizer: { filters: { passes: number }[] } } }).audio
      .equalizer.filters;
    expect(filters.map(f => f.passes)).toEqual([0, 2]);
  });
});

// The three places an equalizer is stored: the global one, one per sound card source and
// one per stream. All three go through the same coercion.
type EqPath = {
  name: string;
  wrap: (equalizer: unknown) => Record<string, unknown>;
  read: (result: unknown) => Record<string, unknown> | undefined;
  wrapWithout: (extra?: Record<string, unknown>) => Record<string, unknown>;
  readOwner: (result: unknown) => Record<string, unknown>;
};

const EQ_PATHS: EqPath[] = [
  {
    name: 'realtime.audio.equalizer',
    wrap: equalizer => ({ audio: { equalizer } }),
    read: result => (result as { audio: { equalizer?: Record<string, unknown> } }).audio.equalizer,
    wrapWithout: extra => ({ audio: { ...extra } }),
    readOwner: result => (result as { audio: Record<string, unknown> }).audio,
  },
  {
    name: 'realtime.audio.sources[0].equalizer',
    wrap: equalizer => ({ audio: { sources: [{ name: 'Card', device: 'hw:0', equalizer }] } }),
    read: result =>
      (result as { audio: { sources: Array<{ equalizer?: Record<string, unknown> }> } }).audio
        .sources[0]?.equalizer,
    wrapWithout: extra => ({ audio: { sources: [{ name: 'Card', device: 'hw:0', ...extra }] } }),
    readOwner: result =>
      (result as { audio: { sources: Array<Record<string, unknown>> } }).audio.sources[0] ?? {},
  },
  {
    name: 'realtime.rtsp.streams[0].equalizer',
    wrap: equalizer => ({
      rtsp: { streams: [{ name: 'Cam', url: 'rtsp://cam', type: 'rtsp', equalizer }] },
    }),
    read: result =>
      (result as { rtsp: { streams: Array<{ equalizer?: Record<string, unknown> }> } }).rtsp
        .streams[0]?.equalizer,
    wrapWithout: extra => ({
      rtsp: { streams: [{ name: 'Cam', url: 'rtsp://cam', type: 'rtsp', ...extra }] },
    }),
    readOwner: result =>
      (result as { rtsp: { streams: Array<Record<string, unknown>> } }).rtsp.streams[0] ?? {},
  },
];

type Filters = Array<Record<string, unknown>>;

function filtersOf(path: EqPath, input: unknown): Filters {
  const equalizer = path.read(coerceSettings('realtime', path.wrap(input)));
  const filters = equalizer?.filters;
  return Array.isArray(filters) ? filters : [];
}

describe.each(EQ_PATHS)('settingsCoercion equalizer on $name', path => {
  describe.each([true, false])('with enabled=%s', enabled => {
    const equalizerOf = (filters: unknown[]) => ({ enabled, filters });

    it('keeps every value the backend produces', () => {
      const types = ['LowPass', 'Peaking', 'AllPass', 'lowpass', 'Notch', ''];
      const frequencies = [10, 22000, 30000];
      const qs = [0.05, 50, 500];
      const widths = [0, 0.5, 15000];
      const gains = [-60, 20];
      const passes = [0, 8, 20, -1];
      const input = Array.from({ length: Math.max(types.length, frequencies.length) }, (_, i) => ({
        type: types[i % types.length],
        frequency: frequencies[i % frequencies.length],
        q: qs[i % qs.length],
        width: widths[i % widths.length],
        gain: gains[i % gains.length],
        passes: passes[i % passes.length],
      }));
      const result = filtersOf(path, equalizerOf(input));
      expect(result).toEqual(input);
    });

    it('keeps each extreme value on its own, including zero', () => {
      for (const field of ['frequency', 'q', 'width', 'gain', 'passes'] as const) {
        for (const value of [0, -1, 1e9, 0.5]) {
          const filter = { type: 'LowPass', frequency: 500, q: 1, width: 2, gain: 3, passes: 1 };
          const [out] = filtersOf(path, equalizerOf([{ ...filter, [field]: value }]));
          expect(out[field], `${field}=${value}`).toBe(
            field === 'passes' ? Math.round(value) : value
          );
        }
      }
    });

    it('replaces only non-numbers with defaults', () => {
      for (const bad of [NaN, Infinity, 'abc', null, {}, undefined]) {
        const [out] = filtersOf(
          path,
          equalizerOf([
            { type: 'HighPass', frequency: bad, q: bad, width: bad, gain: bad, passes: bad },
          ])
        );
        expect(out).toEqual({
          type: 'HighPass',
          frequency: 100,
          q: 0.707,
          width: 100,
          gain: 0,
          passes: 0,
        });
      }
      const [missing] = filtersOf(path, equalizerOf([{ type: 'LowPass' }]));
      expect(missing).toMatchObject({ frequency: 15000, passes: 0 });
      const [rounded] = filtersOf(path, equalizerOf([{ type: 'LowPass', passes: 2.6 }]));
      expect(rounded.passes).toBe(3);
      const [fromString] = filtersOf(
        path,
        equalizerOf([{ type: 'LowPass', frequency: '1000', passes: '2' }])
      );
      expect(fromString).toMatchObject({ frequency: 1000, passes: 2 });
    });

    it('defaults a non-string type to LowPass and drops non-object entries', () => {
      const result = filtersOf(path, equalizerOf([{ type: 5, frequency: 40 }, null, 'x', [1]]));
      expect(result).toHaveLength(1);
      expect(result[0]).toMatchObject({ type: 'LowPass', frequency: 40 });
    });

    it('is deterministic and idempotent, and never gives a filter an id', () => {
      const base = { type: 'LowPass', frequency: 5000, q: 1, width: 100, gain: 0, passes: 1 };
      const second = { type: 'HighPass', frequency: 200, passes: 2 };
      const steps: unknown[][] = [
        [base],
        [base, { ...second, id: 'filter-123' }],
        [base, { ...second, id: 'filter-123' }, { type: 'BandReject', frequency: 50 }],
        [
          { ...second, id: 'x' },
          { type: 'BandReject', frequency: 50 },
        ],
        [
          { type: 'BandReject', frequency: 50 },
          { ...second, id: 'x' },
        ],
      ];
      for (const filters of steps) {
        const input = path.wrap(equalizerOf(filters));
        const once = coerceSettings('realtime', input);
        const again = coerceSettings('realtime', input);
        expect(again).toEqual(once);
        expect(coerceSettings('realtime', once)).toEqual(once);
        for (const filter of filtersOf(path, equalizerOf(filters))) {
          expect(filter).not.toHaveProperty('id');
        }
      }
    });
  });

  it('keeps enabled as a boolean', () => {
    const equalizer = path.read(
      coerceSettings('realtime', path.wrap({ enabled: 'true', filters: [] }))
    );
    expect(equalizer?.enabled).toBe(true);
  });

  // The global equalizer predates this rule and coerces any object, an array included
  const untouchedCases = [
    { name: 'missing key', value: undefined, hasKey: false },
    { name: 'undefined', value: undefined, hasKey: true },
    { name: 'null', value: null, hasKey: true },
    ...(path.name === 'realtime.audio.equalizer'
      ? []
      : [{ name: 'an array', value: [], hasKey: true }]),
    { name: 'a string', value: 'x', hasKey: true },
  ];

  it.each(untouchedCases)('leaves an owner with $name equalizer untouched', ({ value, hasKey }) => {
    const input = path.wrapWithout(hasKey ? { equalizer: value } : {});
    const ownerOut = path.readOwner(coerceSettings('realtime', input));
    expect('equalizer' in ownerOut).toBe(hasKey);
    expect(ownerOut.equalizer).toEqual(value);
  });
});
