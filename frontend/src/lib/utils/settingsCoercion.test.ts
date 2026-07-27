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

describe('settingsCoercion species guide show flags', () => {
  it('defaults an absent showTaxonomy flag to true (backend *bool semantics)', () => {
    const result = coerceSettings('realtime', {
      dashboard: { speciesGuide: { enabled: true } },
    }) as { dashboard: { speciesGuide: Record<string, unknown> } };

    expect(result.dashboard.speciesGuide.showTaxonomy).toBe(true);
  });

  it('preserves an explicit showTaxonomy opt-out', () => {
    const result = coerceSettings('realtime', {
      dashboard: { speciesGuide: { enabled: true, showTaxonomy: false } },
    }) as { dashboard: { speciesGuide: Record<string, unknown> } };

    expect(result.dashboard.speciesGuide.showTaxonomy).toBe(false);
  });

  // An absent speciesGuide key must stay absent — do NOT materialize defaults here.
  //
  // coerceSettings also runs over formData on the way OUT (settingsActions.saveSettings),
  // so synthesizing a section the caller never supplied would push client-side defaults
  // into the save payload and overwrite whatever the backend actually holds. That is why
  // every sibling section (audio, mqtt, species, rtsp, dashboard, falsePositiveFilter)
  // is guarded by the same hasOwnProperty check.
  //
  // Materializing defaults is also unnecessary: SpeciesGuideConfig is a non-pointer,
  // non-omitempty Go field and setDefaultConfig seeds all nine viper keys, so the
  // settings API always emits speciesGuide even for a config.yaml predating the feature.
  // Consumers additionally default it themselves (`?? false` / `?? true`).
  it('leaves an absent speciesGuide absent rather than synthesizing defaults', () => {
    const result = coerceSettings('realtime', {
      dashboard: { locale: 'en' },
    }) as { dashboard: Record<string, unknown> };

    expect('speciesGuide' in result.dashboard).toBe(false);
  });

  it('leaves an absent dashboard absent', () => {
    const result = coerceSettings('realtime', { audio: {} }) as Record<string, unknown>;

    expect('dashboard' in result).toBe(false);
  });
});
