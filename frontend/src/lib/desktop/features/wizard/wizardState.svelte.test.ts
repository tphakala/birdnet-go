import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import type { TranslationKey } from '$lib/i18n';
import type { WizardStep } from './types';
import { deferred, type Deferred } from '../../../../test/async-helpers';

// Mock the API module before importing wizardState
vi.mock('$lib/utils/api', async importOriginal => ({
  ...(await importOriginal<typeof import('$lib/utils/api')>()),
  api: {
    post: vi.fn().mockResolvedValue({}),
  },
}));

// The wizard reports a save that fails after it closed in an error toast; translate
// to the key so the tests can name the message.
vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string) => key),
  getLocale: vi.fn(() => 'en'),
}));

vi.mock('$lib/stores/toast', () => ({
  toastActions: { error: vi.fn() },
}));

// Mock getStepsForFlow so we can control what steps are returned
vi.mock('./wizardRegistry', () => ({
  getStepsForFlow: vi.fn(() => []),
}));

// Import after mocks are set up
const { api, ApiError } = await import('$lib/utils/api');
const { wizardState, STEP_MOVE_GUARD_MS } = await import('./wizardState.svelte');
const { getStepsForFlow } = await import('./wizardRegistry');
const { toastActions } = await import('$lib/stores/toast');

// Test fixtures
function createTestSteps(count: number): WizardStep[] {
  return Array.from({ length: count }, (_, i) => ({
    id: `step-${i}`,
    type: 'content' as const,
    title: `Step ${i}`,
    content: `Content for step ${i}`,
  }));
}

// Component steps (the kind that can have a leave handler); `unfinishedSaveKeys[i]` is
// the unfinishedSaveKey of step i, if any.
function createComponentSteps(unfinishedSaveKeys: Array<TranslationKey | undefined>): WizardStep[] {
  return unfinishedSaveKeys.map((unfinishedSaveKey, i) => ({
    id: `component-${i}`,
    type: 'component' as const,
    titleKey: `component.${i}.title`,
    component: () => Promise.reject(new Error('not rendered in state tests')),
    unfinishedSaveKey,
  }));
}

// Marks the current step ready and lets the guard after a step move run out, as
// a user reading the step before the next click would.
function readyStep(valid = true): void {
  wizardState.setStepValid(valid);
  wizardState.setStepStatus('ready', wizardState.currentStepIndex);
  vi.advanceTimersByTime(STEP_MOVE_GUARD_MS);
}

// Lets chained promise continuations run.
async function flush(): Promise<void> {
  for (let i = 0; i < 5; i++) await Promise.resolve();
}

function launchSteps(count: number): void {
  vi.mocked(getStepsForFlow).mockReturnValue(createTestSteps(count));
  wizardState.launch('onboarding');
}

