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
});
