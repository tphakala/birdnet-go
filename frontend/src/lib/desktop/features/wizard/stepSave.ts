import { onMount } from 'svelte';
import type { StepLeaveHandler, WizardStepProps } from './types';

/**
 * Wires a wizard step's commit into the wizard. Call it during component init,
 * passing a getter for the step's registerLeaveHandler prop so the prop is read
 * at mount. The commit is registered as the step's leave handler then, so Next,
 * Back and Done await it and it never runs when the step unmounts (Skip, Leave
 * setup); it is unregistered on destroy. The commit saves with
 * settingsActions.saveSection, which leaves the settings store untouched when
 * the request fails, so a failed save has nothing to revert.
 */
export function useStepSave(
  getRegister: () => WizardStepProps['registerLeaveHandler'],
  commit: StepLeaveHandler
): void {
  onMount(() => getRegister()?.(commit));
}
