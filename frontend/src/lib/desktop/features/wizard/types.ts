import type { Component } from 'svelte';

export type WizardFlow = 'onboarding' | 'whats-new';
export type WizardStatus = 'idle' | 'active' | 'completed';

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

export interface WizardStepProps {
  onValidChange?: (valid: boolean) => void;
}

export interface WizardLaunchOptions {
  previousVersion?: string;
  currentVersion?: string;
}
