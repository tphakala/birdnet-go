import { describe, it, expect } from 'vitest';
import { getStepsForFlow } from './wizardRegistry';

describe('wizardRegistry — getStepsForFlow()', () => {
  describe('onboarding flow', () => {
    it('returns empty array when no steps are registered', () => {
      const steps = getStepsForFlow('onboarding');
      expect(steps).toEqual([]);
    });
  });

  describe('whats-new flow', () => {
    it('returns empty array with previousVersion and currentVersion provided', () => {
      const steps = getStepsForFlow('whats-new', {
        previousVersion: 'v1',
        currentVersion: 'v2',
      });
      expect(steps).toEqual([]);
    });

    it('returns empty array when no options are provided', () => {
      const steps = getStepsForFlow('whats-new');
      expect(steps).toEqual([]);
    });

    it('returns empty array when previousVersion is undefined', () => {
      const steps = getStepsForFlow('whats-new', {
        previousVersion: undefined,
      });
      expect(steps).toEqual([]);
    });

    it('returns empty array when currentVersion is undefined', () => {
      const steps = getStepsForFlow('whats-new', {
        currentVersion: undefined,
      });
      expect(steps).toEqual([]);
    });

    it('returns empty array when both versions are undefined', () => {
      const steps = getStepsForFlow('whats-new', {
        previousVersion: undefined,
        currentVersion: undefined,
      });
      expect(steps).toEqual([]);
    });
  });
});
