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
  // An absent FIELD must stay absent, for the same reason an absent section does
  // (see the comment below): coerceSettings runs over formData on the way OUT, so
  // materializing a flag the caller never supplied turns a partial update into a
  // full overwrite. A PATCH carrying only `enabled` used to rewrite showNotes back
  // to true for a user who had deliberately turned it off, and reset warmTopN to 50.
  //
  // Consumers apply the backend's *bool "unset means on" semantics themselves when
  // reading (toSpeciesGuideUIConfig), so nothing depends on them being filled here.
  it('leaves an absent showTaxonomy flag absent rather than synthesizing a default', () => {
    const result = coerceSettings('realtime', {
      dashboard: { speciesGuide: { enabled: true } },
    }) as { dashboard: { speciesGuide: Record<string, unknown> } };

    expect(result.dashboard.speciesGuide).not.toHaveProperty('showTaxonomy');
    expect(result.dashboard.speciesGuide.enabled).toBe(true);
  });

  it('does not resurrect other flags when only one field is supplied', () => {
    const result = coerceSettings('realtime', {
      dashboard: { speciesGuide: { enableWikipedia: true } },
    }) as { dashboard: { speciesGuide: Record<string, unknown> } };

    expect(result.dashboard.speciesGuide).toEqual({ enableWikipedia: true });
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
        threshold: 0.35,
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