describe('wizardState - state machine', () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    vi.clearAllMocks();
    localStorage.clear();
    wizardState._resetForTesting();
    vi.mocked(getStepsForFlow).mockReturnValue([]);
  });

  afterEach(() => {
    vi.useRealTimers();
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
      expect(wizardState.stepStatus).toBe('loading');
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
      expect(wizardState.stepStatus).toBe('loading');
      expect(wizardState.isSaving).toBe(false);
    });

    it.each([
      ['not loaded and not valid', () => {}],
      ['ready but never reported valid', () => wizardState.setStepStatus('ready', 0)],
      ['ready and reported invalid', () => readyStep(false)],
      ['valid but not yet marked ready', () => wizardState.setStepValid(true)],
      [
        'valid but failed to load',
        () => {
          wizardState.setStepValid(true);
          wizardState.setStepStatus('failed', 0);
        },
      ],
    ])('refuses a step that is %s', async (_name, arrange) => {
      launchSteps(3);
      arrange();

      expect(wizardState.canAdvance).toBe(false);
      await wizardState.next();

      expect(wizardState.currentStepIndex).toBe(0);
    });

    it('advances once the refused step becomes ready and valid', async () => {
      launchSteps(3);
      wizardState.setStepStatus('ready', 0);
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
      expect(toastActions.error).not.toHaveBeenCalled();

      await wizardState.next(); // retry calls the handler again

      expect(handler).toHaveBeenCalledTimes(2);
      expect(wizardState.currentStepIndex).toBe(1);
      expect(wizardState.stepError).toBeNull();
    });

    it.each([400, 422])(
      'a save the server refuses as invalid (%i) shows the rejected message',
      async status => {
        launchSteps(3);
        const handler = vi
          .fn<() => Promise<void>>()
          .mockRejectedValueOnce(new ApiError('refused', status, new Response(null, { status })));
        wizardState.registerLeaveHandler(handler);
        readyStep();

        await wizardState.next();

        expect(wizardState.stepError).toBe('wizard.errors.saveRejected');
        expect(wizardState.currentStepIndex).toBe(0);
        expect(wizardState.canAdvance).toBe(true);
      }
    );

    it.each([
      ['a plain error', () => new Error('boom')],
      ['a 500 response', () => new ApiError('boom', 500, new Response(null, { status: 500 }))],
      ['a 403 response', () => new ApiError('boom', 403, new Response(null, { status: 403 }))],
      ['a network failure', () => new ApiError('offline', 0, new Response(null), true)],
    ])('%s keeps the connection message', async (_name, makeError) => {
      launchSteps(3);
      wizardState.registerLeaveHandler(
        vi.fn<() => Promise<void>>().mockRejectedValueOnce(makeError())
      );
      readyStep();

      await wizardState.next();

      expect(wizardState.stepError).toBe('wizard.errors.saveFailed');
    });

    it('a rejected save followed by a network failure shows the connection message', async () => {
      launchSteps(3);
      const handler = vi
        .fn<() => Promise<void>>()
        .mockRejectedValueOnce(new ApiError('refused', 400, new Response(null, { status: 400 })))
        .mockRejectedValueOnce(new ApiError('offline', 0, new Response(null), true))
        .mockResolvedValueOnce(undefined);
      wizardState.registerLeaveHandler(handler);
      readyStep();

      await wizardState.next();
      expect(wizardState.stepError).toBe('wizard.errors.saveRejected');

      await wizardState.next();
      expect(wizardState.stepError).toBe('wizard.errors.saveFailed');

      await wizardState.next();
      expect(wizardState.currentStepIndex).toBe(1);
      expect(wizardState.stepError).toBeNull();
    });
  });

  describe('back()', () => {
    it('decrements step index', async () => {
      launchSteps(3);
      readyStep();
      await wizardState.next();
      readyStep();

      await wizardState.back();

      expect(wizardState.currentStepIndex).toBe(0);
      expect(wizardState.isFirstStep).toBe(true);
      expect(wizardState.stepStatus).toBe('loading');
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

    it('runs the leave handler when going back from an invalid step', async () => {
      launchSteps(3);
      readyStep();
      await wizardState.next();
      const handler = vi.fn().mockResolvedValue(undefined);
      wizardState.registerLeaveHandler(handler);
      readyStep(false);

      await wizardState.back();

      expect(handler).toHaveBeenCalledTimes(1);
      expect(wizardState.currentStepIndex).toBe(0);
    });

    it('stays on an invalid step when its Back save fails', async () => {
      launchSteps(3);
      readyStep();
      await wizardState.next();
      wizardState.registerLeaveHandler(() => Promise.reject(new Error('nope')));
      readyStep(false);

      await wizardState.back();

      expect(wizardState.currentStepIndex).toBe(1);
      expect(wizardState.stepError).toBe('wizard.errors.saveFailed');
      expect(wizardState.isSaving).toBe(false);
    });

    it('refuses while the step is not ready, so a double click moves one step', async () => {
      launchSteps(4);
      readyStep();
      await wizardState.next();
      readyStep();
      await wizardState.next(); // index 2, step reported invalid below
      readyStep(false);

      const first = wizardState.back();
      const second = wizardState.back(); // the previous step has not loaded yet
      await Promise.all([first, second]);

      expect(wizardState.currentStepIndex).toBe(1);

      wizardState.setStepStatus('ready', 1);
      vi.advanceTimersByTime(STEP_MOVE_GUARD_MS);
      await wizardState.back();

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

    it('exposes the reason of an invalid report and clears it when valid', () => {
      launchSteps(3);

      wizardState.setStepValid(false, 0, 'wizard.reasons.completeStep');
      expect(wizardState.stepBlockedReason).toBe('wizard.reasons.completeStep');

      wizardState.setStepValid(true);
      expect(wizardState.stepBlockedReason).toBeNull();
    });

    it('drops the reason of an invalid step when Back moves to the previous step', async () => {
      launchSteps(3);
      readyStep();
      await wizardState.next();
      readyStep();
      wizardState.setStepValid(false, 1, 'wizard.reasons.completeStep');
      expect(wizardState.stepBlockedReason).toBe('wizard.reasons.completeStep');

      await wizardState.back();

      expect(wizardState.currentStepIndex).toBe(0);
      expect(wizardState.stepBlockedReason).toBeNull();
    });

    it('ignores a reason reported for another step', async () => {
      launchSteps(3);
      readyStep();
      await wizardState.next();

      wizardState.setStepValid(false, 0, 'wizard.reasons.completeStep');

      expect(wizardState.stepBlockedReason).toBeNull();
    });
  });

  describe('setStepStatus()', () => {
    it('ignores a stale index', async () => {
      launchSteps(3);
      readyStep();
      await wizardState.next();

      wizardState.setStepStatus('ready', 0);

      expect(wizardState.stepStatus).toBe('loading');
    });

    it('a step that failed to load blocks Next but allows Back', async () => {
      launchSteps(3);
      readyStep();
      await wizardState.next();

      wizardState.setStepStatus('failed', 1);
      vi.advanceTimersByTime(STEP_MOVE_GUARD_MS);

      expect(wizardState.canAdvance).toBe(false);
      expect(wizardState.canGoBack).toBe(true);
      await wizardState.back();
      expect(wizardState.currentStepIndex).toBe(0);
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
      const steps = createTestSteps(1);
      vi.mocked(getStepsForFlow).mockReturnValue(steps);
      wizardState.launch('whats-new', { currentVersion: 'v2.0' });

      wizardState.skip();

      expect(api.post).toHaveBeenCalledWith('/api/v2/app/wizard/dismiss');
    });

    it('skip closes at once, does not call the leave handler again and ignores the pending save', async () => {
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

    it('reports a save that fails after Skip in a toast and leaves the step state alone', async () => {
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
      expect(toastActions.error).toHaveBeenCalledTimes(1);
      expect(toastActions.error).toHaveBeenCalledWith('wizard.errors.saveUnfinished', {
        duration: null,
      });
    });

    it('shows no toast when a save started before Skip succeeds', async () => {
      launchSteps(3);
      const d = deferred();
      wizardState.registerLeaveHandler(() => d.promise);
      readyStep();
      const nav = wizardState.next();
      await flush();

      wizardState.skip();
      d.resolve();
      await nav;

      expect(toastActions.error).not.toHaveBeenCalled();
    });

    it('names the step that started the save when it fails after Skip and a relaunch', async () => {
      const audioKey: TranslationKey = 'wizard.errors.audioSourceSaveUnfinished';
      vi.mocked(getStepsForFlow).mockReturnValue(
        createComponentSteps([undefined, audioKey, undefined])
      );
      wizardState.launch('onboarding');
      wizardState.registerLeaveHandler(() => Promise.resolve());
      readyStep();
      await wizardState.next();
      expect(wizardState.currentStepIndex).toBe(1);

      const d = deferred();
      wizardState.registerLeaveHandler(() => d.promise);
      readyStep();
      const nav = wizardState.next();
      await flush();

      wizardState.skip();
      wizardState.launch('onboarding');
      expect(wizardState.currentStepIndex).toBe(0);
      d.reject(new Error('late'));
      await nav;

      expect(toastActions.error).toHaveBeenCalledTimes(1);
      expect(toastActions.error).toHaveBeenCalledWith(audioKey, { duration: null });
    });

    it('uses the generic message for a component step without its own', async () => {
      vi.mocked(getStepsForFlow).mockReturnValue(createComponentSteps([undefined, undefined]));
      wizardState.launch('onboarding');
      const d = deferred();
      wizardState.registerLeaveHandler(() => d.promise);
      readyStep();
      const nav = wizardState.next();
      await flush();

      wizardState.skip();
      d.reject(new Error('late'));
      await nav;

      expect(toastActions.error).toHaveBeenCalledWith('wizard.errors.saveUnfinished', {
        duration: null,
      });
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
      expect(wizardState.stepStatus).toBe('loading');
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
          wizardState.stepStatus === 'ready' &&
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

    it.each([
      ['the last step is not loaded', 1, () => {}],
      [
        'the last step is ready but never reported valid',
        1,
        () => wizardState.setStepStatus('ready', 0),
      ],
      ['the last step is ready and reported invalid', 1, () => readyStep(false)],
      ['the last step is valid but not yet marked ready', 1, () => wizardState.setStepValid(true)],
      [
        'the last step failed to load',
        1,
        () => {
          wizardState.setStepValid(true);
          wizardState.setStepStatus('failed', 0);
        },
      ],
      ['the wizard is not on the last step', 2, () => readyStep()],
    ] as const)('refuses when %s', async (_name, count, arrange) => {
      launchSteps(count);
      arrange();

      await wizardState.complete();

      expect(wizardState.isActive).toBe(true);
      expect(api.post).not.toHaveBeenCalled();
    });

    it('awaits the leave handler and then dismisses', async () => {
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
      launchSteps(1);
      wizardState.registerLeaveHandler(() => Promise.reject(new Error('nope')));
      readyStep();

      await wizardState.complete();

      expect(wizardState.isActive).toBe(true);
      expect(wizardState.stepError).toBe('wizard.errors.saveFailed');
      expect(api.post).not.toHaveBeenCalled();
      expect(toastActions.error).not.toHaveBeenCalled();
    });
  });

  describe('step move guard', () => {
    // A cached step chunk settles about this long after the click that moved to it
    const SETTLE_MS = 27;

    // Lets the step reached by a move load, then waits `ms` from the first click
    async function settleAndWait(ms: number): Promise<void> {
      await flush();
      vi.advanceTimersByTime(SETTLE_MS);
      wizardState.setStepValid(true);
      wizardState.setStepStatus('ready', wizardState.currentStepIndex);
      vi.advanceTimersByTime(ms - SETTLE_MS);
    }

    it.each([60, 200])(
      'a second Next click %i ms after the first moves only one step',
      async gap => {
        launchSteps(4);
        readyStep();

        const first = wizardState.next();
        await settleAndWait(gap);
        await wizardState.next();
        await first;

        expect(wizardState.currentStepIndex).toBe(1);
      }
    );

    it('Next clicks 0, 60 and 200 ms apart move one step', async () => {
      launchSteps(4);
      readyStep();

      const first = wizardState.next();
      const atZero = wizardState.next();
      await settleAndWait(60);
      await wizardState.next();
      vi.advanceTimersByTime(140);
      await wizardState.next();
      await Promise.all([first, atZero]);

      expect(wizardState.currentStepIndex).toBe(1);
    });

    it('a Next click after the guard interval moves again', async () => {
      launchSteps(4);
      readyStep();

      await wizardState.next();
      await settleAndWait(SETTLE_MS);
      vi.advanceTimersByTime(STEP_MOVE_GUARD_MS - 1);
      await wizardState.next();
      expect(wizardState.currentStepIndex).toBe(1);

      vi.advanceTimersByTime(1);
      await wizardState.next();

      expect(wizardState.currentStepIndex).toBe(2);
    });

    it('counts the interval from when the step settled, not from the click', async () => {
      launchSteps(4);
      readyStep();

      await wizardState.next();
      vi.advanceTimersByTime(STEP_MOVE_GUARD_MS * 2); // the step is still loading
      wizardState.setStepValid(true);
      wizardState.setStepStatus('ready', 1);
      await wizardState.next();

      expect(wizardState.currentStepIndex).toBe(1);
    });

    it('a double click on Back moves one step', async () => {
      launchSteps(4);
      readyStep();
      await wizardState.next();
      readyStep();
      await wizardState.next();
      readyStep();

      const first = wizardState.back();
      await settleAndWait(120);
      await wizardState.back();
      await first;

      expect(wizardState.currentStepIndex).toBe(1);
      vi.advanceTimersByTime(STEP_MOVE_GUARD_MS);
      await wizardState.back();
      expect(wizardState.currentStepIndex).toBe(0);
    });

    it('applies after a move to a step that failed to load', async () => {
      launchSteps(4);
      readyStep();
      await wizardState.next();
      readyStep();

      const first = wizardState.next();
      await flush();
      wizardState.setStepStatus('failed', 2);
      vi.advanceTimersByTime(100);
      await wizardState.back();
      await first;

      expect(wizardState.currentStepIndex).toBe(2);
    });

    it('a double click that lands on the last step does not finish', async () => {
      launchSteps(2);
      readyStep();

      const first = wizardState.next();
      await settleAndWait(150);
      await wizardState.complete();
      await first;

      expect(wizardState.isActive).toBe(true);
      expect(wizardState.currentStepIndex).toBe(1);
      expect(api.post).not.toHaveBeenCalled();

      vi.advanceTimersByTime(STEP_MOVE_GUARD_MS);
      await wizardState.complete();
      expect(wizardState.isActive).toBe(false);
    });

    it('does not delay the first step after launch', async () => {
      launchSteps(3);
      wizardState.setStepValid(true);
      wizardState.setStepStatus('ready', 0);

      await wizardState.next();

      expect(wizardState.currentStepIndex).toBe(1);
    });

    it('does not block Skip right after a step move', async () => {
      launchSteps(3);
      readyStep();
      await wizardState.next();
      await settleAndWait(SETTLE_MS);

      wizardState.skip();

      expect(wizardState.isActive).toBe(false);
    });

    it('a relaunch starts without the guard of the previous session', async () => {
      launchSteps(3);
      readyStep();
      await wizardState.next();
      await settleAndWait(SETTLE_MS);
      wizardState.skip();

      launchSteps(3);
      wizardState.setStepValid(true);
      wizardState.setStepStatus('ready', 0);
      await wizardState.next();

      expect(wizardState.currentStepIndex).toBe(1);
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
      ['ready', 'next', 'resolve', 'ready', 'notValid', 'back', 'reject', 'back', 'resolve'],
      ['ready', 'next', 'resolve', 'ready', 'notValid', 'back', 'resolve'],
      ['ready', 'next', 'relaunch', 'resolve', 'ready', 'next', 'resolve'],
      ['next', 'complete', 'back', 'skip', 'complete'],
      ['ready', 'next', 'skip', 'reject'],
      ['ready', 'next', 'relaunch', 'reject', 'ready', 'next', 'reject'],
      ['ready', 'next', 'skip', 'resolve', 'skip'],
    ];

    it.each(sequences.map(seq => [seq.join(' > '), seq] as const))(
      'keeps invariants for %s',
      async (_name, sequence) => {
        const total = 3;
        launchSteps(total);
        const pending: Array<{ d: Deferred; generation: number; settled: boolean }> = [];
        let generation = 0;
        let allowDismiss = false;
        // A rejection that outlives its session is reported once in a toast; one
        // in the open session is shown on the step and never toasted.
        let expectedToasts = 0;
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
                else {
                  if (entry.generation !== generation) expectedToasts++;
                  entry.d.reject(new Error('rejected'));
                }
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
          const toasts = vi.mocked(toastActions.error).mock.calls.length;
          if (toasts !== expectedToasts) {
            violations.push(`${op}: ${toasts} toasts, expected ${expectedToasts}`);
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
