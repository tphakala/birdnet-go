import { api } from '$lib/utils/api';
import { loggers } from '$lib/utils/logger';
import type {
  StepLeaveHandler,
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
let isStepReady = $state<boolean>(false);
let isSaving = $state<boolean>(false);
// i18n key of the error to show for the current step, or null
let stepError = $state<string | null>(null);
let previousVersion = $state<string | null>(null);
let currentVersion = $state<string | null>(null);

const totalSteps = $derived(steps.length);
// eslint-disable-next-line security/detect-object-injection -- currentStepIndex is a numeric index bounded by steps.length
const currentStep = $derived(steps[currentStepIndex] ?? null);
const isFirstStep = $derived(currentStepIndex === 0);
const isLastStep = $derived(currentStepIndex === totalSteps - 1);
const isActive = $derived(status === 'active');
const canAdvance = $derived(isActive && !isSaving && isStepReady && isStepValid);
// Back needs a mounted step too, so a double click cannot skip over a step that
// has not loaded yet; a step whose chunk failed is marked ready by the dialog.
const canGoBack = $derived(isActive && !isSaving && !isFirstStep && isStepReady);

// The leave handler of the mounted step, awaited by next(), back() and complete().
let leaveHandler: StepLeaveHandler | null = null;
// Incremented whenever the wizard closes or relaunches, so an async result that
// belongs to an earlier session can be recognised and dropped.
let session = 0;

const SAVE_FAILED_KEY = 'wizard.errors.saveFailed';

function resetStepFlags(): void {
  isStepValid = false;
  isStepReady = false;
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
  status = 'idle';
  flow = null;
  currentStepIndex = 0;
  steps = [];
  previousVersion = null;
  currentVersion = null;
  session++;
  resetStepFlags();
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

// Called by the dialog once the step's component is mounted and has had the
// chance to report its validity.
function markStepReady(index: number): void {
  if (isActive && index === currentStepIndex) {
    isStepReady = true;
  }
}

function setStepValid(valid: boolean, index: number = currentStepIndex): void {
  if (isActive && index === currentStepIndex) {
    isStepValid = valid;
  }
}

// Runs the current step's leave handler. isSaving is set before the first await
// so a second click in the same tick is refused. Returns true when navigation may
// proceed; isSaving then stays true and the caller clears it in the same
// synchronous block that moves the step. A result that belongs to another session
// or step never touches state.
async function runLeave(): Promise<boolean> {
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
    return false;
  }
  if (isStale()) return false;
  leaveHandler = null;
  return true;
}

async function next(): Promise<void> {
  if (!canAdvance || isLastStep) return;
  const startSession = session;
  const startIndex = currentStepIndex;
  if (!(await runLeave())) return;
  if (startSession !== session || startIndex !== currentStepIndex) return;
  currentStepIndex++;
  resetStepFlags();
}

async function back(): Promise<void> {
  if (!canGoBack) return;
  const startSession = session;
  const startIndex = currentStepIndex;
  // An invalid step has nothing safe to save; its edits are discarded.
  if (isStepReady && isStepValid) {
    if (!(await runLeave())) return;
    if (startSession !== session || startIndex !== currentStepIndex) return;
  }
  currentStepIndex--;
  resetStepFlags();
}

function skip(): void {
  leaveHandler = null;
  void dismiss();
  resetState();
}

async function complete(): Promise<void> {
  if (!canAdvance || !isLastStep) return;
  if (!(await runLeave())) return;
  void dismiss();
  resetState();
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
  get isStepReady() {
    return isStepReady;
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
  markStepReady,
  registerLeaveHandler,
  skip,
  complete,
  dismissOnly,
  _resetForTesting,
};
