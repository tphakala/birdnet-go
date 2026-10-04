import { describe, it, expect, vi, beforeEach } from 'vitest';
import type { Component } from 'svelte';
import { renderTyped, screen, waitFor } from '../../../../test/render-helpers';
import userEvent from '@testing-library/user-event';
import { expectNoA11yViolations } from '$lib/utils/axe-utils';
import type { WizardStep, WizardStepProps } from './types';

vi.mock('$lib/utils/api', () => ({
  api: {
    post: vi.fn().mockResolvedValue({}),
  },
}));

vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string) => key),
  getLocale: vi.fn(() => 'en'),
}));

vi.mock('./wizardRegistry', () => ({
  getStepsForFlow: vi.fn(() => []),
}));

const { api } = await import('$lib/utils/api');
const { getStepsForFlow } = await import('./wizardRegistry');
const { wizardState } = await import('./wizardState.svelte');
const { stepControl } = await import('./wizardTestStepControl');
const { default: WizardDialog } = await import('./WizardDialog.svelte');
const { default: WizardTestStep } = await import('./WizardTestStep.test.svelte');

type StepModule = { default: Component<WizardStepProps> };

interface Deferred<T> {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (err: Error) => void;
}

function deferred<T = void>(): Deferred<T> {
  let resolve!: (value: T) => void;
  let reject!: (err: Error) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

const loadStep = (): Promise<StepModule> =>
  Promise.resolve({ default: WizardTestStep as unknown as Component<WizardStepProps> });

// One loader per step index; tests replace entries to control chunk loading.
let loaders: Array<() => Promise<StepModule>> = [];

function componentSteps(count: number): WizardStep[] {
  loaders = Array.from({ length: count }, () => loadStep);
  return Array.from({ length: count }, (_, i) => ({
    id: `step-${i + 1}`,
    type: 'component' as const,
    titleKey: `test.step${i + 1}`,
    // eslint-disable-next-line security/detect-object-injection -- i is a bounded test index
    component: () => loaders[i](),
  }));
}

function renderWizard(steps: WizardStep[], flow: 'onboarding' | 'whats-new' = 'onboarding') {
  vi.mocked(getStepsForFlow).mockReturnValue(steps);
  wizardState.launch(flow, { currentVersion: 'v1' });
  return renderTyped(WizardDialog);
}

const primaryButton = () =>
  screen.getByRole('button', { name: /wizard\.(next|done|status\.saving)/ });
const backButton = () => screen.getByRole('button', { name: /wizard\.back/ });
const heading = () => screen.getByRole('heading', { level: 3 });

const isBlocked = (el: HTMLElement) => el.getAttribute('aria-disabled') === 'true';

async function waitForPrimaryEnabled() {
  await waitFor(() => expect(isBlocked(primaryButton())).toBe(false));
}

function describedText(el: HTMLElement): string {
  const id = el.getAttribute('aria-describedby');
  if (!id) return '';
  return document.getElementById(id)?.textContent ?? '';
}

describe('WizardDialog', () => {
  let user: ReturnType<typeof userEvent.setup>;

  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    wizardState._resetForTesting();
    stepControl.reset();
    user = userEvent.setup();
  });

  it('enables Next only after the step mounted and reported valid', async () => {
    renderWizard(componentSteps(3));

    await waitForPrimaryEnabled();

    expect(heading()).toHaveTextContent('test.step1');
  });

  it('keeps Next disabled with a reason while the step reports invalid', async () => {
    stepControl.validQueue = [false];
    renderWizard(componentSteps(3));

    await waitFor(() => expect(describedText(primaryButton())).toBe('wizard.reasons.completeStep'));

    expect(isBlocked(primaryButton())).toBe(true);
  });

  it('treats content steps as ready and valid at once', async () => {
    renderWizard([
      { id: 'c1', type: 'content', title: 'First', content: 'one' },
      { id: 'c2', type: 'content', title: 'Second', content: 'two' },
    ]);

    await waitForPrimaryEnabled();

    expect(heading()).toHaveTextContent('First');
  });

  it('Next waits for the step save and shows Saving', async () => {
    const save = deferred();
    stepControl.leave = vi.fn(() => save.promise);
    renderWizard(componentSteps(3));
    await waitForPrimaryEnabled();

    await user.click(primaryButton());

    await waitFor(() => expect(primaryButton()).toHaveTextContent('wizard.status.saving'));
    expect(isBlocked(primaryButton())).toBe(true);
    expect(describedText(primaryButton())).toBe('wizard.status.saving');
    expect(heading()).toHaveTextContent('test.step1');

    save.resolve();

    await waitFor(() => expect(heading()).toHaveTextContent('test.step2'));
  });

  it('double click during a save advances exactly one step', async () => {
    const save = deferred();
    stepControl.leave = vi.fn(() => save.promise);
    renderWizard(componentSteps(3));
    await waitForPrimaryEnabled();

    await user.dblClick(primaryButton());
    expect(stepControl.leave).toHaveBeenCalledTimes(1);

    save.resolve();

    await waitFor(() => expect(heading()).toHaveTextContent('test.step2'));
    await waitForPrimaryEnabled();
    expect(stepControl.leave).toHaveBeenCalledTimes(1);
    expect(heading()).toHaveTextContent('test.step2');
  });

  it('clicks while the next step loads do not advance', async () => {
    renderWizard(componentSteps(3));
    const load = deferred<StepModule>();
    loaders[1] = () => load.promise;
    await waitForPrimaryEnabled();

    await user.click(primaryButton());
    await waitFor(() => expect(heading()).toHaveTextContent('test.step2'));

    expect(describedText(primaryButton())).toBe('wizard.status.loadingStep');
    await user.click(primaryButton());
    expect(heading()).toHaveTextContent('test.step2');
    expect(stepControl.leave).toHaveBeenCalledTimes(1);

    load.resolve({ default: WizardTestStep as unknown as Component<WizardStepProps> });
    await waitForPrimaryEnabled();
    await user.click(primaryButton());

    await waitFor(() => expect(heading()).toHaveTextContent('test.step3'));
  });

  it('double click on the second to last step cannot finish without the last step valid', async () => {
    stepControl.validQueue = [true, false];
    renderWizard(componentSteps(2));
    const load = deferred<StepModule>();
    loaders[1] = () => load.promise;
    await waitForPrimaryEnabled();

    await user.dblClick(primaryButton());
    await waitFor(() => expect(heading()).toHaveTextContent('test.step2'));
    load.resolve({ default: WizardTestStep as unknown as Component<WizardStepProps> });

    await waitFor(() => expect(describedText(primaryButton())).toBe('wizard.reasons.completeStep'));
    expect(primaryButton()).toHaveTextContent('wizard.done');
    expect(isBlocked(primaryButton())).toBe(true);
    expect(wizardState.isActive).toBe(true);
    expect(api.post).not.toHaveBeenCalled();
  });

  it('a failed save keeps the step and shows an alert', async () => {
    stepControl.leave = vi
      .fn<() => Promise<void>>()
      .mockRejectedValueOnce(new Error('save failed'))
      .mockResolvedValue(undefined);
    renderWizard(componentSteps(3));
    await waitForPrimaryEnabled();

    await user.click(primaryButton());

    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent('wizard.errors.saveFailed')
    );
    expect(heading()).toHaveTextContent('test.step1');
    expect(isBlocked(primaryButton())).toBe(false);

    await user.click(primaryButton());

    await waitFor(() => expect(heading()).toHaveTextContent('test.step2'));
    expect(screen.getByRole('alert')).toHaveTextContent('');
  });

  it('Back waits for the save and is aria-disabled while saving', async () => {
    renderWizard(componentSteps(3));
    await waitForPrimaryEnabled();
    await user.click(primaryButton());
    await waitFor(() => expect(heading()).toHaveTextContent('test.step2'));
    await waitForPrimaryEnabled();
    const save = deferred();
    stepControl.leave = vi.fn(() => save.promise);

    await user.click(backButton());

    await waitFor(() => expect(isBlocked(backButton())).toBe(true));
    expect(heading()).toHaveTextContent('test.step2');

    save.resolve();

    await waitFor(() => expect(heading()).toHaveTextContent('test.step1'));
  });

  it('a failed step chunk shows an error with Retry and keeps Next disabled', async () => {
    renderWizard(componentSteps(3));
    loaders[1] = () => Promise.reject(new Error('chunk failed'));
    await waitForPrimaryEnabled();

    await user.click(primaryButton());

    const retry = await screen.findByRole('button', { name: /common\.retry/ });
    expect(screen.getByRole('alert')).toHaveTextContent('wizard.errors.stepLoadFailed');
    expect(isBlocked(primaryButton())).toBe(true);
    expect(describedText(primaryButton())).toBe('wizard.errors.stepLoadFailed');

    loaders[1] = loadStep;
    await user.click(retry);

    await waitForPrimaryEnabled();
    expect(screen.queryByRole('button', { name: /common\.retry/ })).not.toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('');
  });

  it('Skip closes at once while a save is pending and saves nothing', async () => {
    const save = deferred();
    stepControl.leave = vi.fn(() => save.promise);
    renderWizard(componentSteps(3));
    await waitForPrimaryEnabled();
    await user.click(primaryButton());
    await waitFor(() => expect(primaryButton()).toHaveTextContent('wizard.status.saving'));

    await user.click(screen.getByRole('button', { name: 'wizard.skip' }));

    expect(wizardState.isActive).toBe(false);
    expect(stepControl.leave).toHaveBeenCalledTimes(1);
    expect(api.post).toHaveBeenCalledTimes(1);

    save.resolve();
    await Promise.resolve();

    expect(wizardState.isActive).toBe(false);
    expect(wizardState.currentStepIndex).toBe(0);
    expect(api.post).toHaveBeenCalledTimes(1);
  });

  describe('leave confirmation', () => {
    const closeButton = () => screen.getByRole('button', { name: 'common.aria.closeModal' });
    const confirmation = () =>
      screen.queryByRole('alertdialog', { name: 'wizard.leaveConfirm.title' });

    it('X opens the leave confirmation with Keep setting up focused', async () => {
      renderWizard(componentSteps(3));
      await waitForPrimaryEnabled();

      await user.click(closeButton());

      expect(confirmation()).toBeInTheDocument();
      await waitFor(() =>
        expect(screen.getByRole('button', { name: 'wizard.leaveConfirm.stay' })).toHaveFocus()
      );
      expect(wizardState.isActive).toBe(true);
      expect(api.post).not.toHaveBeenCalled();
    });

    it('describes the confirmation with its message', async () => {
      renderWizard(componentSteps(3));
      await waitForPrimaryEnabled();

      await user.click(closeButton());

      expect(confirmation()).toHaveAccessibleDescription('wizard.leaveConfirm.message');
    });

    it('Escape opens the confirmation and Escape inside it returns to the wizard', async () => {
      renderWizard(componentSteps(3));
      await waitForPrimaryEnabled();

      await user.keyboard('{Escape}');
      expect(confirmation()).toBeInTheDocument();

      await user.keyboard('{Escape}');

      await waitFor(() => expect(confirmation()).not.toBeInTheDocument());
      expect(wizardState.isActive).toBe(true);
      expect(api.post).not.toHaveBeenCalled();
    });

    it('Keep setting up returns to the same step', async () => {
      renderWizard(componentSteps(3));
      await waitForPrimaryEnabled();
      await user.click(closeButton());

      await user.click(screen.getByRole('button', { name: 'wizard.leaveConfirm.stay' }));

      await waitFor(() => expect(confirmation()).not.toBeInTheDocument());
      expect(heading()).toHaveTextContent('test.step1');
      expect(wizardState.isActive).toBe(true);
    });

    it('Leave setup dismisses without calling the leave handler', async () => {
      renderWizard(componentSteps(3));
      await waitForPrimaryEnabled();
      await user.click(closeButton());

      await user.click(screen.getByRole('button', { name: 'wizard.leaveConfirm.leave' }));

      expect(wizardState.isActive).toBe(false);
      expect(stepControl.leave).not.toHaveBeenCalled();
      expect(api.post).toHaveBeenCalledTimes(1);
      await waitFor(() => expect(confirmation()).not.toBeInTheDocument());
    });

    it('X and Escape open the confirmation while a save is running', async () => {
      const save = deferred();
      stepControl.leave = vi.fn(() => save.promise);
      renderWizard(componentSteps(3));
      await waitForPrimaryEnabled();
      await user.click(primaryButton());
      await waitFor(() => expect(primaryButton()).toHaveTextContent('wizard.status.saving'));

      await user.click(closeButton());
      expect(confirmation()).toBeInTheDocument();
      await user.click(screen.getByRole('button', { name: 'wizard.leaveConfirm.stay' }));
      await waitFor(() => expect(confirmation()).not.toBeInTheDocument());

      await user.keyboard('{Escape}');
      expect(confirmation()).toBeInTheDocument();

      save.resolve();
    });

    it('X on the whats-new flow closes without confirmation', async () => {
      renderWizard([{ id: 'c1', type: 'content', title: 'Changes', content: 'text' }], 'whats-new');

      await user.click(closeButton());

      expect(confirmation()).not.toBeInTheDocument();
      expect(wizardState.isActive).toBe(false);
      expect(api.post).toHaveBeenCalledTimes(1);
    });
  });
});

