import { api } from '$lib/utils/api';
import type { WizardFlow, WizardLaunchOptions, WizardStatus, WizardStep } from './types';
import { getStepsForFlow } from './wizardRegistry';

export const WIZARD_DISMISSED_VERSION_KEY = 'birdnet-wizard-dismissed-version';

let status = $state<WizardStatus>('idle');
let flow = $state<WizardFlow | null>(null);
let currentStepIndex = $state(0);
let steps = $state<WizardStep[]>([]);
let isStepValid = $state(true);
let previousVersion = $state<string | null>(null);
let currentVersion = $state<string | null>(null);

const totalSteps = $derived(steps.length);
const currentStep = $derived(steps[currentStepIndex] ?? null);
const isFirstStep = $derived(currentStepIndex === 0);
const isLastStep = $derived(currentStepIndex === totalSteps - 1);
const isActive = $derived(status === 'active');

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
    await api.post('/api/v2/app/wizard/dismiss');
  } catch {
    // Swallow error — localStorage fallback prevents re-display
  }
}

function resetState(): void {
  status = 'completed';
  flow = null;
  currentStepIndex = 0;
  steps = [];
  isStepValid = true;
  previousVersion = null;
  currentVersion = null;
}

function launch(wizardFlow: WizardFlow, options?: WizardLaunchOptions): void {
  const resolvedSteps = getStepsForFlow(wizardFlow, options);
  if (resolvedSteps.length === 0) return;

  flow = wizardFlow;
  steps = resolvedSteps;
  currentStepIndex = 0;
  status = 'active';
  isStepValid = true;
  previousVersion = options?.previousVersion ?? null;
  currentVersion = options?.currentVersion ?? null;
}

function next(): void {
  if (isStepValid && !isLastStep) {
    currentStepIndex++;
    isStepValid = true;
  }
}

function back(): void {
  if (!isFirstStep) {
    currentStepIndex--;
  }
}

function setStepValid(valid: boolean): void {
  isStepValid = valid;
}

async function skip(): Promise<void> {
  await dismiss();
  resetState();
}

async function complete(): Promise<void> {
  await dismiss();
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
  get previousVersion() {
    return previousVersion;
  },
  launch,
  next,
  back,
  setStepValid,
  skip,
  complete,
};
