import { describe, it, expect } from 'vitest';
import { getStepsForFlow } from './wizardRegistry';

describe('wizardRegistry — getStepsForFlow()', () => {
  describe('onboarding flow', () => {
    it('returns 6 steps for onboarding flow', () => {
      const steps = getStepsForFlow('onboarding');
      expect(steps).toHaveLength(6);
    });

    it('all onboarding steps are component type', () => {
      const steps = getStepsForFlow('onboarding');
      for (const step of steps) {
        expect(step.type).toBe('component');
      }
    });

    it('onboarding steps have unique IDs', () => {
      const steps = getStepsForFlow('onboarding');
      const ids = steps.map(s => s.id);
      expect(new Set(ids).size).toBe(ids.length);
    });

    it('first onboarding step is welcome', () => {
      const steps = getStepsForFlow('onboarding');
      expect(steps[0].id).toBe('welcome');
    });

    it('last onboarding step is responsible-use', () => {
      const steps = getStepsForFlow('onboarding');
      expect(steps[steps.length - 1].id).toBe('responsible-use');
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
