import { onMount } from 'svelte';
import { get } from 'svelte/store';
import { settingsActions, settingsStore } from '$lib/stores/settings';
import type { StepLeaveHandler, WizardStepProps } from './types';

/**
 * Saves the settings store without toasts. The caller applies its edits to the
 * store first. On failure the store is reverted to the last saved state and the
 * error is rethrown. The revert runs while isCurrent() is true, and also after
 * the step is gone (the wizard was left while the save was in flight) as long
 * as the store still holds exactly what was submitted; if anything edited the
 * store since, such as a Settings page, those edits are kept.
 */
export async function saveStepSettings(isCurrent: () => boolean): Promise<void> {
  const submitted = JSON.stringify(get(settingsStore).formData);
  try {
    await settingsActions.saveSettings({ notify: false });
  } catch (err) {
    if (isCurrent() || JSON.stringify(get(settingsStore).formData) === submitted) {
      settingsActions.resetAllSettings();
    }
    throw err;
  }
}

/**
 * Wires a wizard step's commit into the wizard. Call it during component init,
 * passing a getter for the step's registerLeaveHandler prop so the prop is read
 * at mount. The commit is registered as the step's leave handler then, so Next, Back
 * and Done await it and it never runs when the step unmounts (Skip, Leave
 * setup); it is unregistered on destroy. Returns the save for the commit to
 * await after applying its edits; see saveStepSettings for when a failed save
 * reverts the store.
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
