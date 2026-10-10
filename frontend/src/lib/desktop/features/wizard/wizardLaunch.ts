/** Outcome of the App's own settings load, as seen by the wizard launch decision. */
export type SettingsLoadState = 'pending' | 'loaded' | 'failed' | 'skipped';

/** What the App should do about the wizard right now. */
export type WizardLaunchDecision = 'wait' | 'none' | 'onboarding' | 'whats-new';

/**
 * Decides whether and how to launch the wizard.
 *
 * Onboarding saves settings, so it launches only after the App's settings load
 * resolved ('loaded'); while the load is 'pending' the caller waits, and when the
 * load 'failed' or was 'skipped' (no access yet) onboarding is not launched and
 * nothing is dismissed, so the next page load tries again. The changelog flow
 * never saves, so it does not wait for settings.
 */
export function resolveWizardLaunch(input: {
  /** Version the wizard was last dismissed for in localStorage, if any */
  dismissedVersion: string | null;
  /** Current application version */
  version: string;
  freshInstall: boolean;
  newVersion: boolean;
  settingsLoad: SettingsLoadState;
}): WizardLaunchDecision {
  if (input.dismissedVersion === input.version) return 'none';

  if (input.freshInstall) {
    if (input.settingsLoad === 'pending') return 'wait';
    return input.settingsLoad === 'loaded' ? 'onboarding' : 'none';
  }

  return input.newVersion ? 'whats-new' : 'none';
}
