/**
 * Rows of the Analysis page's detection thresholds list.
 *
 * Each enabled classifier from GET /api/v2/models with a known registry ID gets one row, chosen by its
 * registry ID. The descriptor names the settings section that stores the
 * classifier's threshold, how the row behaves, and the field bounds.
 *
 * The rows are peers. Some classifiers read the BirdNET v2.4 threshold (birdnet.threshold)
 * instead of a value of their own (backend modelGlobalConfidenceThreshold), and that value
 * applies even while BirdNET v2.4 is not enabled, so its row is kept whenever a listed
 * classifier can use it.
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
/** Name of BirdNET v2.4 for its row while the model is not in the list (a product name, not translated). */
const BIRDNET_NAME = 'BirdNET v2.4';
/** Category of BirdNET v2.4 in the models list. */
const BIRDNET_CATEGORY = 'bird';

/** BirdNET v2.4 threshold shown until the birdnet section is loaded. */
export const BIRDNET_THRESHOLD_FALLBACK = 0.3;
/** Backend default threshold of the other classifier sections. */
export const SECTION_THRESHOLD_DEFAULT = 0.5;

/**
 * How a row behaves:
 * - birdnet: BirdNET v2.4, whose threshold some other classifiers also use
 * - override: uses the BirdNET v2.4 threshold until the classifier's own threshold is switched on
 * - own: always uses the classifier's own threshold
 * - follows: has no threshold setting and always uses the BirdNET v2.4 threshold
 */
export type ThresholdRowKind = 'birdnet' | 'override' | 'own' | 'follows';

/** Settings sections that hold a classifier threshold. */
export type ThresholdSection = 'birdnet' | 'perch' | 'birdnetv3' | 'birdnetplus' | 'bat' | 'bsgbat';

/** How one classifier's threshold row is rendered and saved. */
export interface ThresholdRowDescriptor {
  kind: ThresholdRowKind;
  /** Section the threshold is saved to; absent for rows that always use the BirdNET v2.4 threshold. */
  section?: ThresholdSection;
  min: number;
  max: number;
  step: number;
}

/** One row: the model and its descriptor. */
export interface ThresholdRow {
  model: BackendModel;
  descriptor: ThresholdRowDescriptor;
  /**
   * False for the BirdNET v2.4 row kept while that model is not enabled, because its
   * threshold still applies to the listed classifiers that use it.
   */
  enabled: boolean;
}

/** Bounds of birdnet.threshold. */
const BIRDNET_BOUNDS = { min: 0, max: 1, step: 0.05 };
/** Bounds of the thresholds in the other classifier sections. */
const SECTION_BOUNDS = { min: 0.01, max: 0.99, step: 0.01 };

const DESCRIPTORS = new Map<string, ThresholdRowDescriptor>([
  [REGISTRY_ID.birdnetV24, { kind: 'birdnet', section: 'birdnet', ...BIRDNET_BOUNDS }],
  [REGISTRY_ID.perchV2, { kind: 'override', section: 'perch', ...SECTION_BOUNDS }],
  [REGISTRY_ID.birdnetV30, { kind: 'override', section: 'birdnetv3', ...SECTION_BOUNDS }],
  [REGISTRY_ID.birdnetPlusV30, { kind: 'override', section: 'birdnetplus', ...SECTION_BOUNDS }],
  [REGISTRY_ID.bat, { kind: 'own', section: 'bat', ...SECTION_BOUNDS }],
  [REGISTRY_ID.bsgBat, { kind: 'own', section: 'bsgbat', ...SECTION_BOUNDS }],
  [REGISTRY_ID.bsg, { kind: 'follows', ...BIRDNET_BOUNDS }],
]);

/** The descriptor for one listed model, or undefined when it has no threshold row. */
export function thresholdDescriptor(model: BackendModel): ThresholdRowDescriptor | undefined {
  // The store's stand-in entry (used before the first fetch) has no registry ID.
  const registryId =
    model.registryId ?? (model.id === BIRDNET_ALIAS ? REGISTRY_ID.birdnetV24 : undefined);
  return registryId === undefined ? undefined : DESCRIPTORS.get(registryId);
}

/** Whether a row of this kind can use the BirdNET v2.4 threshold. */
function canUseBirdnetThreshold(kind: ThresholdRowKind): boolean {
  return kind === 'override' || kind === 'follows';
}

/**
 * Rows for the listed models, in list order. When BirdNET v2.4 is not listed but a listed
 * classifier can use its threshold, a row for that threshold is added in front, so the value
 * stays editable.
 */
export function thresholdRows(models: readonly BackendModel[]): ThresholdRow[] {
  const rows: ThresholdRow[] = [];
  for (const model of models) {
    const descriptor = thresholdDescriptor(model);
    if (descriptor) rows.push({ model, descriptor, enabled: true });
  }
  const birdnetDescriptor = DESCRIPTORS.get(REGISTRY_ID.birdnetV24);
  if (
    birdnetDescriptor &&
    !rows.some(row => row.descriptor.kind === 'birdnet') &&
    rows.some(row => canUseBirdnetThreshold(row.descriptor.kind))
  ) {
    rows.unshift({
      model: {
        id: BIRDNET_ALIAS,
        registryId: REGISTRY_ID.birdnetV24,
        name: BIRDNET_NAME,
        category: BIRDNET_CATEGORY,
      },
      descriptor: birdnetDescriptor,
      enabled: false,
    });
  }
  return rows;
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
      case 'birdnet':
        snapshot[model.id] = { threshold: values?.threshold ?? BIRDNET_THRESHOLD_FALLBACK };
        break;
      case 'override':
        snapshot[model.id] = {
          override: values?.overrideThreshold ?? false,
          threshold: values?.threshold ?? SECTION_THRESHOLD_DEFAULT,
        };
        break;
      case 'own':
      case 'follows':
        snapshot[model.id] = { threshold: values?.threshold ?? SECTION_THRESHOLD_DEFAULT };
        break;
    }
  }
  return snapshot;
}
