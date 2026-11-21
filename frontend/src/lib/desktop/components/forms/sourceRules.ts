import type { AudioSourceConfig, StreamConfig } from '$lib/stores/settings';
import { isStreamNameTooLong, isValidStreamUrl } from './streamOptions';

/**
 * Save rules of the sound card and stream lists. Pure functions of the form
 * values and the saved values, so the page can evaluate them whichever tab is open and the
 * cards show the same messages.
 *
 * A duplicate that the saved configuration already has is not reported, so a
 * stored oddity cannot block Save on other tabs; only what the user changed can.
 * An empty name, device or URL is always reported: the server refuses to save one.
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
 * The names of the entries (by list index) that are flagged, each listed once, in
 * list order. Two entries that share a name are both flagged, but naming that name
 * twice would not help the user find them. An entry without a name is labelled by
 * `unnamed` with its one-based position.
 */
function flaggedNames(
  items: readonly { name: string }[],
  flagged: (index: number) => boolean,
  unnamed: (position: number) => string
): string[] {
  const names = new Set<string>();
  items.forEach((item, index) => {
    if (!flagged(index)) return;
    const name = item.name.trim();
    names.add(name.length > 0 ? name : unnamed(index + 1));
  });
  return [...names];
}

/** The names of the sources that have a problem, each listed once, in list order. */
export function problemSourceNames(
  sources: readonly AudioSourceConfig[],
  problems: readonly SoundCardProblem[],
  unnamed: (position: number) => string
): string[] {
  return flaggedNames(
    sources,
    index => {
      const problem = problems.at(index);
      return Boolean(problem?.name ?? problem?.device);
    },
    unnamed
  );
}

/** What is wrong with one stream, by field. */
export interface StreamProblem {
  name?: 'required' | 'tooLong' | 'duplicate';
  url?: 'required' | 'invalid' | 'duplicate' | 'untested';
}

/**
 * Problems per stream, index for index; a stream without one has an empty object.
 * `untested` means the trimmed URL is neither the URL of a saved stream nor in
 * `testedUrls`: the test belongs to the URL string, never to a row, so it follows
 * a URL across edits, deletes and swaps. A name or URL the saved configuration
 * already holds is not reported (a URL is checked against its saved type).
 */
export function streamProblems(
  streams: readonly StreamConfig[],
  savedStreams: readonly StreamConfig[],
  testedUrls: ReadonlySet<string>
): StreamProblem[] {
  const names = countKeys(streams.map(s => nameKey(s.name)));
  const savedNames = countKeys(savedStreams.map(s => nameKey(s.name)));
  const urls = countKeys(streams.map(s => s.url.trim()));
  const savedUrls = countKeys(savedStreams.map(s => s.url.trim()));
  const savedPairs = new Set(savedStreams.map(s => `${s.type}\n${s.url.trim()}`));

  return streams.map(stream => {
    const problem: StreamProblem = {};
    const name = nameKey(stream.name);
    if (!name) problem.name = 'required';
    else if (isStreamNameTooLong(stream.name.trim()) && !savedNames.has(name)) {
      problem.name = 'tooLong';
    } else if (isNewDuplicate(name, names, savedNames)) problem.name = 'duplicate';

    const url = stream.url.trim();
    if (!url) problem.url = 'required';
    else if (!isValidStreamUrl(url, stream.type) && !savedPairs.has(`${stream.type}\n${url}`)) {
      problem.url = 'invalid';
    } else if (isNewDuplicate(url, urls, savedUrls)) problem.url = 'duplicate';
    else if (!savedUrls.has(url) && !testedUrls.has(url)) problem.url = 'untested';
    return problem;
  });
}

/**
 * The names of the streams the save bar lists, each stream once. `invalid` lists
 * streams with a name problem or a URL problem other than `untested`; `untested`
 * lists the streams whose only problem is a URL that still needs its test, so a
 * stream with both is named by `invalid` until that is fixed.
 */
export function problemStreamNames(
  streams: readonly StreamConfig[],
  problems: readonly StreamProblem[],
  unnamed: (position: number) => string,
  kind: 'invalid' | 'untested'
): string[] {
  return flaggedNames(
    streams,
    index => {
      const problem = problems.at(index);
      if (!problem) return false;
      const invalid =
        Boolean(problem.name) || (problem.url !== undefined && problem.url !== 'untested');
      return kind === 'invalid' ? invalid : !invalid && problem.url === 'untested';
    },
    unnamed
  );
}
