import { describe, expect, it } from 'vitest';
import { coerceSettings } from './settingsCoercion';

describe('settingsCoercion low-noise auto suspend', () => {
  it('backfills lowNoiseAutoSuspend defaults for audio sources', () => {
    const coerced = coerceSettings('realtime', {
      audio: {
        sources: [{ name: 'Mic 1', device: 'sysdefault', gain: 0, models: ['birdnet'] }],
      },
    }) as {
      audio?: { sources?: Array<{ lowNoiseAutoSuspend?: { enabled: boolean; suspendThreshold: number } }> };
    };

    expect(coerced.audio?.sources?.[0].lowNoiseAutoSuspend).toEqual({
      enabled: false,
      suspendThreshold: 15,
      resumeThreshold: 25,
      minSuspendFrames: 3,
      minResumeFrames: 2,
    });
  });

  it('clamps lowNoiseAutoSuspend values into valid ranges', () => {
    const coerced = coerceSettings('realtime', {
      audio: {
        sources: [
          {
            name: 'Mic 1',
            device: 'sysdefault',
            gain: 0,
            models: ['birdnet'],
            lowNoiseAutoSuspend: {
              enabled: true,
              suspendThreshold: -10,
              resumeThreshold: 120,
              minSuspendFrames: -1,
              minResumeFrames: 2000,
            },
          },
        ],
      },
    }) as {
      audio?: {
        sources?: Array<{
          lowNoiseAutoSuspend?: {
            enabled: boolean;
            suspendThreshold: number;
            resumeThreshold: number;
            minSuspendFrames: number;
            minResumeFrames: number;
          };
        }>;
      };
    };

    expect(coerced.audio?.sources?.[0].lowNoiseAutoSuspend).toEqual({
      enabled: true,
      suspendThreshold: 0,
      resumeThreshold: 100,
      minSuspendFrames: 0,
      minResumeFrames: 1000,
    });
  });
});
