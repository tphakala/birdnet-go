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

function readyStep(valid = true): void {
  wizardState.setStepValid(valid);
  wizardState.markStepReady(wizardState.currentStepIndex);
}

interface Deferred {
  promise: Promise<void>;
  resolve: () => void;
  reject: (err: Error) => void;
}

function deferred(): Deferred {
  let resolve!: () => void;
  let reject!: (err: Error) => void;
  const promise = new Promise<void>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

// Lets chained promise continuations run.
async function flush(): Promise<void> {
  for (let i = 0; i < 5; i++) await Promise.resolve();
}

function launchSteps(count: number): void {
  vi.mocked(getStepsForFlow).mockReturnValue(createTestSteps(count));
  wizardState.launch('onboarding');
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

    it('starts each step not ready and not valid', () => {
      const steps = createTestSteps(2);
      vi.mocked(getStepsForFlow).mockReturnValue(steps);

      wizardState.launch('onboarding');

      expect(wizardState.isStepValid).toBe(false);
      expect(wizardState.isStepReady).toBe(false);
      expect(wizardState.canAdvance).toBe(false);
      expect(wizardState.isSaving).toBe(false);
      expect(wizardState.stepError).toBeNull();
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
    it('advances step index', async () => {
      const steps = createTestSteps(3);
      vi.mocked(getStepsForFlow).mockReturnValue(steps);
      wizardState.launch('onboarding');
      readyStep();

      await wizardState.next();

      expect(wizardState.currentStepIndex).toBe(1);
      expect(wizardState.currentStep).toEqual(steps[1]);
      expect(wizardState.isFirstStep).toBe(false);
    });

    it('does not advance past last step', async () => {
      launchSteps(2);
      readyStep();
      await wizardState.next(); // index 1, last step
      readyStep();

      await wizardState.next(); // should not go past

      expect(wizardState.currentStepIndex).toBe(1);
      expect(wizardState.isLastStep).toBe(true);
    });

    it('resets readiness and validity after advancing', async () => {
      launchSteps(3);
      readyStep();

      await wizardState.next();

      expect(wizardState.isStepValid).toBe(false);
      expect(wizardState.isStepReady).toBe(false);
      expect(wizardState.isSaving).toBe(false);
    });

    it('does nothing until the step is ready and valid', async () => {
      launchSteps(3);

      await wizardState.next();
      expect(wizardState.currentStepIndex).toBe(0);

      wizardState.markStepReady(0); // ready but never reported valid
      await wizardState.next();
      expect(wizardState.currentStepIndex).toBe(0);

      wizardState.setStepValid(true);
      await wizardState.next();
      expect(wizardState.currentStepIndex).toBe(1);
    });

    it('awaits the leave handler before advancing', async () => {
      launchSteps(3);
      const d = deferred();
      const handler = vi.fn(() => d.promise);
      wizardState.registerLeaveHandler(handler);
      readyStep();

      const nav = wizardState.next();
      await flush();

      expect(handler).toHaveBeenCalledTimes(1);
      expect(wizardState.currentStepIndex).toBe(0);
      expect(wizardState.isSaving).toBe(true);

      d.resolve();
      await nav;

      expect(wizardState.currentStepIndex).toBe(1);
      expect(wizardState.isSaving).toBe(false);
    });

    it('ignores a second next while saving so the handler runs once', async () => {
      launchSteps(3);
      const d = deferred();
      const handler = vi.fn(() => d.promise);
      wizardState.registerLeaveHandler(handler);
      readyStep();

      const first = wizardState.next();
      const second = wizardState.next();
      d.resolve();
      await Promise.all([first, second]);

      expect(handler).toHaveBeenCalledTimes(1);
      expect(wizardState.currentStepIndex).toBe(1);
    });

    it('keeps the step and sets stepError when the leave handler rejects', async () => {
      launchSteps(3);
      const failing = deferred();
      const handler = vi
        .fn<() => Promise<void>>()
        .mockImplementationOnce(() => failing.promise)
        .mockResolvedValueOnce(undefined);
      wizardState.registerLeaveHandler(handler);
      readyStep();

      const nav = wizardState.next();
      failing.reject(new Error('save failed'));
      await nav;

      expect(wizardState.currentStepIndex).toBe(0);
      expect(wizardState.stepError).toBe('wizard.errors.saveFailed');
      expect(wizardState.isSaving).toBe(false);
      expect(wizardState.canAdvance).toBe(true);

      await wizardState.next(); // retry calls the handler again

      expect(handler).toHaveBeenCalledTimes(2);
      expect(wizardState.currentStepIndex).toBe(1);
      expect(wizardState.stepError).toBeNull();
    });
  });

  describe('back()', () => {
    it('decrements step index', async () => {
      launchSteps(3);
      readyStep();
      await wizardState.next();

      await wizardState.back();

      expect(wizardState.currentStepIndex).toBe(0);
      expect(wizardState.isFirstStep).toBe(true);
      expect(wizardState.isStepReady).toBe(false);
      expect(wizardState.isStepValid).toBe(false);
    });

    it('does not go below zero', async () => {
      launchSteps(3);

      await wizardState.back(); // already at 0

      expect(wizardState.currentStepIndex).toBe(0);
      expect(wizardState.isFirstStep).toBe(true);
    });

    it('awaits the leave handler when the step is valid', async () => {
      launchSteps(3);
      readyStep();
      await wizardState.next();
      const d = deferred();
      const handler = vi.fn(() => d.promise);
      wizardState.registerLeaveHandler(handler);
      readyStep();

      const nav = wizardState.back();
      await flush();

      expect(handler).toHaveBeenCalledTimes(1);
      expect(wizardState.currentStepIndex).toBe(1);
      expect(wizardState.isSaving).toBe(true);

      d.resolve();
      await nav;

      expect(wizardState.currentStepIndex).toBe(0);
      expect(wizardState.isSaving).toBe(false);
    });

    it('skips the leave handler when the step is invalid', async () => {
      launchSteps(3);
      readyStep();
      await wizardState.next();
      const handler = vi.fn().mockResolvedValue(undefined);
      wizardState.registerLeaveHandler(handler);
      readyStep(false);

      await wizardState.back();

      expect(handler).not.toHaveBeenCalled();
      expect(wizardState.currentStepIndex).toBe(0);
    });

    it('stays on the step when the leave handler rejects', async () => {
      launchSteps(3);
      readyStep();
      await wizardState.next();
      wizardState.registerLeaveHandler(() => Promise.reject(new Error('nope')));
      readyStep();

      await wizardState.back();

      expect(wizardState.currentStepIndex).toBe(1);
      expect(wizardState.stepError).toBe('wizard.errors.saveFailed');
    });
  });

  describe('setStepValid()', () => {
    it('prevents next() when set to false', async () => {
      launchSteps(3);
      readyStep(false);

      await wizardState.next();

      expect(wizardState.currentStepIndex).toBe(0);
      expect(wizardState.isStepValid).toBe(false);
    });

    it('allows next() when set back to true', async () => {
      launchSteps(3);
      readyStep(false);
      wizardState.setStepValid(true);

      await wizardState.next();

      expect(wizardState.currentStepIndex).toBe(1);
    });

    it('ignores a report for another step', async () => {
      launchSteps(3);
      readyStep();
      await wizardState.next();

      wizardState.setStepValid(true, 0);

      expect(wizardState.isStepValid).toBe(false);
    });
  });

  describe('markStepReady()', () => {
    it('ignores a stale index', async () => {
      launchSteps(3);
      readyStep();
      await wizardState.next();

      wizardState.markStepReady(0);

      expect(wizardState.isStepReady).toBe(false);
    });
  });

  describe('registerLeaveHandler()', () => {
    it('keeps the newer handler when a replaced one unregisters', async () => {
      launchSteps(3);
      const oldHandler = vi.fn().mockResolvedValue(undefined);
      const newHandler = vi.fn().mockResolvedValue(undefined);
      const unregisterOld = wizardState.registerLeaveHandler(oldHandler);
      wizardState.registerLeaveHandler(newHandler);
      unregisterOld();
      readyStep();

      await wizardState.next();

      expect(oldHandler).not.toHaveBeenCalled();
      expect(newHandler).toHaveBeenCalledTimes(1);
    });

    it('clears its own handler when unregistered', async () => {
      launchSteps(3);
      const handler = vi.fn().mockResolvedValue(undefined);
      const unregister = wizardState.registerLeaveHandler(handler);
      unregister();
      readyStep();

      await wizardState.next();

      expect(handler).not.toHaveBeenCalled();
    });
  });

  describe('skip()', () => {
    it('resets state to completed', () => {
      const steps = createTestSteps(3);
      vi.mocked(getStepsForFlow).mockReturnValue(steps);
      wizardState.launch('onboarding');

      wizardState.skip();

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

      wizardState.skip();

      expect(api.post).toHaveBeenCalledWith('/api/v2/app/wizard/dismiss');
    });

    it('never calls the leave handler and closes at once while a save is pending', async () => {
      const { api } = await import('$lib/utils/api');
      launchSteps(3);
      const d = deferred();
      const handler = vi.fn(() => d.promise);
      wizardState.registerLeaveHandler(handler);
      readyStep();
      const nav = wizardState.next();
      await flush();
      expect(handler).toHaveBeenCalledTimes(1);

      wizardState.skip();

      expect(wizardState.status).toBe('completed');
      expect(handler).toHaveBeenCalledTimes(1);
      expect(api.post).toHaveBeenCalledTimes(1);

      d.resolve();
      await nav;

      expect(wizardState.status).toBe('completed');
      expect(wizardState.currentStepIndex).toBe(0);
      expect(wizardState.stepError).toBeNull();
    });

    it('ignores a late failure of a save started before the skip', async () => {
      launchSteps(3);
      const d = deferred();
      wizardState.registerLeaveHandler(() => d.promise);
      readyStep();
      const nav = wizardState.next();
      await flush();

      wizardState.skip();
      d.reject(new Error('late'));
      await nav;

      expect(wizardState.stepError).toBeNull();
      expect(wizardState.isSaving).toBe(false);
    });
  });

  describe('session guard', () => {
    it('does not move a relaunched wizard when an old save resolves', async () => {
      launchSteps(3);
      const d = deferred();
      wizardState.registerLeaveHandler(() => d.promise);
      readyStep();
      const nav = wizardState.next();
      await flush();

      wizardState.skip();
      launchSteps(3);
      d.resolve();
      await nav;

      expect(wizardState.currentStepIndex).toBe(0);
      expect(wizardState.isStepReady).toBe(false);
      expect(wizardState.isSaving).toBe(false);
      expect(wizardState.isActive).toBe(true);
    });

    it('isSaving stays true until the index has moved', async () => {
      launchSteps(3);
      const d = deferred();
      wizardState.registerLeaveHandler(() => d.promise);
      readyStep();

      const observed: string[] = [];
      const sample = () => {
        if (
          wizardState.currentStepIndex === 0 &&
          wizardState.isStepReady &&
          wizardState.isStepValid &&
          !wizardState.isSaving
        ) {
          observed.push('old step looked ready and idle');
        }
      };
      const nav = wizardState.next();
      sample();
      d.resolve();
      for (let i = 0; i < 6; i++) {
        await Promise.resolve();
        // Skip the first sample point: before next() ran, the step is legitimately idle.
        if (i > 0) sample();
      }
      await nav;

      expect(observed).toEqual([]);
      expect(wizardState.currentStepIndex).toBe(1);
    });
  });

  describe('complete()', () => {
    it('resets state to completed', async () => {
      launchSteps(1);
      readyStep();

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
      readyStep();

      await wizardState.complete();

      expect(api.post).toHaveBeenCalledWith('/api/v2/app/wizard/dismiss');
    });

    it('sets localStorage dismissed version', async () => {
      const steps = createTestSteps(1);
      vi.mocked(getStepsForFlow).mockReturnValue(steps);
      wizardState.launch('whats-new', { currentVersion: 'v2.5' });
      readyStep();

      await wizardState.complete();

      expect(localStorage.getItem('birdnet-wizard-dismissed-version')).toBe('v2.5');
    });

    it('refuses when the last step is not ready or not valid', async () => {
      const { api } = await import('$lib/utils/api');
      launchSteps(1);

      await wizardState.complete();
      expect(wizardState.isActive).toBe(true);

      wizardState.markStepReady(0); // ready but not reported valid
      await wizardState.complete();
      expect(wizardState.isActive).toBe(true);

      wizardState.setStepValid(false);
      await wizardState.complete();

      expect(wizardState.isActive).toBe(true);
      expect(api.post).not.toHaveBeenCalled();
    });

    it('refuses when the wizard is not on the last step', async () => {
      const { api } = await import('$lib/utils/api');
      launchSteps(2);
      readyStep();

      await wizardState.complete();

      expect(wizardState.isActive).toBe(true);
      expect(api.post).not.toHaveBeenCalled();
    });

    it('awaits the leave handler and then dismisses', async () => {
      const { api } = await import('$lib/utils/api');
      launchSteps(1);
      const d = deferred();
      wizardState.registerLeaveHandler(() => d.promise);
      readyStep();

      const done = wizardState.complete();
      await flush();
      expect(wizardState.isActive).toBe(true);
      expect(api.post).not.toHaveBeenCalled();

      d.resolve();
      await done;

      expect(wizardState.status).toBe('completed');
      expect(api.post).toHaveBeenCalledTimes(1);
    });

    it('stays open and keeps the error when the leave handler rejects', async () => {
      const { api } = await import('$lib/utils/api');
      launchSteps(1);
      wizardState.registerLeaveHandler(() => Promise.reject(new Error('nope')));
      readyStep();

      await wizardState.complete();

      expect(wizardState.isActive).toBe(true);
      expect(wizardState.stepError).toBe('wizard.errors.saveFailed');
      expect(api.post).not.toHaveBeenCalled();
    });
  });

  describe('transition table', () => {
    type Op =
      | 'next'
      | 'back'
      | 'skip'
      | 'ready'
      | 'notValid'
      | 'resolve'
      | 'reject'
      | 'relaunch'
      | 'complete';

    const sequences: Op[][] = [
      ['ready', 'next', 'next', 'resolve'],
      ['ready', 'next', 'ready', 'next', 'complete', 'complete'],
      ['ready', 'next', 'ready', 'next', 'ready', 'complete', 'complete', 'resolve'],
      ['ready', 'next', 'skip', 'resolve'],
      ['ready', 'next', 'reject', 'next', 'resolve'],
      ['ready', 'next', 'resolve', 'back', 'ready', 'back', 'resolve'],
      ['ready', 'notValid', 'next', 'back'],
      ['ready', 'next', 'relaunch', 'resolve', 'ready', 'next', 'resolve'],
      ['next', 'complete', 'back', 'skip', 'complete'],
    ];

    it.each(sequences.map(seq => [seq.join(' > '), seq] as const))(
      'keeps invariants for %s',
      async (_name, sequence) => {
        const { api } = await import('$lib/utils/api');
        const total = 3;
        launchSteps(total);
        const pending: Array<{ d: Deferred; generation: number; settled: boolean }> = [];
        let generation = 0;
        let allowDismiss = false;
        const violations: string[] = [];

        for (const op of sequence) {
          const indexBefore = wizardState.currentStepIndex;
          const postsBefore = vi.mocked(api.post).mock.calls.length;
          const wasReadyLast =
            wizardState.isActive && wizardState.isLastStep && wizardState.canAdvance;

          switch (op) {
            case 'ready':
              wizardState.registerLeaveHandler(() => {
                const entry = { d: deferred(), generation, settled: false };
                pending.push(entry);
                return entry.d.promise;
              });
              readyStep();
              break;
            case 'notValid':
              wizardState.setStepValid(false);
              break;
            case 'next':
              void wizardState.next();
              break;
            case 'back':
              void wizardState.back();
              break;
            case 'complete':
              if (wasReadyLast) allowDismiss = true;
              void wizardState.complete();
              break;
            case 'skip':
              allowDismiss = true;
              generation++;
              wizardState.skip();
              break;
            case 'relaunch':
              generation++;
              launchSteps(total);
              break;
            case 'resolve':
            case 'reject': {
              const entry = pending.find(p => !p.settled);
              if (entry) {
                entry.settled = true;
                if (op === 'resolve') entry.d.resolve();
                else entry.d.reject(new Error('rejected'));
              }
              break;
            }
          }
          await flush();

          const index = wizardState.currentStepIndex;
          if (index < 0 || index >= total) violations.push(`${op}: index ${index} out of range`);
          if (wizardState.isActive && op !== 'relaunch' && Math.abs(index - indexBefore) > 1) {
            violations.push(`${op}: index jumped from ${indexBefore} to ${index}`);
          }
          if (!allowDismiss && vi.mocked(api.post).mock.calls.length !== postsBefore) {
            violations.push(`${op}: dismissed without skip or a ready last step`);
          }
          if (
            wizardState.isSaving &&
            !(wizardState.isActive && pending.some(p => !p.settled && p.generation === generation))
          ) {
            violations.push(`${op}: isSaving without a pending handler of this session`);
          }
        }

        expect(violations).toEqual([]);
      }
    );
  });
});
