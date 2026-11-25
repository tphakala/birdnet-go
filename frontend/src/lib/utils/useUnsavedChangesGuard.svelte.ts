/**
 * Registers an unsaved-changes guard for as long as the calling component is mounted.
 */
import { untrack } from 'svelte';
import { unsavedChanges, type UnsavedChangesGuard } from '$lib/stores/unsavedChanges.svelte';

/**
 * Registers a guard with the unsaved-changes registry while the calling component is mounted, and
 * removes only its own registration on unmount. Call it during component initialisation.
 * `isDirty` is read at every navigation and unload decision, so it should read live state;
 * `options.allows` says which moves keep the component mounted (default: same path).
 */
export function useUnsavedChangesGuard(
  isDirty: () => boolean,
  options: { allows?: UnsavedChangesGuard['allows'] } = {}
): void {
  // untrack: register writes registry state, which tracking would turn into a loop
  $effect(() => untrack(() => unsavedChanges.register({ isDirty, allows: options.allows })));
}
