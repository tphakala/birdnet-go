import type { ModelNextStep, WizardFlow, WizardStep, WizardLaunchOptions } from './types';

// Onboarding steps, in the order they are shown
const onboardingSteps: WizardStep[] = [
  {
    id: 'welcome',
    type: 'component',
    titleKey: 'wizard.steps.welcome.title',
    component: () => import('./steps/WelcomeStep.svelte'),
  },
  {
    id: 'location-language',
    type: 'component',
    titleKey: 'wizard.steps.locationLanguage.title',
    component: () => import('./steps/LocationLanguageStep.svelte'),
    size: 'wide',
  },
  {
    id: 'audio-source',
    type: 'component',
    titleKey: 'wizard.steps.audioSource.title',
    component: () => import('./steps/AudioSourceStep.svelte'),
    unfinishedSaveKey: 'wizard.errors.audioSourceSaveUnfinished',
  },
  {
    id: 'detection',
    type: 'component',
    titleKey: 'wizard.steps.detection.title',
    component: () => import('./steps/DetectionStep.svelte'),
  },
  {
    id: 'integration',
    type: 'component',
    titleKey: 'wizard.steps.integration.title',
    component: () => import('./steps/IntegrationStep.svelte'),
  },
  {
    id: 'responsible-use',
    type: 'component',
    titleKey: 'wizard.steps.responsibleUse.title',
    component: () => import('./steps/ResponsibleUseStep.svelte'),
  },
];

// Recommended steps after a model install, in the order they are shown. Each
// step is offered only when its predicate holds for the installed model.
const modelNextSteps: ModelNextStep[] = [
  {
    id: 'enable-on-sources',
    type: 'component',
    titleKey: 'wizard.steps.enableOnSources.title',
    component: () => import('./steps/EnableOnSourcesStep.svelte'),
    appliesTo: model => Boolean(model.registryId),
  },
  // Extension point: a bat classifier gets a step that sets up its species range
  // filter by hand, as one more entry here with
  // `appliesTo: model => model.category === 'bat'`.
];

// Changelog registry, ordered from oldest to newest
interface ChangelogEntry {
  version: string;
  title: string;
  content: string;
}

const changelogs: ChangelogEntry[] = [
  // Entries added per release
];

export function getStepsForFlow(flow: WizardFlow, options?: WizardLaunchOptions): WizardStep[] {
  switch (flow) {
    case 'onboarding':
      return onboardingSteps;
    case 'model-next-steps': {
      const model = options?.model;
      return model ? modelNextSteps.filter(step => step.appliesTo(model)) : [];
    }
    case 'whats-new':
      return resolveChangelogSteps(options?.previousVersion, options?.currentVersion);
  }
}

function resolveChangelogSteps(previousVersion?: string, currentVersion?: string): WizardStep[] {
  if (!previousVersion || !currentVersion) return [];

  // Find all changelogs between previousVersion and currentVersion
  const prevIndex = changelogs.findIndex(c => c.version === previousVersion);
  const currIndex = changelogs.findIndex(c => c.version === currentVersion);

  // If either version not found, show nothing (dev builds, unknown versions)
  if (prevIndex === -1 || currIndex === -1) return [];

  // Get entries after previousVersion up to and including currentVersion
  const relevantEntries = changelogs.slice(prevIndex + 1, currIndex + 1);

  return relevantEntries.map(entry => ({
    id: `changelog-${entry.version}`,
    type: 'content' as const,
    title: entry.title,
    content: entry.content,
  }));
}
