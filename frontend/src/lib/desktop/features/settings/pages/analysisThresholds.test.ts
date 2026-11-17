import { describe, it, expect } from 'vitest';
import type { BackendModel } from '$lib/stores/models.svelte';
import {
  REGISTRY_ID,
  SECTION_THRESHOLD_DEFAULT,
  SHARED_THRESHOLD_FALLBACK,
  thresholdDescriptor,
  thresholdRows,
  thresholdSnapshot,
  usesSharedThreshold,
} from './analysisThresholds';

/** The descriptor for a registry ID, failing the test when there is none. */
function descriptorOf(registryId: string) {
  const descriptor = thresholdDescriptor(model(registryId));
  if (!descriptor) throw new Error(`no descriptor for ${registryId}`);
  return descriptor;
}

function model(registryId: string | undefined, id = registryId ?? 'birdnet'): BackendModel {
  return { id, registryId, name: id, category: 'bird' };
}

describe('thresholdDescriptor', () => {
  // Pins the map against a typo in a registry ID, which would silently drop a classifier's row.
  it.each([
    [REGISTRY_ID.birdnetV24, 'shared', undefined],
    [REGISTRY_ID.bsg, 'shared', undefined],
    [REGISTRY_ID.perchV2, 'optional', 'perch'],
    [REGISTRY_ID.birdnetV30, 'optional', 'birdnetv3'],
    [REGISTRY_ID.birdnetPlusV30, 'optional', 'birdnetplus'],
    [REGISTRY_ID.bat, 'own', 'bat'],
    [REGISTRY_ID.bsgBat, 'own', 'bsgbat'],
  ])('maps %s to a %s row saved to %s', (registryId, kind, section) => {
    const descriptor = thresholdDescriptor(model(registryId));
    expect(descriptor?.kind).toBe(kind);
    expect(descriptor?.section).toBe(section);
  });

  it('maps the stand-in BirdNET entry without a registry ID to the shared kind', () => {
    expect(thresholdDescriptor(model(undefined, 'birdnet'))?.kind).toBe('shared');
  });

  it('gives no descriptor to an unknown registry ID', () => {
    expect(thresholdDescriptor(model('Mystery_V1'))).toBeUndefined();
  });
});

describe('thresholdRows', () => {
  it('keeps list order and drops models without a known threshold', () => {
    const rows = thresholdRows([
      model(REGISTRY_ID.perchV2),
      model('Mystery_V1'),
      model(REGISTRY_ID.birdnetV24),
    ]);
    expect(rows.map(row => row.model.registryId)).toEqual([
      REGISTRY_ID.perchV2,
      REGISTRY_ID.birdnetV24,
    ]);
  });
});

describe('usesSharedThreshold', () => {
  const shared = descriptorOf(REGISTRY_ID.bsg);
  const optional = descriptorOf(REGISTRY_ID.perchV2);
  const own = descriptorOf(REGISTRY_ID.bat);

  it.each([
    ['a classifier without a threshold of its own', shared, undefined, true],
    ['an optional classifier with no saved section', optional, undefined, true],
    [
      'an optional classifier with its custom threshold off',
      optional,
      { overrideThreshold: false },
      true,
    ],
    [
      'an optional classifier with its custom threshold on',
      optional,
      { overrideThreshold: true },
      false,
    ],
    ['a classifier that always has its own threshold', own, undefined, false],
  ])('is %s: %s', (_name, descriptor, values, expected) => {
    expect(usesSharedThreshold(descriptor, values)).toBe(expected);
  });
});

describe('thresholdSnapshot', () => {
  const rows = thresholdRows([
    model(REGISTRY_ID.birdnetV24),
    model(REGISTRY_ID.perchV2),
    model(REGISTRY_ID.bat),
  ]);

  it('treats sections not loaded yet as their backend defaults, so they are no change', () => {
    const defaults = thresholdSnapshot(rows, {
      birdnet: { threshold: SHARED_THRESHOLD_FALLBACK },
      perch: { overrideThreshold: false, threshold: SECTION_THRESHOLD_DEFAULT },
      bat: { threshold: SECTION_THRESHOLD_DEFAULT },
    });
    expect(thresholdSnapshot(rows, {})).toEqual(defaults);
  });

  it('always holds the shared threshold, also with no classifier rows', () => {
    expect(thresholdSnapshot([], {})).not.toEqual(
      thresholdSnapshot([], { birdnet: { threshold: 0.9 } })
    );
  });

  it('ignores the section of a classifier that has no row', () => {
    const perchOnly = thresholdRows([model(REGISTRY_ID.perchV2)]);
    expect(thresholdSnapshot(perchOnly, { bat: { threshold: 0.9 } })).toEqual(
      thresholdSnapshot(perchOnly, {})
    );
  });
});
