import { describe, it, expect, vi, beforeEach } from 'vitest';
import type { Component } from 'svelte';
import { renderTyped, screen, waitFor, fireEvent } from '../../../../test/render-helpers';
import userEvent from '@testing-library/user-event';
import { expectNoA11yViolations } from '$lib/utils/axe-utils';
import type { TranslationKey } from '$lib/i18n';
import type { WizardStep, WizardStepProps } from './types';
import { deferred } from '../../../../test/async-helpers';
import { toastActions } from '$lib/stores/toast';

vi.mock('$lib/utils/api', async importOriginal => ({
  ...(await importOriginal<typeof import('$lib/utils/api')>()),
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

const { api, ApiError } = await import('$lib/utils/api');
const { getStepsForFlow } = await import('./wizardRegistry');
const { wizardState, STEP_MOVE_GUARD_MS } = await import('./wizardState.svelte');
const { stepControl } = await import('./wizardTestStepControl');
const { default: WizardDialog } = await import('./WizardDialog.svelte');
const { default: WizardTestStep } = await import('./WizardTestStep.test.svelte');
const { default: WizardDropdownStep } = await import('./WizardDropdownStep.test.svelte');

type StepModule = { default: Component<WizardStepProps> };

const loadStep = (): Promise<StepModule> => Promise.resolve({ default: WizardTestStep });

const loadDropdownStep = (): Promise<StepModule> =>
  Promise.resolve({ default: WizardDropdownStep });

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

// After a step move, Next, Back and Done ignore clicks for a short interval so
// the second click of a double click cannot move again; a deliberate click
// comes later, which this stands in for.
function waitOutStepMoveGuard(): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, STEP_MOVE_GUARD_MS));
}

// The box that holds the step content; the fixture step renders a checkbox in it
const contentBox = () => screen.getByRole('checkbox').closest<HTMLElement>('[tabindex="-1"]');
// Svelte sets inert as a property, which jsdom does not reflect to the attribute
const isContentInert = () => contentBox()?.inert === true;

function describedText(el: HTMLElement): string {
  const id = el.getAttribute('aria-describedby');
  if (!id) return '';
  return document.getElementById(id)?.textContent ?? '';
}

// Elements showing `text` on screen, leaving out screen reader only regions
const visibleWithText = (text: string) =>
  screen.queryAllByText(text).filter(el => !el.closest('.sr-only'));

// The footer reason: the paragraph in the row that holds the Next button
const footerReason = () => primaryButton().closest('div.w-full')?.querySelector('p') ?? null;

const closeButton = () => screen.getByRole('button', { name: 'common.aria.closeModal' });

// Starts a save that stays pending and returns once the wizard shows it saving
async function startPendingSave(user: ReturnType<typeof userEvent.setup>) {
  const save = deferred();
  stepControl.leave = vi.fn(() => save.promise);
  const { container } = renderWizard(componentSteps(3));
  await waitForPrimaryEnabled();
  await user.click(primaryButton());
  await waitFor(() => expect(primaryButton()).toHaveTextContent('wizard.status.saving'));
  return { save, container };
}

