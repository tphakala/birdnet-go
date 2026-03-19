import type { WizardFlow, WizardStep, WizardLaunchOptions } from './types';

// Onboarding steps — static list, content added later
const onboardingSteps: WizardStep[] = [
  // Placeholder — steps will be added by the user later
];

// Changelog registry — ordered from oldest to newest
interface ChangelogEntry {
  version: string;
  title: string;
  content: string;
}

const changelogs: ChangelogEntry[] = [
  // Entries added per release
];

export function getStepsForFlow(flow: WizardFlow, options?: WizardLaunchOptions): WizardStep[] {
  if (flow === 'onboarding') {
    return onboardingSteps;
  }

  // flow === 'whats-new'
  return resolveChangelogSteps(options?.previousVersion, options?.currentVersion);
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
