import { api } from '$lib/utils/api';
import { loggers } from '$lib/utils/logger';
import type { TranslationKey } from '$lib/i18n';
import type {
  StepLeaveHandler,
  StepStatus,
  WizardFlow,
  WizardLaunchOptions,
  WizardStatus,
  WizardStep,
} from './types';
import { getStepsForFlow } from './wizardRegistry';

export const WIZARD_DISMISSED_VERSION_KEY = 'birdnet-wizard-dismissed-version';
const WIZARD_DISMISS_ENDPOINT = '/api/v2/app/wizard/dismiss';

let status = $state<WizardStatus>('idle');
let flow = $state<WizardFlow | null>(null);
let currentStepIndex = $state(0);
let steps = $state<WizardStep[]>([]);
// Fail closed: a step is not valid until it reports so, and not ready until the
// dialog has mounted it.
let isStepValid = $state<boolean>(false);
let stepStatus = $state<StepStatus>('loading');
let isSaving = $state<boolean>(false);
// i18n key of the error to show for the current step, or null
let stepError = $state<TranslationKey | null>(null);
let previousVersion = $state<string | null>(null);
let currentVersion = $state<string | null>(null);

const totalSteps = $derived(steps.length);
// eslint-disable-next-line security/detect-object-injection -- currentStepIndex is a numeric index bounded by steps.length
const currentStep = $derived(steps[currentStepIndex] ?? null);
const isFirstStep = $derived(currentStepIndex === 0);
const isLastStep = $derived(currentStepIndex === totalSteps - 1);
const isActive = $derived(status === 'active');
const canAdvance = $derived(isActive && !isSaving && stepStatus === 'ready' && isStepValid);
// Back needs a settled step too, so a double click cannot skip over a step that
// has not loaded yet; a step whose chunk failed to load can still go back.
const canGoBack = $derived(isActive && !isSaving && !isFirstStep && stepStatus !== 'loading');

// The leave handler of the mounted step, awaited by next(), back() and complete().
let leaveHandler: StepLeaveHandler | null = null;
// Incremented whenever the wizard closes or relaunches, so an async result that
// belongs to an earlier session can be recognised and dropped.
let session = 0;

const SAVE_FAILED_KEY: TranslationKey = 'wizard.errors.saveFailed';

function resetStepFlags(): void {
  isStepValid = false;
  stepStatus = 'loading';
  isSaving = false;
  stepError = null;
  leaveHandler = null;
}

async function dismiss(): Promise<void> {
  // Optimistic localStorage update
  const version = currentVersion ?? '';
  if (version) {
    try {
      localStorage.setItem(WIZARD_DISMISSED_VERSION_KEY, version);
    } catch {
      // localStorage unavailable (private browsing, etc.)
    }
  }

  try {
    await api.post(WIZARD_DISMISS_ENDPOINT);
  } catch {
    setTimeout(() => {
      api.post(WIZARD_DISMISS_ENDPOINT).catch(() => {});
    }, 2000);
  }
}

function resetState(): void {
  status = 'completed';
  flow = null;
  currentStepIndex = 0;
  steps = [];
  previousVersion = null;
  currentVersion = null;
  session++;
  resetStepFlags();
}

// Dismiss without changing wizard state. Used when launch() didn't activate
// (e.g., no changelog steps matched) but we still need to update last_seen_version.
async function dismissOnly(version?: string): Promise<void> {
  const ver = version ?? currentVersion ?? '';
  if (ver) {
    try {
      localStorage.setItem(WIZARD_DISMISSED_VERSION_KEY, ver);
    } catch {
      // localStorage unavailable
    }
  }
  try {
    await api.post(WIZARD_DISMISS_ENDPOINT);
  } catch {
    setTimeout(() => {
      api.post(WIZARD_DISMISS_ENDPOINT).catch(() => {});
    }, 2000);
  }
}

function _resetForTesting(): void {
  resetState();
  status = 'idle';
}

function launch(wizardFlow: WizardFlow, options?: WizardLaunchOptions): void {
  const resolvedSteps = getStepsForFlow(wizardFlow, options);
  if (resolvedSteps.length === 0) return;

  flow = wizardFlow;
  steps = resolvedSteps;
  currentStepIndex = 0;
  status = 'active';
  previousVersion = options?.previousVersion ?? null;
  currentVersion = options?.currentVersion ?? null;
  session++;
  resetStepFlags();
}

function registerLeaveHandler(handler: StepLeaveHandler): () => void {
  leaveHandler = handler;
  return () => {
    if (leaveHandler === handler) leaveHandler = null;
  };
}

// Called by the dialog: 'loading' while the step's component loads, 'ready' once
// it is mounted and has had the chance to report its validity, 'failed' when it
// could not be loaded.
function setStepStatus(next: StepStatus, index: number): void {
  if (isActive && index === currentStepIndex) {
    stepStatus = next;
  }
}

function setStepValid(valid: boolean, index: number = currentStepIndex): void {
  if (isActive && index === currentStepIndex) {
    isStepValid = valid;
  }
}

// Runs the current step's leave handler, then move() if the wizard is still on
// the same session and step. isSaving is set before the first await so a second
// click in the same tick is refused; it stays true until move() runs, and move()
// clears it in the same synchronous block that moves the step. A result that
// belongs to another session or step never touches state.
async function runLeave(move: () => void): Promise<void> {
  const startSession = session;
  const startIndex = currentStepIndex;
  isSaving = true;
  stepError = null;
  const isStale = () => startSession !== session || startIndex !== currentStepIndex;
  try {
    await leaveHandler?.();
  } catch (err) {
    loggers.ui.error('Wizard step save failed', err);
    if (!isStale()) {
      stepError = SAVE_FAILED_KEY;
      isSaving = false;
    }
    return;
  }
  if (isStale()) return;
  move();
}

function moveBy(delta: number): void {
  currentStepIndex += delta;
  resetStepFlags();
}

async function next(): Promise<void> {
  if (!canAdvance || isLastStep) return;
  await runLeave(() => moveBy(1));
}

async function back(): Promise<void> {
  if (!canGoBack) return;
  // An invalid step has nothing safe to save; its edits are discarded.
  if (isStepValid) {
    await runLeave(() => moveBy(-1));
  } else {
    moveBy(-1);
  }
}

function skip(): void {
  void dismiss();
  resetState();
}

async function complete(): Promise<void> {
  if (!canAdvance || !isLastStep) return;
  await runLeave(() => {
    void dismiss();
    resetState();
  });
}

export const wizardState = {
  get status() {
    return status;
  },
  get flow() {
    return flow;
  },
  get currentStep() {
    return currentStep;
  },
  get currentStepIndex() {
    return currentStepIndex;
  },
  get totalSteps() {
    return totalSteps;
  },
  get isFirstStep() {
    return isFirstStep;
  },
  get isLastStep() {
    return isLastStep;
  },
  get isActive() {
    return isActive;
  },
  get isStepValid() {
    return isStepValid;
  },
  get stepStatus() {
    return stepStatus;
  },
  get isSaving() {
    return isSaving;
  },
  get stepError() {
    return stepError;
  },
  get canAdvance() {
    return canAdvance;
  },
  get canGoBack() {
    return canGoBack;
  },
  get previousVersion() {
    return previousVersion;
  },
  launch,
  next,
  back,
  setStepValid,
  setStepStatus,
  registerLeaveHandler,
  skip,
  complete,
  dismissOnly,
  _resetForTesting,
};
