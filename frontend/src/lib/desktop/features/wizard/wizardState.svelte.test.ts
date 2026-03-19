import { describe, it, expect, vi, beforeEach } from 'vitest';
import type { WizardStep } from './types';

// Mock the API module before importing wizardState
vi.mock('$lib/utils/api', () => ({
  api: {
    post: vi.fn().mockResolvedValue({}),
  },
}));

// Mock getStepsForFlow so we can control what steps are returned
vi.mock('./wizardRegistry', () => ({
  getStepsForFlow: vi.fn(() => []),
}));

// Import after mocks are set up
const { wizardState } = await import('./wizardState.svelte');
const { getStepsForFlow } = await import('./wizardRegistry');

// Test fixtures
function createTestSteps(count: number): WizardStep[] {
  return Array.from({ length: count }, (_, i) => ({
    id: `step-${i}`,
    type: 'content' as const,
    title: `Step ${i}`,
    content: `Content for step ${i}`,
  }));
}

describe('wizardState — state machine', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    wizardState._resetForTesting();
    vi.mocked(getStepsForFlow).mockReturnValue([]);
  });

  describe('initial state', () => {
    it('starts with idle status', () => {
      expect(wizardState.status).toBe('idle');
    });

    it('has null flow when not active', () => {
      expect(wizardState.flow).toBeNull();
    });

    it('has null currentStep when not active', () => {
      expect(wizardState.currentStep).toBeNull();
    });

    it('is not active when idle or completed', () => {
      expect(wizardState.isActive).toBe(false);
    });
  });

  describe('launch()', () => {
    it('does not activate when getStepsForFlow returns empty array', () => {
      vi.mocked(getStepsForFlow).mockReturnValue([]);

      wizardState.launch('onboarding');

      expect(wizardState.isActive).toBe(false);
      expect(wizardState.flow).toBeNull();
    });

    it('activates when steps are available', () => {
      const steps = createTestSteps(3);
      vi.mocked(getStepsForFlow).mockReturnValue(steps);

      wizardState.launch('onboarding');

      expect(wizardState.isActive).toBe(true);
      expect(wizardState.status).toBe('active');
      expect(wizardState.flow).toBe('onboarding');
      expect(wizardState.currentStepIndex).toBe(0);
      expect(wizardState.totalSteps).toBe(3);
      expect(wizardState.currentStep).toEqual(steps[0]);
    });

    it('sets isFirstStep and isLastStep correctly', () => {
      const steps = createTestSteps(3);
      vi.mocked(getStepsForFlow).mockReturnValue(steps);

      wizardState.launch('onboarding');

      expect(wizardState.isFirstStep).toBe(true);
      expect(wizardState.isLastStep).toBe(false);
    });

    it('sets isStepValid to true on launch', () => {
      const steps = createTestSteps(2);
      vi.mocked(getStepsForFlow).mockReturnValue(steps);

      wizardState.launch('onboarding');

      expect(wizardState.isStepValid).toBe(true);
    });

    it('sets isLastStep true when only one step', () => {
      const steps = createTestSteps(1);
      vi.mocked(getStepsForFlow).mockReturnValue(steps);

      wizardState.launch('onboarding');

      expect(wizardState.isFirstStep).toBe(true);
      expect(wizardState.isLastStep).toBe(true);
    });

    it('passes options through to getStepsForFlow', () => {
      vi.mocked(getStepsForFlow).mockReturnValue([]);
      const options = { previousVersion: 'v1.0', currentVersion: 'v2.0' };

      wizardState.launch('whats-new', options);

      expect(getStepsForFlow).toHaveBeenCalledWith('whats-new', options);
    });

    it('stores previousVersion from options', () => {
      const steps = createTestSteps(1);
      vi.mocked(getStepsForFlow).mockReturnValue(steps);

      wizardState.launch('whats-new', {
        previousVersion: 'v1.0',
        currentVersion: 'v2.0',
      });

      expect(wizardState.previousVersion).toBe('v1.0');
    });
  });

  describe('next()', () => {
    it('advances step index', () => {
      const steps = createTestSteps(3);
      vi.mocked(getStepsForFlow).mockReturnValue(steps);
      wizardState.launch('onboarding');

      wizardState.next();

      expect(wizardState.currentStepIndex).toBe(1);
      expect(wizardState.currentStep).toEqual(steps[1]);
      expect(wizardState.isFirstStep).toBe(false);
    });

    it('does not advance past last step', () => {
      const steps = createTestSteps(2);
      vi.mocked(getStepsForFlow).mockReturnValue(steps);
      wizardState.launch('onboarding');

      wizardState.next(); // index 1 — last step
      wizardState.next(); // should not go past

      expect(wizardState.currentStepIndex).toBe(1);
      expect(wizardState.isLastStep).toBe(true);
    });

    it('resets isStepValid to true after advancing', () => {
      const steps = createTestSteps(3);
      vi.mocked(getStepsForFlow).mockReturnValue(steps);
      wizardState.launch('onboarding');

      wizardState.setStepValid(false);
      // Can't advance when invalid, so set valid again
      wizardState.setStepValid(true);
      wizardState.next();

      expect(wizardState.isStepValid).toBe(true);
    });
  });

  describe('back()', () => {
    it('decrements step index', () => {
      const steps = createTestSteps(3);
      vi.mocked(getStepsForFlow).mockReturnValue(steps);
      wizardState.launch('onboarding');
      wizardState.next();

      wizardState.back();

      expect(wizardState.currentStepIndex).toBe(0);
      expect(wizardState.isFirstStep).toBe(true);
    });

    it('does not go below zero', () => {
      const steps = createTestSteps(3);
      vi.mocked(getStepsForFlow).mockReturnValue(steps);
      wizardState.launch('onboarding');

      wizardState.back(); // already at 0

      expect(wizardState.currentStepIndex).toBe(0);
      expect(wizardState.isFirstStep).toBe(true);
    });
  });

  describe('setStepValid()', () => {
    it('prevents next() when set to false', () => {
      const steps = createTestSteps(3);
      vi.mocked(getStepsForFlow).mockReturnValue(steps);
      wizardState.launch('onboarding');

      wizardState.setStepValid(false);
      wizardState.next();

      expect(wizardState.currentStepIndex).toBe(0);
      expect(wizardState.isStepValid).toBe(false);
    });

    it('allows next() when set back to true', () => {
      const steps = createTestSteps(3);
      vi.mocked(getStepsForFlow).mockReturnValue(steps);
      wizardState.launch('onboarding');

      wizardState.setStepValid(false);
      wizardState.setStepValid(true);
      wizardState.next();

      expect(wizardState.currentStepIndex).toBe(1);
    });
  });

  describe('skip()', () => {
    it('resets state to completed', async () => {
      const steps = createTestSteps(3);
      vi.mocked(getStepsForFlow).mockReturnValue(steps);
      wizardState.launch('onboarding');

      await wizardState.skip();

      expect(wizardState.status).toBe('completed');
      expect(wizardState.isActive).toBe(false);
      expect(wizardState.flow).toBeNull();
      expect(wizardState.currentStep).toBeNull();
    });

    it('calls dismiss API', async () => {
      const { api } = await import('$lib/utils/api');
      const steps = createTestSteps(1);
      vi.mocked(getStepsForFlow).mockReturnValue(steps);
      wizardState.launch('whats-new', { currentVersion: 'v2.0' });

      await wizardState.skip();

      expect(api.post).toHaveBeenCalledWith('/api/v2/app/wizard/dismiss');
    });
  });

  describe('complete()', () => {
    it('resets state to completed', async () => {
      const steps = createTestSteps(3);
      vi.mocked(getStepsForFlow).mockReturnValue(steps);
      wizardState.launch('onboarding');

      await wizardState.complete();

      expect(wizardState.status).toBe('completed');
      expect(wizardState.isActive).toBe(false);
      expect(wizardState.flow).toBeNull();
      expect(wizardState.currentStep).toBeNull();
    });

    it('calls dismiss API', async () => {
      const { api } = await import('$lib/utils/api');
      const steps = createTestSteps(1);
      vi.mocked(getStepsForFlow).mockReturnValue(steps);
      wizardState.launch('onboarding', { currentVersion: 'v3.0' });

      await wizardState.complete();

      expect(api.post).toHaveBeenCalledWith('/api/v2/app/wizard/dismiss');
    });

    it('sets localStorage dismissed version', async () => {
      const steps = createTestSteps(1);
      vi.mocked(getStepsForFlow).mockReturnValue(steps);
      wizardState.launch('whats-new', { currentVersion: 'v2.5' });

      await wizardState.complete();

      expect(localStorage.getItem('birdnet-wizard-dismissed-version')).toBe('v2.5');
    });
  });
});