// Starts a save that stays pending, then opens the confirmation over it
async function openConfirmationDuringSave(user: ReturnType<typeof userEvent.setup>) {
  const started = await startPendingSave(user);
  await user.click(closeButton());
  return started;
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

  it('shows the reason the step reports instead of the generic one', async () => {
    stepControl.validQueue = [false];
    stepControl.reasonQueue = ['wizard.reasons.stepSpecific' as TranslationKey];
    renderWizard(componentSteps(3));

    await waitFor(() => expect(describedText(primaryButton())).toBe('wizard.reasons.stepSpecific'));
  });

  it('takes no space for the alert or the reason while there is nothing to say', async () => {
    renderWizard(componentSteps(3));
    await waitForPrimaryEnabled();

    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent('');
    expect(alert).toHaveClass('sr-only');
    expect(footerReason()).toHaveTextContent('');
    expect(primaryButton()).not.toHaveAttribute('aria-describedby');
  });

  it('shows the reason in the same row as the buttons', async () => {
    stepControl.validQueue = [false];
    renderWizard(componentSteps(3));

    await waitFor(() => expect(describedText(primaryButton())).toBe('wizard.reasons.completeStep'));

    const reasonId = primaryButton().getAttribute('aria-describedby') ?? '';
    expect(footerReason()?.id).toBe(reasonId);
    expect(footerReason()).toHaveTextContent('wizard.reasons.completeStep');
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
    const { save } = await startPendingSave(user);
    expect(isBlocked(primaryButton())).toBe(true);
    // Saving shows once, on the button; the footer reason does not repeat it
    expect(visibleWithText('wizard.status.saving')).toEqual([primaryButton()]);
    expect(footerReason()).toHaveTextContent('');
    // Screen readers still hear it from the saving status region
    expect(
      screen.getAllByRole('status').some(el => el.textContent === 'wizard.status.saving')
    ).toBe(true);
    expect(heading()).toHaveTextContent('test.step1');

    save.resolve(undefined);

    await waitFor(() => expect(heading()).toHaveTextContent('test.step2'));
  });

  it('double click during a save advances exactly one step', async () => {
    const save = deferred();
    stepControl.leave = vi.fn(() => save.promise);
    renderWizard(componentSteps(3));
    await waitForPrimaryEnabled();

    await user.dblClick(primaryButton());
    expect(stepControl.leave).toHaveBeenCalledTimes(1);

    save.resolve(undefined);

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

    load.resolve({ default: WizardTestStep });
    await waitForPrimaryEnabled();
    await waitOutStepMoveGuard();
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
    load.resolve({ default: WizardTestStep });

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

  it('a save the server refuses as invalid shows the rejected message in the alert', async () => {
    stepControl.leave = vi
      .fn<() => Promise<void>>()
      .mockRejectedValueOnce(new ApiError('refused', 400, new Response(null, { status: 400 })));
    renderWizard(componentSteps(3));
    await waitForPrimaryEnabled();

    await user.click(primaryButton());

    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent('wizard.errors.saveRejected')
    );
    expect(heading()).toHaveTextContent('test.step1');
  });

  it('makes the step content inert while saving and interactive again after the save', async () => {
    const save = deferred();
    stepControl.leave = vi.fn(() => save.promise);
    renderWizard(componentSteps(3));
    await waitForPrimaryEnabled();
    expect(isContentInert()).toBe(false);

    await user.click(primaryButton());

    await waitFor(() => expect(isContentInert()).toBe(true));
    expect(contentBox()).toHaveAttribute('aria-busy', 'true');

    save.resolve(undefined);

    await waitFor(() => expect(heading()).toHaveTextContent('test.step2'));
    await waitForPrimaryEnabled();
    expect(isContentInert()).toBe(false);
    expect(contentBox()).not.toHaveAttribute('aria-busy');
  });

  it('makes the step content interactive again after a failed save', async () => {
    const save = deferred();
    stepControl.leave = vi.fn(() => save.promise);
    renderWizard(componentSteps(3));
    await waitForPrimaryEnabled();
    await user.click(primaryButton());
    await waitFor(() => expect(isContentInert()).toBe(true));

    save.reject(new Error('save failed'));

    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent('wizard.errors.saveFailed')
    );
    expect(heading()).toHaveTextContent('test.step1');
    expect(isContentInert()).toBe(false);
    expect(contentBox()).not.toHaveAttribute('aria-busy');
  });

  it('Back waits for the save and is aria-disabled while saving', async () => {
    renderWizard(componentSteps(3));
    await waitForPrimaryEnabled();
    await user.click(primaryButton());
    await waitFor(() => expect(heading()).toHaveTextContent('test.step2'));
    await waitForPrimaryEnabled();
    await waitOutStepMoveGuard();
    const save = deferred();
    stepControl.leave = vi.fn(() => save.promise);

    await user.click(backButton());

    await waitFor(() => expect(isBlocked(backButton())).toBe(true));
    expect(describedText(backButton())).toBe('wizard.status.saving');
    expect(heading()).toHaveTextContent('test.step2');

    save.resolve(undefined);

    await waitFor(() => expect(heading()).toHaveTextContent('test.step1'));
  });

  it('a failed step chunk shows an error with Retry and keeps Next disabled', async () => {
    renderWizard(componentSteps(3));
    loaders[1] = () => Promise.reject(new Error('chunk failed'));
    await waitForPrimaryEnabled();

    await user.click(primaryButton());

    const retry = await screen.findByRole('button', { name: /common\.retry/ });
    expect(screen.getByRole('alert')).toHaveTextContent('wizard.errors.stepLoadFailed');
    expect(screen.getByRole('alert')).not.toHaveClass('sr-only');
    expect(isBlocked(primaryButton())).toBe(true);
    expect(describedText(primaryButton())).toBe('wizard.errors.stepLoadFailed');
    expect(primaryButton().getAttribute('aria-describedby')).toBe(screen.getByRole('alert').id);
    expect(screen.getAllByText('wizard.errors.stepLoadFailed')).toHaveLength(1);

    loaders[1] = loadStep;
    await user.click(retry);

    await waitForPrimaryEnabled();
    expect(screen.queryByRole('button', { name: /common\.retry/ })).not.toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('');
  });

  it('offers Reload page instead of Retry when the retried chunk fails again', async () => {
    renderWizard(componentSteps(3));
    loaders[1] = () => Promise.reject(new Error('chunk failed'));
    await waitForPrimaryEnabled();
    await user.click(primaryButton());
    const retry = await screen.findByRole('button', { name: /common\.retry/ });

    await user.click(retry);

    const reload = await screen.findByRole('button', { name: 'wizard.actions.reloadPage' });
    expect(screen.queryByRole('button', { name: /common\.retry/ })).not.toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('wizard.errors.stepLoadFailedReload');
    expect(isBlocked(primaryButton())).toBe(true);
    expect(describedText(primaryButton())).toBe('wizard.errors.stepLoadFailedReload');
    await waitFor(() => expect(reload).toHaveFocus());

    await user.click(reload);

    expect(window.location.reload).toHaveBeenCalledTimes(1);
  });

  it('offers Retry again on the next session after Reload page was shown', async () => {
    renderWizard(componentSteps(3));
    loaders[1] = () => Promise.reject(new Error('chunk failed'));
    await waitForPrimaryEnabled();
    await user.click(primaryButton());
    await user.click(await screen.findByRole('button', { name: /common\.retry/ }));
    await screen.findByRole('button', { name: 'wizard.actions.reloadPage' });

    wizardState.skip();
    wizardState.launch('onboarding', { currentVersion: 'v1' });
    await waitForPrimaryEnabled();
    await user.click(primaryButton());

    await screen.findByRole('button', { name: /common\.retry/ });
    expect(
      screen.queryByRole('button', { name: 'wizard.actions.reloadPage' })
    ).not.toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('wizard.errors.stepLoadFailed');
  });

  it('Retry keeps keyboard focus inside the dialog', async () => {
    renderWizard(componentSteps(3));
    loaders[1] = () => Promise.reject(new Error('chunk failed'));
    await waitForPrimaryEnabled();
    await user.click(primaryButton());
    const retry = await screen.findByRole('button', { name: /common\.retry/ });
    const load = deferred<StepModule>();
    loaders[1] = () => load.promise;

    await user.click(retry);

    expect(retry).not.toBeInTheDocument();
    expect(document.activeElement?.closest('[role="dialog"]')).not.toBeNull();
    load.resolve({ default: WizardTestStep });
    await waitForPrimaryEnabled();
  });

  it('Back still works after a step chunk failed to load', async () => {
    renderWizard(componentSteps(3));
    loaders[1] = () => Promise.reject(new Error('chunk failed'));
    await waitForPrimaryEnabled();
    await user.click(primaryButton());
    await screen.findByRole('button', { name: /common\.retry/ });
    await waitOutStepMoveGuard();

    await user.click(backButton());

    await waitFor(() => expect(heading()).toHaveTextContent('test.step1'));
  });

  it('a double click on Back while the previous step loads moves one step', async () => {
    renderWizard(componentSteps(3));
    await waitForPrimaryEnabled();
    await user.click(primaryButton());
    await waitFor(() => expect(heading()).toHaveTextContent('test.step2'));
    await waitForPrimaryEnabled();
    await waitOutStepMoveGuard();
    await user.click(primaryButton());
    await waitFor(() => expect(heading()).toHaveTextContent('test.step3'));
    const load = deferred<StepModule>();
    loaders[1] = () => load.promise; // step 2 stays loading after the first Back
    await waitForPrimaryEnabled();
    await waitOutStepMoveGuard();

    await user.dblClick(backButton());

    await waitFor(() => expect(heading()).toHaveTextContent('test.step2'));
    load.resolve({ default: WizardTestStep });
  });

  it('a click right after the next step is ready does not move again', async () => {
    renderWizard(componentSteps(3));
    await waitForPrimaryEnabled();

    await user.click(primaryButton());
    await waitFor(() => expect(heading()).toHaveTextContent('test.step2'));
    await waitForPrimaryEnabled();
    await user.click(primaryButton());

    expect(heading()).toHaveTextContent('test.step2');
    expect(stepControl.leave).toHaveBeenCalledTimes(1);

    await waitOutStepMoveGuard();
    await user.click(primaryButton());

    await waitFor(() => expect(heading()).toHaveTextContent('test.step3'));
  });

  it('Skip closes at once without calling the leave handler again and ignores the pending save', async () => {
    const { save } = await startPendingSave(user);

    await user.click(screen.getByRole('button', { name: 'wizard.skip' }));

    expect(wizardState.isActive).toBe(false);
    expect(stepControl.leave).toHaveBeenCalledTimes(1);
    expect(api.post).toHaveBeenCalledTimes(1);

    save.resolve(undefined);
    await Promise.resolve();

    expect(wizardState.isActive).toBe(false);
    expect(wizardState.currentStepIndex).toBe(0);
    expect(api.post).toHaveBeenCalledTimes(1);
    expect(toastActions.error).not.toHaveBeenCalled();
  });

  it('Skip during a save that later fails shows an error toast', async () => {
    const { save } = await startPendingSave(user);
    await user.click(screen.getByRole('button', { name: 'wizard.skip' }));

    save.reject(new Error('late'));

    await waitFor(() => expect(toastActions.error).toHaveBeenCalledTimes(1));
    expect(toastActions.error).toHaveBeenCalledWith('wizard.errors.saveUnfinished', {
      duration: null,
    });
  });

  describe('leave confirmation', () => {
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

    it('describes the confirmation with the normal message when no save is running', async () => {
      renderWizard(componentSteps(3));
      await waitForPrimaryEnabled();

      await user.click(closeButton());

      expect(confirmation()).toHaveAccessibleDescription('wizard.leaveConfirm.message');
    });

    it('describes the confirmation with the in-flight message while a save is running', async () => {
      const { save } = await openConfirmationDuringSave(user);

      expect(confirmation()).toHaveAccessibleDescription('wizard.leaveConfirm.messageSaving');

      save.resolve(undefined);
    });

    it('switches the confirmation to the normal message when the save finishes while it is open', async () => {
      const { save } = await openConfirmationDuringSave(user);
      expect(confirmation()).toHaveAccessibleDescription('wizard.leaveConfirm.messageSaving');

      save.resolve(undefined);

      await waitFor(() =>
        expect(confirmation()).toHaveAccessibleDescription('wizard.leaveConfirm.message')
      );
    });

    it('switches the confirmation to the normal message when the save fails while it is open', async () => {
      const { save } = await openConfirmationDuringSave(user);
      expect(confirmation()).toHaveAccessibleDescription('wizard.leaveConfirm.messageSaving');

      save.reject(new Error('save failed'));

      await waitFor(() =>
        expect(confirmation()).toHaveAccessibleDescription('wizard.leaveConfirm.message')
      );
      expect(toastActions.error).not.toHaveBeenCalled();
    });

    it('Leave setup during a save that later fails shows an error toast', async () => {
      const { save } = await openConfirmationDuringSave(user);
      await user.click(screen.getByRole('button', { name: 'wizard.leaveConfirm.leave' }));
      await waitFor(() => expect(wizardState.isActive).toBe(false));

      save.reject(new Error('late'));

      await waitFor(() => expect(toastActions.error).toHaveBeenCalledTimes(1));
      expect(toastActions.error).toHaveBeenCalledWith('wizard.errors.saveUnfinished', {
        duration: null,
      });
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

      await waitFor(() => expect(wizardState.isActive).toBe(false));
      expect(stepControl.leave).not.toHaveBeenCalled();
      expect(api.post).toHaveBeenCalledTimes(1);
      await waitFor(() => expect(confirmation()).not.toBeInTheDocument());
    });

    it('Leave setup returns focus to the element focused before the wizard opened', async () => {
      const opener = document.createElement('button');
      opener.textContent = 'opener';
      document.body.appendChild(opener);
      opener.focus();
      try {
        renderWizard(componentSteps(3));
        await waitForPrimaryEnabled();
        await user.click(closeButton());

        await user.click(screen.getByRole('button', { name: 'wizard.leaveConfirm.leave' }));

        await waitFor(() => expect(wizardState.isActive).toBe(false));
        await waitFor(() => expect(opener).toHaveFocus());
      } finally {
        opener.remove();
      }
    });

    it('X and Escape open the confirmation while a save is running', async () => {
      const { save } = await startPendingSave(user);

      await user.click(closeButton());
      expect(confirmation()).toBeInTheDocument();
      await user.click(screen.getByRole('button', { name: 'wizard.leaveConfirm.stay' }));
      await waitFor(() => expect(confirmation()).not.toBeInTheDocument());

      await user.keyboard('{Escape}');
      expect(confirmation()).toBeInTheDocument();

      save.resolve(undefined);
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
    await expectNoA11yViolations(container);
  });

  it('has no violations in the load error state', async () => {
    const { container } = renderWizard(componentSteps(3));
    loaders[1] = () => Promise.reject(new Error('chunk failed'));
    await waitForPrimaryEnabled();
    await user.click(primaryButton());
    await screen.findByRole('button', { name: /common\.retry/ });

    expect(screen.getByRole('alert')).toHaveTextContent('wizard.errors.stepLoadFailed');
    await expectNoA11yViolations(container);
  });

  it('has no violations with the leave confirmation open', async () => {
    const { container } = renderWizard(componentSteps(3));
    await waitForPrimaryEnabled();
    await user.click(screen.getByRole('button', { name: 'common.aria.closeModal' }));
    const dialog = await screen.findByRole('alertdialog', { name: 'wizard.leaveConfirm.title' });

    expect(dialog).toHaveAccessibleDescription('wizard.leaveConfirm.message');
    await expectNoA11yViolations(container);
  });

  it('has no violations with the leave confirmation open during a save', async () => {
    const { save, container } = await openConfirmationDuringSave(user);
    const dialog = await screen.findByRole('alertdialog', { name: 'wizard.leaveConfirm.title' });

    expect(dialog).toHaveAccessibleDescription('wizard.leaveConfirm.messageSaving');
    await expectNoA11yViolations(container);

    save.resolve(undefined);
  });

  it('names the dialog with the step title and renames it on the next step', async () => {
    renderWizard(componentSteps(3));
    await waitForPrimaryEnabled();

    expect(screen.getByRole('dialog', { name: 'test.step1' })).toBeInTheDocument();

    await user.click(primaryButton());

    expect(await screen.findByRole('dialog', { name: 'test.step2' })).toBeInTheDocument();
  });

  it('does not describe the dialog with the whole step body', async () => {
    renderWizard(componentSteps(3));
    await waitForPrimaryEnabled();

    expect(screen.getByRole('dialog', { name: 'test.step1' })).not.toHaveAttribute(
      'aria-describedby'
    );
  });

  it('keeps focus in the dialog when Back returns to the first step', async () => {
    renderWizard(componentSteps(3));
    await waitForPrimaryEnabled();
    await user.click(primaryButton());
    await waitFor(() => expect(heading()).toHaveTextContent('test.step2'));
    await waitForPrimaryEnabled();
    await waitOutStepMoveGuard();

    backButton().focus();
    await user.keyboard('{Enter}');

    await waitFor(() => expect(heading()).toHaveTextContent('test.step1'));
    await waitFor(() => expect(document.activeElement).toBe(primaryButton()));
  });

  it('moves focus to Next when Back is clicked while focus was on body', async () => {
    renderWizard(componentSteps(3));
    await waitForPrimaryEnabled();
    await user.click(primaryButton());
    await screen.findByRole('dialog', { name: 'test.step2' });
    await waitForPrimaryEnabled();
    await waitOutStepMoveGuard();
    // A click on a button that takes no focus (Safari) leaves focus on body
    primaryButton().blur();
    expect(document.activeElement).toBe(document.body);

    await fireEvent.click(backButton());

    await screen.findByRole('dialog', { name: 'test.step1' });
    await waitFor(() => expect(document.activeElement).toBe(primaryButton()));
  });

  it('moves focus to Back when Back is clicked on step 3 while focus was on body', async () => {
    renderWizard(componentSteps(3));
    await waitForPrimaryEnabled();
    await user.click(primaryButton());
    await screen.findByRole('dialog', { name: 'test.step2' });
    await waitForPrimaryEnabled();
    await waitOutStepMoveGuard();
    await user.click(primaryButton());
    await screen.findByRole('dialog', { name: 'test.step3' });
    await waitForPrimaryEnabled();
    await waitOutStepMoveGuard();
    primaryButton().blur();
    expect(document.activeElement).toBe(document.body);

    await fireEvent.click(backButton());

    await screen.findByRole('dialog', { name: 'test.step2' });
    await waitFor(() => expect(document.activeElement).toBe(backButton()));
  });

  it('leaves focus alone when Back finishes after the user moved it', async () => {
    renderWizard(componentSteps(3));
    await waitForPrimaryEnabled();
    await user.click(primaryButton());
    await screen.findByRole('dialog', { name: 'test.step2' });
    await waitForPrimaryEnabled();
    await waitOutStepMoveGuard();

    const save = deferred<undefined>();
    stepControl.leave = vi.fn(() => save.promise);
    const close = screen.getByRole('button', { name: 'common.aria.closeModal' });
    backButton().focus();
    await user.keyboard('{Enter}');
    close.focus();
    save.resolve(undefined);

    await screen.findByRole('dialog', { name: 'test.step1' });
    // Let the focus handling that follows the step move run
    await new Promise(resolve => setTimeout(resolve, 0));
    expect(document.activeElement).toBe(close);
  });

  it('Escape in an open step dropdown closes only the dropdown', async () => {
    loaders = [loadDropdownStep, loadStep];
    vi.mocked(getStepsForFlow).mockReturnValue(
      loaders.map((_, i) => ({
        id: `step-${i + 1}`,
        type: 'component' as const,
        titleKey: `test.step${i + 1}` as TranslationKey,
        // eslint-disable-next-line security/detect-object-injection -- i is a bounded test index
        component: () => loaders[i](),
      }))
    );
    wizardState.launch('onboarding', { currentVersion: 'v1' });
    renderTyped(WizardDialog);
    const trigger = await waitFor(() => {
      const el = document.getElementById('fixture-dropdown');
      if (!el) throw new Error('fixture dropdown not rendered');
      return el;
    });

    await user.click(trigger);
    const search = await screen.findByRole('searchbox');
    await waitFor(() => expect(search).toHaveFocus());
    await user.keyboard('{Escape}');

    await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    expect(wizardState.isActive).toBe(true);
    expect(document.activeElement).toBe(trigger);

    await user.keyboard('{Escape}');

    expect(await screen.findByRole('alertdialog')).toBeInTheDocument();
  });

  it('colours the footer alert with the error text token', async () => {
    stepControl.leave = vi.fn(() => Promise.reject(new Error('save failed')));
    renderWizard(componentSteps(3));
    await waitForPrimaryEnabled();

    await user.click(primaryButton());

    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent('wizard.errors.saveFailed')
    );
    expect(screen.getByRole('alert')).toHaveClass('text-[var(--text-error)]');
  });
});
