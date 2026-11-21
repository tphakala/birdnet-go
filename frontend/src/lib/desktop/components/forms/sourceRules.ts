import type { AudioSourceConfig } from '$lib/stores/settings';

/**
 * Save rules of the sound card list. Pure functions of the form values and the
 * saved values, so the page can evaluate them whichever tab is open and the
 * cards show the same messages.
 *
 * A duplicate that the saved configuration already has is not reported, so a
 * stored oddity cannot block Save on other tabs; only what the user changed can.
 * An empty name or device is always reported: the server refuses to save one.
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

/**
 * The names of the sources that have a problem, each listed once, in list order.
 * Two cards that share a name are both flagged, but naming that name twice would
 * not help the user find them. A source without a name is labelled by `unnamed`
 * with its one-based position.
 */
export function problemSourceNames(
  sources: readonly AudioSourceConfig[],
  problems: readonly SoundCardProblem[],
  unnamed: (position: number) => string
): string[] {
  const names = new Set<string>();
  problems.forEach((problem, index) => {
    if (!problem.name && !problem.device) return;
    const name = sources.at(index)?.name.trim() ?? '';
    names.add(name.length > 0 ? name : unnamed(index + 1));
  });
  return [...names];
}