// The wizard Modal has no accessible name yet: naming it belongs to the Modal-level
// accessibility work. The confirmation's name and description are asserted by role
// queries in the leave confirmation tests above.
const A11Y_OPTIONS = { rules: { 'aria-dialog-name': { enabled: false } } };

describe('WizardDialog Accessibility', () => {
  let user: ReturnType<typeof userEvent.setup>;

  beforeEach(() => {
    vi.clearAllMocks();
    wizardState._resetForTesting();
    stepControl.reset();
    user = userEvent.setup();
  });

  it('has no violations on a step', async () => {
    const { container } = renderWizard(componentSteps(3));
    await waitForPrimaryEnabled();

    expect(heading()).toHaveTextContent('test.step1');
    await expectNoA11yViolations(container, A11Y_OPTIONS);
  });

  it('has no violations in the load error state', async () => {
    const { container } = renderWizard(componentSteps(3));
    loaders[1] = () => Promise.reject(new Error('chunk failed'));
    await waitForPrimaryEnabled();
    await user.click(primaryButton());
    await screen.findByRole('button', { name: /common\.retry/ });

    expect(screen.getByRole('alert')).toHaveTextContent('wizard.errors.stepLoadFailed');
    await expectNoA11yViolations(container, A11Y_OPTIONS);
  });

  it('has no violations with the leave confirmation open', async () => {
    const { container } = renderWizard(componentSteps(3));
    await waitForPrimaryEnabled();
    await user.click(screen.getByRole('button', { name: 'common.aria.closeModal' }));
    const dialog = await screen.findByRole('alertdialog', { name: 'wizard.leaveConfirm.title' });

    expect(dialog).toHaveAccessibleDescription('wizard.leaveConfirm.message');
    await expectNoA11yViolations(container, A11Y_OPTIONS);
  });
});
