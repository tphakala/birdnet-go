import { describe, expect, it } from 'vitest';
import { hasValidLowNoiseAutoSuspendThresholds } from './lowNoiseAutoSuspend';

describe('hasValidLowNoiseAutoSuspendThresholds', () => {
  it('returns true when feature is disabled', () => {
    expect(
      hasValidLowNoiseAutoSuspendThresholds({
        enabled: false,
        suspendThreshold: 30,
        resumeThreshold: 20,
        minSuspendFrames: 3,
        minResumeFrames: 2,
      })
    ).toBe(true);
  });

  it('returns true when resume threshold is greater than suspend threshold', () => {
    expect(
      hasValidLowNoiseAutoSuspendThresholds({
        enabled: true,
        suspendThreshold: 15,
        resumeThreshold: 25,
        minSuspendFrames: 3,
        minResumeFrames: 2,
      })
    ).toBe(true);
  });

  it('returns false when resume threshold is equal to suspend threshold', () => {
    expect(
      hasValidLowNoiseAutoSuspendThresholds({
        enabled: true,
        suspendThreshold: 20,
        resumeThreshold: 20,
        minSuspendFrames: 3,
        minResumeFrames: 2,
      })
    ).toBe(false);
  });

  it('returns false when resume threshold is lower than suspend threshold', () => {
    expect(
      hasValidLowNoiseAutoSuspendThresholds({
        enabled: true,
        suspendThreshold: 25,
        resumeThreshold: 15,
        minSuspendFrames: 3,
        minResumeFrames: 2,
      })
    ).toBe(false);
  });
});
