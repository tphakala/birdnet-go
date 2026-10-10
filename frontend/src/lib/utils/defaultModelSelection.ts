/**
 * Pure helper that turns the classifier's acoustic model availability into the
 * config-alias selection an audio source editor should pre-select.
 *
 * The two ID spaces never mix by accident here: `defaultTargets` carries
 * classifier REGISTRY IDs ("BirdNET_V2.4"), while source model lists and the
 * editor checkboxes carry config ALIASES ("birdnet"). The join key is
 * `registryId` on each option (GET /api/v2/models); when a server predates that
 * field the case-insensitive alias match and then the legacy heuristic apply.
 */
import { DEFAULT_MODEL_ID } from '$lib/stores/models.svelte';
import type { AcousticModelAvailability } from '$lib/types/models';

/** The subset of a model list entry the selection logic needs. */
export interface ModelSelectionOption {
  /** Config alias, the value persisted in a source's model list. */
  id: string;
  /** Classifier registry ID; absent on an older server. */
  registryId?: string;
}

/**
 * Pre-v4 default pick, used whenever no classifier verdict is available:
 * BirdNET when it is offered, else the first option, else nothing. It never
 * invents a model that is not in the list.
 */
export function legacyDefaultSelection(options: readonly ModelSelectionOption[]): string[] {
  if (options.some(option => option.id === DEFAULT_MODEL_ID)) {
    return [DEFAULT_MODEL_ID];
  }
  return options.length > 0 ? [options[0].id] : [];
}

/**
 * Map registry IDs onto option aliases, preserving target order and dropping
 * duplicates and unmatched targets. An exact `registryId` match wins; a
 * case-insensitive alias match is the fallback for servers without the field.
 */
function mapDefaultTargets(
  targets: readonly string[],
  options: readonly ModelSelectionOption[]
): string[] {
  const selected: string[] = [];
  for (const target of targets) {
    const lowerTarget = target.toLowerCase();
    const match =
      options.find(option => option.registryId === target) ??
      options.find(option => option.id.toLowerCase() === lowerTarget);
    if (match && !selected.includes(match.id)) {
      selected.push(match.id);
    }
  }
  return selected;
}

/**
 * The model aliases to pre-select for a source that has no explicit list.
 * - ready: the mapped default targets (BirdNET v2.4 first); the legacy pick if
 *   none of them can be mapped onto the offered options.
 * - none: nothing. The source is saved with an empty list, which the backend
 *   resolves to its defaults as soon as a model loads.
 * - unknown: the legacy pick.
 */
export function defaultModelSelection(
  availability: AcousticModelAvailability,
  options: readonly ModelSelectionOption[]
): string[] {
  switch (availability.kind) {
    case 'none':
      return [];
    case 'ready': {
      const mapped = mapDefaultTargets(availability.defaultTargets, options);
      return mapped.length > 0 ? mapped : legacyDefaultSelection(options);
    }
    case 'unknown':
      return legacyDefaultSelection(options);
  }
}
