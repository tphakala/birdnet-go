/**
 * Pure helpers for editing the reverse proxy lists on the Security page.
 *
 * security.trustedProxies is a string array that mixes proxy addresses with the
 * reserved `cloudflare` preset token (conf.TrustedProxyCloudflarePreset). The
 * address list editor shows only the addresses and a checkbox owns the token, so
 * the form has to split the array and put it back together. The backend treats
 * the token's position and spelling as irrelevant, but the page compares the
 * saved and edited arrays by order and value, so every helper here returns the
 * saved array itself (as a copy) whenever the result describes the same entries
 * and the same Cloudflare state. That keeps the "unsaved changes" badge honest
 * and never rewrites a user's config order or spelling.
 *
 * security.allowSubnetBypass.subnet is a comma-separated string; splitSubnetList
 * and joinSubnetList convert it to and from list rows with the same rule.
 */

/** Reserved trusted-proxy token that expands to Cloudflare's edge ranges (mirrors conf.TrustedProxyCloudflarePreset). */
export const CLOUDFLARE_PRESET = 'cloudflare';

/** Separator written between subnets when a list is saved as text. */
const SUBNET_SEPARATOR = ', ';

/** True for the Cloudflare preset token in any case, ignoring surrounding spaces. */
export function isCloudflarePreset(entry: string): boolean {
  return entry.trim().toLowerCase() === CLOUDFLARE_PRESET;
}

/** A trusted-proxy list split into the rows the editor shows and the preset state. */
export interface TrustedProxyPartition {
  /** Address entries in list order, without preset tokens and without blank entries. */
  entries: string[];
  /** Whether the list holds at least one preset token. */
  hasCloudflare: boolean;
}

/**
 * Split a trusted-proxy list into editor rows and the Cloudflare state. Blank or
 * whitespace-only entries are valid to the backend and are not shown as rows.
 */
export function partitionTrustedProxies(list: readonly string[]): TrustedProxyPartition {
  const entries: string[] = [];
  let hasCloudflare = false;
  for (const entry of list) {
    if (isCloudflarePreset(entry)) {
      hasCloudflare = true;
    } else if (entry.trim() !== '') {
      entries.push(entry);
    }
  }
  return { entries, hasCloudflare };
}

function sameStrings(a: readonly string[], b: readonly string[]): boolean {
  return a.length === b.length && a.every((value, index) => value === b.at(index));
}

/**
 * Build the list to store from edited rows and the Cloudflare state.
 *
 * When the rows and the state equal those of `savedList`, a copy of `savedList`
 * is returned, so a round trip is the identity. Otherwise the result is the rows
 * with, when the preset is on, one token inserted at the position (clamped to the
 * number of rows) and in the spelling of the first token in `savedList`, or a
 * plain `cloudflare` appended when the saved list had none. Extra duplicate
 * tokens and blank entries are dropped on a real change.
 */
export function recombineTrustedProxies(
  entries: readonly string[],
  hasCloudflare: boolean,
  savedList: readonly string[]
): string[] {
  const saved = partitionTrustedProxies(savedList);
  if (hasCloudflare === saved.hasCloudflare && sameStrings(entries, saved.entries)) {
    return [...savedList];
  }
  const result = [...entries];
  if (!hasCloudflare) return result;

  const savedIndex = savedList.findIndex(isCloudflarePreset);
  if (savedIndex < 0) {
    result.push(CLOUDFLARE_PRESET);
  } else {
    result.splice(
      Math.min(savedIndex, result.length),
      0,
      savedList.at(savedIndex) ?? CLOUDFLARE_PRESET
    );
  }
  return result;
}

/**
 * Turn the Cloudflare preset on or off in `list`. When the list is already in the
 * requested state it is returned unchanged (toggling on twice adds one token).
 * Turning it off removes every spelling of the token; turning it on restores the
 * saved token or adds one `cloudflare`. See recombineTrustedProxies.
 */
export function setCloudflarePreset(
  list: readonly string[],
  enabled: boolean,
  savedList: readonly string[]
): string[] {
  const { entries, hasCloudflare } = partitionTrustedProxies(list);
  if (hasCloudflare === enabled) return [...list];
  return recombineTrustedProxies(entries, enabled, savedList);
}

/** Split the comma-separated subnet text into trimmed, non-blank values, as the backend does. */
export function splitSubnetList(text: string): string[] {
  return text
    .split(',')
    .map(value => value.trim())
    .filter(value => value !== '');
}

/**
 * Join subnet values into the text to store. Returns `saved` itself when it
 * already describes exactly `values`, so edits that return to the saved list
 * restore the saved string byte for byte; otherwise the values joined with ", ".
 */
export function joinSubnetList(values: readonly string[], saved: string): string {
  return sameStrings(splitSubnetList(saved), values) ? saved : values.join(SUBNET_SEPARATOR);
}
