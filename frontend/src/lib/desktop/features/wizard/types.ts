import type { Component } from 'svelte';

export type WizardFlow = 'onboarding' | 'whats-new';
export type WizardStatus = 'idle' | 'active' | 'completed';

/** Load status of the current step, set by the dialog. */
export type StepStatus = 'loading' | 'ready' | 'failed';

export interface ComponentStep {
  id: string;
  type: 'component';
  titleKey: string; // i18n key
  component: () => Promise<{ default: Component<WizardStepProps> }>;
}

export interface ContentStep {
  id: string;
  type: 'content';
  titleKey?: string; // i18n key (optional for changelogs)
  title?: string; // plain string fallback
  content: string; // HTML/markdown content
}

export type WizardStep = ComponentStep | ContentStep;

/**
 * Saves a step's pending edits. Rejects when the save fails; the wizard then
 * stays on the step and shows an error.
 */
export type StepLeaveHandler = () => Promise<void>;

export interface WizardStepProps {
  /**
   * Reports whether the step is valid. A step must call it at mount and on every
   * validity change; until it does, Next stays disabled.
   */
  onValidChange?: (valid: boolean) => void;
  /**
   * Registers the step's leave handler, which Next, Back and Done await before
   * navigating. Register once at mount; the returned function unregisters the
   * handler and must run when the step is destroyed.
   */
  registerLeaveHandler?: (handler: StepLeaveHandler) => () => void;
}

export interface WizardLaunchOptions {
  previousVersion?: string;
  currentVersion?: string;
}
