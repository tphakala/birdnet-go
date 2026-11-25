import type { Component } from 'svelte';
import type { TranslationKey } from '$lib/i18n';
import type { CatalogEntry } from '$lib/types/models';

/**
 * The wizard flows. Onboarding and what's new belong to an app version and are
 * dismissed for it; the model next steps flow follows a model install and is
 * offered again after the next one.
 */
export type WizardFlow = 'onboarding' | 'whats-new' | 'model-next-steps';
export type WizardStatus = 'idle' | 'active' | 'completed';

/** Load status of the current step, set by the dialog. */
export type StepStatus = 'loading' | 'ready' | 'failed';

/** Dialog width of a step: `wide` for steps with a map or a long list. */
export type WizardStepSize = 'default' | 'wide';

export interface ComponentStep {
  id: string;
  type: 'component';
  titleKey: string; // i18n key
  component: () => Promise<{ default: Component<WizardStepProps> }>;
  /**
   * i18n key of the error toast shown when this step's save fails after the wizard
   * closed (Skip, Leave setup, relaunch). The generic message is used when unset.
   */
  unfinishedSaveKey?: TranslationKey;
  /** Dialog width for this step: 'wide' for steps with a map or a long list. Default when unset. */
  size?: WizardStepSize;
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
 * stays on the step and shows an error. A rejection that arrives after the wizard
 * closed is reported as an error toast instead.
 */
export type StepLeaveHandler = () => Promise<void>;

export interface WizardStepProps {
  /**
   * Reports whether the step is valid. A step must call it at mount and on every
   * validity change; until it does, Next stays disabled. `reason` is the i18n key
   * of why the step is not valid yet; it is shown beside Next and linked by
   * aria-describedby, and ignored when `valid` is true.
   */
  onValidChange?: (valid: boolean, reason?: TranslationKey) => void;
  /**
   * Registers the step's leave handler, which Next, Back and Done await before
   * navigating. Back runs it on an invalid step too, so the handler must send
   * only the parts that are valid now. Register once at mount; the returned
   * function unregisters the handler and must run when the step is destroyed.
   */
  registerLeaveHandler?: (handler: StepLeaveHandler) => () => void;
}

/** The model a model next steps flow was launched for, taken from its catalog entry. */
export interface InstalledModelContext {
  catalogId: string;
  name: string;
  category: CatalogEntry['category'];
  /** Classifier registry ID; absent for an entry that is not a classifier (the geomodel). */
  registryId?: string;
}

/** A step of the model next steps flow, shown when its predicate holds for the installed model. */
export type ModelNextStep = ComponentStep & {
  appliesTo: (model: InstalledModelContext) => boolean;
};

export interface WizardLaunchOptions {
  previousVersion?: string;
  currentVersion?: string;
  /** The installed model; required by the model next steps flow. */
  model?: InstalledModelContext;
}
