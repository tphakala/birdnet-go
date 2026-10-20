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
 */

import { hasInvalidAddress, trimGoSpace, type NetworkAddressMode } from '$lib/utils/networkAddress';
import { deepEqual } from '$lib/utils/settingsChanges';

/** Reserved trusted-proxy token that expands to Cloudflare's edge ranges (mirrors conf.TrustedProxyCloudflarePreset). */
export const CLOUDFLARE_PRESET = 'cloudflare';

/** What a trusted-proxy row may hold: a single IP or a CIDR range. */
export const TRUSTED_PROXY_ACCEPT: NetworkAddressMode = 'ip-or-cidr';

/** True for the Cloudflare preset token in any case, ignoring surrounding spaces. */
export function isCloudflarePreset(entry: string): boolean {
  return trimGoSpace(entry).toLowerCase() === CLOUDFLARE_PRESET;
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
 * whitespace-only entries (whitespace as Go's strings.TrimSpace reads it) are valid to the backend and are not shown as rows.
 */
export function partitionTrustedProxies(list: readonly string[]): TrustedProxyPartition {
  const entries: string[] = [];
  let hasCloudflare = false;
  for (const entry of list) {
    if (isCloudflarePreset(entry)) {
      hasCloudflare = true;
    } else if (trimGoSpace(entry) !== '') {
      entries.push(entry);
    }
  }
  return { entries, hasCloudflare };
}

/** Whether any address row of a trusted-proxy list is not an IP or a CIDR; the preset token and blanks are not rows. */
export function hasInvalidTrustedProxy(list: readonly string[]): boolean {
  return hasInvalidAddress(partitionTrustedProxies(list).entries, TRUSTED_PROXY_ACCEPT);
}

/**
 * Build the list to store from edited rows and the Cloudflare state. A preset token
 * in `entries` is ignored: only `hasCloudflare` decides whether the token is stored.
 * Blank and whitespace-only entries in `entries` are ignored too.
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
  // The checkbox alone decides whether the token is stored, so a token in the rows is
  // dropped, and blank entries are not rows.
  const rows = entries.filter(entry => !isCloudflarePreset(entry) && trimGoSpace(entry) !== '');
  if (hasCloudflare === saved.hasCloudflare && deepEqual(rows, saved.entries)) {
    return [...savedList];
  }
  const result = [...rows];
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
