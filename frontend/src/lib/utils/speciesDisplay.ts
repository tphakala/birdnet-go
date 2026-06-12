import { localizeScientific } from '$lib/stores/speciesDictionary.svelte';

/**
 * The species common name to show this visitor, in their UI locale.
 * Fallback chain: client dictionary (visitor locale) -> server-provided common
 * name (server locale) -> scientific name. Reactive: call inside $derived/$effect.
 */
export function localizeSpeciesName(
  scientificName: string | undefined,
  fallbackCommonName?: string
): string {
  if (scientificName) {
    const localized = localizeScientific(scientificName);
    if (localized) return localized;
  }
  return fallbackCommonName ?? scientificName ?? '';
}
