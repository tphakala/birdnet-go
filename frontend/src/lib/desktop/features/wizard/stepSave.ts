import { onMount } from 'svelte';
import { settingsActions } from '$lib/stores/settings';
import type { StepLeaveHandler, WizardStepProps } from './types';

/**
 * Saves the settings store without toasts. The caller applies its edits to the
 * store first. On failure, if isCurrent() is still true, the store is reverted to
 * the last saved state; the error is always rethrown. Once the step is no longer
 * current the user may be editing a Settings page, so a late failure must not
 * revert the shared store.
 */
export async function saveStepSettings(isCurrent: () => boolean): Promise<void> {
  try {
    await settingsActions.saveSettings({ notify: false });
  } catch (err) {
    if (isCurrent()) settingsActions.resetAllSettings();
    throw err;
  }
}

/**
 * Wires a wizard step's commit into the wizard. Call it during component init,
 * passing a getter for the step's registerLeaveHandler prop so the prop is read
 * at mount. The commit is registered as the step's leave handler then, so Next, Back
 * and Done await it and it never runs when the step unmounts (Skip, Leave
 * setup); it is unregistered on destroy. Returns the save for the commit to
 * await after applying its edits; a failed save reverts the store only while the
 * step is still mounted.
 */
export function useStepSave(
  getRegister: () => WizardStepProps['registerLeaveHandler'],
  commit: StepLeaveHandler
): () => Promise<void> {
  let mounted = false;
  onMount(() => {
    mounted = true;
    const unregister = getRegister()?.(commit);
    return () => {
      mounted = false;
      unregister?.();
    };
  });
  return () => saveStepSettings(() => mounted);
}
