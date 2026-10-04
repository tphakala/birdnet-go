import { settingsActions } from '$lib/stores/settings';

/**
 * Applies a wizard step's edits to the settings store and saves them without
 * toasts. On failure, if isCurrent() is still true, the store is reverted to the
 * last saved state; the error is always rethrown. Once the step is no longer
 * current the user may be editing a Settings page, so a late failure must not
 * revert the shared store.
 */
export async function saveStepSettings(apply: () => void, isCurrent: () => boolean): Promise<void> {
  apply();
  try {
    await settingsActions.saveSettings({ notify: false });
  } catch (err) {
    if (isCurrent()) settingsActions.resetAllSettings();
    throw err;
  }
}
