/**
 * Registers a shortcut scope for as long as the calling component is mounted.
 */
import { untrack } from 'svelte';
import { shortcuts, type ShortcutScope } from '$lib/stores/shortcuts.svelte';

/**
 * Registers `scope` with the shortcut registry while the calling component is mounted, and
 * removes only its own registration on unmount. Call it during component initialisation. The scope
 * is read once: later changes to the object are not picked up, so its definitions should
 * read live state through closures.
 */
export function useShortcuts(scope: ShortcutScope): void {
  // untrack: register reads and writes the scope list, which tracking would turn into a loop
  $effect(() => untrack(() => shortcuts.register(scope)));
}
