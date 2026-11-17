/**
 * Rows of the Analysis page's detection thresholds table.
 *
 * Each enabled classifier from GET /api/v2/models with a known registry ID gets one row, chosen by its
 * registry ID. The descriptor names the settings section that stores the
 * classifier's threshold, how the row behaves, and the field bounds.
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

/** BirdNET threshold shown until the birdnet section is loaded. */
export const PRIMARY_THRESHOLD_FALLBACK = 0.3;
/** Backend default threshold of the secondary classifier sections. */
export const SECONDARY_THRESHOLD_DEFAULT = 0.5;

/**
 * How a row behaves:
 * - primary: the BirdNET threshold, which other classifiers may follow
 * - override: follows BirdNET until the classifier's own threshold is switched on
 * - own: always uses the classifier's own threshold
 * - follows: has no threshold setting and always uses the BirdNET threshold
 */
export type ThresholdRowKind = 'primary' | 'override' | 'own' | 'follows';

/** Settings sections that hold a classifier threshold. */
export type ThresholdSection = 'birdnet' | 'perch' | 'birdnetv3' | 'birdnetplus' | 'bat' | 'bsgbat';

/** How one classifier's threshold row is rendered and saved. */
export interface ThresholdRowDescriptor {
  kind: ThresholdRowKind;
  /** Section the threshold is saved to; absent for rows that follow BirdNET. */
  section?: ThresholdSection;
  min: number;
  max: number;
  step: number;
}

/** One table row: the listed model and its descriptor. */
export interface ThresholdRow {
  model: BackendModel;
  descriptor: ThresholdRowDescriptor;
}

const PRIMARY_BOUNDS = { min: 0, max: 1, step: 0.05 };
const SECONDARY_BOUNDS = { min: 0.01, max: 0.99, step: 0.01 };

const DESCRIPTORS = new Map<string, ThresholdRowDescriptor>([
  [REGISTRY_ID.birdnetV24, { kind: 'primary', section: 'birdnet', ...PRIMARY_BOUNDS }],
  [REGISTRY_ID.perchV2, { kind: 'override', section: 'perch', ...SECONDARY_BOUNDS }],
  [REGISTRY_ID.birdnetV30, { kind: 'override', section: 'birdnetv3', ...SECONDARY_BOUNDS }],
  [REGISTRY_ID.birdnetPlusV30, { kind: 'override', section: 'birdnetplus', ...SECONDARY_BOUNDS }],
  [REGISTRY_ID.bat, { kind: 'own', section: 'bat', ...SECONDARY_BOUNDS }],
  [REGISTRY_ID.bsgBat, { kind: 'own', section: 'bsgbat', ...SECONDARY_BOUNDS }],
  [REGISTRY_ID.bsg, { kind: 'follows', ...PRIMARY_BOUNDS }],
]);

/** The descriptor for one listed model, or undefined when it has no threshold row. */
export function thresholdDescriptor(model: BackendModel): ThresholdRowDescriptor | undefined {
  // The store's stand-in entry (used before the first fetch) has no registry ID.
  const registryId =
    model.registryId ?? (model.id === BIRDNET_ALIAS ? REGISTRY_ID.birdnetV24 : undefined);
  return registryId === undefined ? undefined : DESCRIPTORS.get(registryId);
}

/** Table rows for the listed models: BirdNET first, the rest in list order. */
export function thresholdRows(models: readonly BackendModel[]): ThresholdRow[] {
  const rows: ThresholdRow[] = [];
  for (const model of models) {
    const descriptor = thresholdDescriptor(model);
    if (descriptor) rows.push({ model, descriptor });
  }
  return rows.sort(
    (a, b) => Number(b.descriptor.kind === 'primary') - Number(a.descriptor.kind === 'primary')
  );
}

/** The threshold fields of one settings section. */
export interface ThresholdSectionValues {
  threshold?: number;
  overrideThreshold?: boolean;
}

/**
 * The threshold values of the given rows, keyed by model id, with the backend
 * defaults filled in. Used for both the saved and the current side of the
 * card's change pair, so a section that is not loaded yet never shows as a change.
 */
export function thresholdSnapshot(
  rows: readonly ThresholdRow[],
  sections: Partial<Record<ThresholdSection, ThresholdSectionValues | undefined>>
): Record<string, unknown> {
  const snapshot: Record<string, unknown> = {};
  for (const { model, descriptor } of rows) {
    if (!descriptor.section) continue;
    const values = sections[descriptor.section];
    switch (descriptor.kind) {
      case 'primary':
        snapshot[model.id] = { threshold: values?.threshold ?? PRIMARY_THRESHOLD_FALLBACK };
        break;
      case 'override':
        snapshot[model.id] = {
          override: values?.overrideThreshold ?? false,
          threshold: values?.threshold ?? SECONDARY_THRESHOLD_DEFAULT,
        };
        break;
      case 'own':
      case 'follows':
        snapshot[model.id] = { threshold: values?.threshold ?? SECONDARY_THRESHOLD_DEFAULT };
        break;
    }
  }
  return snapshot;
}
