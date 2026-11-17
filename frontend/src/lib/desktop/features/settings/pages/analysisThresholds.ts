/**
 * Rows of the Analysis page's detection thresholds list.
 *
 * The backend has one shared threshold (birdnet.threshold) and per-classifier thresholds
 * (backend modelGlobalConfidenceThreshold). The shared threshold is a setting of its own,
 * not tied to any model: it applies whichever classifiers are installed. Each enabled
 * classifier with a known registry ID gets one row, and its descriptor says whether it
 * always uses the shared threshold, may switch to a custom one, or always has its own.
 */
import type { BackendModel } from '$lib/stores/models.svelte';

/** Registry IDs, mirroring the Go constants in internal/classifier (RegistryID*). */
export const REGISTRY_ID = {
  birdnetV24: 'BirdNET_V2.4',
  birdnetV30: 'BirdNET_V3.0',
  birdnetPlusV30: 'BirdNET_Plus_V3.0',
  perchV2: 'Perch_V2',
  bat: 'Bat',
  bsg: 'BSG',
  bsgBat: 'BSG_BAT',
} as const;

/** Config alias of the built-in BirdNET model (conf.ModelIDBirdNET). */
const BIRDNET_ALIAS = 'birdnet';

/** Shared threshold shown until the birdnet section is loaded. */
export const SHARED_THRESHOLD_FALLBACK = 0.3;
/** Backend default threshold of the per-classifier sections. */
export const SECTION_THRESHOLD_DEFAULT = 0.5;

/**
 * How a classifier's row behaves:
 * - shared: has no threshold setting of its own and always uses the shared threshold
 * - optional: uses the shared threshold until its custom threshold is switched on
 * - own: always uses its own threshold
 */
export type ThresholdRowKind = 'shared' | 'optional' | 'own';

/** Settings sections that hold a threshold (birdnet holds the shared threshold). */
export type ThresholdSection = 'birdnet' | 'perch' | 'birdnetv3' | 'birdnetplus' | 'bat' | 'bsgbat';

/** Settings sections that hold a classifier's own threshold. */
export type ClassifierThresholdSection = Exclude<ThresholdSection, 'birdnet'>;

/** Bounds of a threshold field. */
export interface ThresholdBounds {
  min: number;
  max: number;
  step: number;
}

/** How one classifier's threshold row is rendered and saved. */
export interface ThresholdRowDescriptor extends ThresholdBounds {
  kind: ThresholdRowKind;
  /** Section of the classifier's own threshold; absent for rows that always use the shared one. */
  section?: ClassifierThresholdSection;
}

/** One row: the model and its descriptor. */
export interface ThresholdRow {
  model: BackendModel;
  descriptor: ThresholdRowDescriptor;
}

/** Bounds of the shared threshold, also used by rows that show it. */
export const SHARED_BOUNDS: ThresholdBounds = { min: 0, max: 1, step: 0.01 };
/** Bounds of the per-classifier thresholds. */
const SECTION_BOUNDS: ThresholdBounds = { min: 0.01, max: 0.99, step: 0.01 };

const DESCRIPTORS = new Map<string, ThresholdRowDescriptor>([
  [REGISTRY_ID.birdnetV24, { kind: 'shared', ...SHARED_BOUNDS }],
  [REGISTRY_ID.bsg, { kind: 'shared', ...SHARED_BOUNDS }],
  [REGISTRY_ID.perchV2, { kind: 'optional', section: 'perch', ...SECTION_BOUNDS }],
  [REGISTRY_ID.birdnetV30, { kind: 'optional', section: 'birdnetv3', ...SECTION_BOUNDS }],
  [REGISTRY_ID.birdnetPlusV30, { kind: 'optional', section: 'birdnetplus', ...SECTION_BOUNDS }],
  [REGISTRY_ID.bat, { kind: 'own', section: 'bat', ...SECTION_BOUNDS }],
  [REGISTRY_ID.bsgBat, { kind: 'own', section: 'bsgbat', ...SECTION_BOUNDS }],
]);

/** The descriptor for one listed model, or undefined when it has no threshold row. */
export function thresholdDescriptor(model: BackendModel): ThresholdRowDescriptor | undefined {
  // The store's stand-in entry (used before the first fetch) has no registry ID.
  const registryId =
    model.registryId ?? (model.id === BIRDNET_ALIAS ? REGISTRY_ID.birdnetV24 : undefined);
  return registryId === undefined ? undefined : DESCRIPTORS.get(registryId);
}

/** Rows for the listed models that have a known threshold, in list order. */
export function thresholdRows(models: readonly BackendModel[]): ThresholdRow[] {
  const rows: ThresholdRow[] = [];
  for (const model of models) {
    const descriptor = thresholdDescriptor(model);
    if (descriptor) rows.push({ model, descriptor });
  }
  return rows;
}

/** The threshold fields of one settings section. */
export interface ThresholdSectionValues {
  threshold?: number;
  overrideThreshold?: boolean;
}

/** Whether a row currently uses the shared threshold. */
export function usesSharedThreshold(
  descriptor: ThresholdRowDescriptor,
  values: ThresholdSectionValues | undefined
): boolean {
  if (descriptor.kind === 'shared') return true;
  if (descriptor.kind === 'own') return false;
  return !(values?.overrideThreshold ?? false);
}

/** Snapshot key of the shared threshold; model ids never contain a colon. */
const SHARED_KEY = 'shared:threshold';

/**
 * The shared threshold and the per-classifier values of the given rows, with the backend
 * defaults filled in. Used for both the saved and the current side of the card's change
 * pair, so a section that is not loaded yet never shows as a change.
 */
export function thresholdSnapshot(
  rows: readonly ThresholdRow[],
  sections: Partial<Record<ThresholdSection, ThresholdSectionValues | undefined>>
): Record<string, unknown> {
  const snapshot: Record<string, unknown> = {
    [SHARED_KEY]: sections.birdnet?.threshold ?? SHARED_THRESHOLD_FALLBACK,
  };
  for (const { model, descriptor } of rows) {
    if (!descriptor.section) continue;
    const values = sections[descriptor.section];
    snapshot[model.id] =
      descriptor.kind === 'optional'
        ? {
            override: values?.overrideThreshold ?? false,
            threshold: values?.threshold ?? SECTION_THRESHOLD_DEFAULT,
          }
        : { threshold: values?.threshold ?? SECTION_THRESHOLD_DEFAULT };
  }
  return snapshot;
}
