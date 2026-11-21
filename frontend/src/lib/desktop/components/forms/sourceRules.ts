import type { AudioSourceConfig } from '$lib/stores/settings';

/**
 * Save rules of the sound card and stream lists. Pure functions of the form
 * values, the saved values and (for streams) the set of tested URLs, so the page
 * can evaluate them whichever tab is open and the cards show the same messages.
 *
 * A problem that the saved configuration already has is never reported, so a
 * stored oddity cannot block Save on other tabs; only what the user changed can.
 */

/** What is wrong with one sound card source, by field. */
export interface SoundCardProblem {
  name?: 'required' | 'duplicate';
  device?: 'required' | 'duplicate';
}

function nameKey(name: string): string {
  return name.trim().toLowerCase();
}

/** Counts how often each key occurs in a list. */
function countKeys(keys: readonly string[]): Map<string, number> {
  const counts = new Map<string, number>();
  for (const key of keys) counts.set(key, (counts.get(key) ?? 0) + 1);
  return counts;
}

/**
 * True when a key occurs more often than the saved list had it. Counting instead
 * of looking positions up keeps the rule independent of row order and ids, and a
 * duplicate the saved list already holds stays unreported until it grows.
 */
function isNewDuplicate(
  key: string,
  current: ReadonlyMap<string, number>,
  saved: ReadonlyMap<string, number>
): boolean {
  return (current.get(key) ?? 0) > 1 && (current.get(key) ?? 0) > (saved.get(key) ?? 0);
}

/** Problems per sound card source, index for index; a source without one has an empty object. */
export function soundCardProblems(
  sources: readonly AudioSourceConfig[],
  savedSources: readonly AudioSourceConfig[]
): SoundCardProblem[] {
  const names = countKeys(sources.map(s => nameKey(s.name)));
  const savedNames = countKeys(savedSources.map(s => nameKey(s.name)));
  const devices = countKeys(sources.map(s => s.device.trim()));
  const savedDevices = countKeys(savedSources.map(s => s.device.trim()));

  return sources.map(source => {
    const problem: SoundCardProblem = {};
    const name = nameKey(source.name);
    if (!name) problem.name = 'required';
    else if (isNewDuplicate(name, names, savedNames)) problem.name = 'duplicate';
    const device = source.device.trim();
    if (!device) problem.device = 'required';
    else if (isNewDuplicate(device, devices, savedDevices)) problem.device = 'duplicate';
    return problem;
  });
}
