/**
 * Shape checks for the answers the detail page reads. Each guard takes `unknown`
 * and accepts a body only when the fields the page dereferences have the type it
 * expects, so a null, array or reshaped answer is treated like a failed request
 * instead of reaching a card as a typed object.
 *
 * Optional fields are checked only when present; fields the page never reads are
 * not checked.
 */
import type { ImageAttribution } from '$lib/types/detection.types';
import { isPlainObject } from '$lib/utils/security';
import type {
  DailyCounts,
  DetectionDetailData,
  DetectionNeighbors,
  DetectionPredictions,
  DetectionWeather,
  HourlyBucket,
  SpeciesRarity,
  SpeciesSummary,
  SunTimes,
  TaxonomyHierarchy,
} from './detailTypes';

type Kind = 'string' | 'number' | 'boolean';
type Fields = readonly (readonly [key: string, kind: Kind])[];

/** The value of a field of an object whose key is one of this module's own literals. */
function field(record: Record<string, unknown>, key: string): unknown {
  // eslint-disable-next-line security/detect-object-injection -- key is a literal of this module
  return record[key];
}

/** True when every required field has its kind and every optional one has it when present. */
function fieldsMatch(
  record: Record<string, unknown>,
  required: Fields,
  optional: Fields = []
): boolean {
  return (
    required.every(([key, kind]) => typeof field(record, key) === kind) &&
    optional.every(([key, kind]) => {
      const value = field(record, key);
      return value === undefined || typeof value === kind;
    })
  );
}

/** A number that is a real number: JSON cannot carry NaN or Infinity, but a guard should not rely on it. */
function isCount(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value);
}

/** An optional nested object whose fields match; absent is fine. */
function optionalObject(record: Record<string, unknown>, key: string, optional: Fields): boolean {
  const value = field(record, key);
  return value === undefined || (isPlainObject(value) && fieldsMatch(value, [], optional));
}

const DETECTION_REQUIRED: Fields = [
  ['id', 'number'],
  ['date', 'string'],
  ['time', 'string'],
  ['scientificName', 'string'],
  ['commonName', 'string'],
  ['confidence', 'number'],
  ['verified', 'string'],
  ['locked', 'boolean'],
];
const DETECTION_OPTIONAL: Fields = [
  ['timestamp', 'string'],
  ['beginTime', 'string'],
  ['endTime', 'string'],
  ['clipName', 'string'],
  ['spectrogramOnly', 'boolean'],
  ['timeOfDay', 'string'],
  ['firstReviewedAt', 'string'],
  ['lastReviewedAt', 'string'],
  ['lockedAt', 'string'],
];
const VERDICTS: readonly unknown[] = ['correct', 'false_positive', 'unverified'];

/** GET /api/v2/detections/:id */
export function isDetectionDetail(value: unknown): value is DetectionDetailData {
  if (!isPlainObject(value)) return false;
  if (!fieldsMatch(value, DETECTION_REQUIRED, DETECTION_OPTIONAL)) return false;
  if (!VERDICTS.includes(value.verified)) return false;
  const { comments, modelContributions } = value;
  const commentsValid =
    comments === undefined ||
    (Array.isArray(comments) &&
      comments.every(
        (comment: unknown) =>
          isPlainObject(comment) &&
          fieldsMatch(
            comment,
            [
              ['id', 'number'],
              ['entry', 'string'],
              ['createdAt', 'string'],
            ],
            [['updatedAt', 'string']]
          )
      ));
  const contributionsValid =
    modelContributions === undefined ||
    (Array.isArray(modelContributions) &&
      modelContributions.every(
        (entry: unknown) =>
          isPlainObject(entry) &&
          fieldsMatch(
            entry,
            [
              ['modelName', 'string'],
              ['modelVersion', 'string'],
              ['hitCount', 'number'],
            ],
            [
              ['modelVariant', 'string'],
              ['firstHitAt', 'string'],
              ['lastHitAt', 'string'],
            ]
          )
      ));
  return commentsValid && contributionsValid;
}

const HOURLY_OPTIONAL: Fields = [
  ['time', 'string'],
  ['temperature', 'number'],
  ['feels_like', 'number'],
  ['humidity', 'number'],
  ['wind_speed', 'number'],
  ['wind_deg', 'number'],
  ['clouds', 'number'],
  ['precipitation', 'number'],
  ['weather_icon', 'string'],
  ['weather_desc', 'string'],
];

/** GET /api/v2/weather/detection/:id */
export function isDetectionWeather(value: unknown): value is DetectionWeather {
  if (!isPlainObject(value)) return false;
  return (
    fieldsMatch(value, [], [['time_of_day', 'string']]) &&
    optionalObject(value, 'daily', [
      ['sunrise', 'string'],
      ['sunset', 'string'],
    ]) &&
    optionalObject(value, 'hourly', HOURLY_OPTIONAL)
  );
}

/** GET /api/v2/weather/sun/:date */
export function isSunTimes(value: unknown): value is SunTimes {
  return (
    isPlainObject(value) &&
    fieldsMatch(
      value,
      [['date', 'string']],
      [
        ['sunrise', 'string'],
        ['sunset', 'string'],
      ]
    )
  );
}

const NEIGHBOR_KEYS = ['previous', 'next', 'previousSameSpecies', 'nextSameSpecies'] as const;

/** GET /api/v2/detections/:id/neighbors */
export function isDetectionNeighbors(value: unknown): value is DetectionNeighbors {
  if (!isPlainObject(value) || !isCount(value.detectionId)) return false;
  return NEIGHBOR_KEYS.every(key => {
    const neighbor = field(value, key);
    return neighbor === null || isCount(neighbor);
  });
}

/** GET /api/v2/detections/:id/predictions */
export function isDetectionPredictions(value: unknown): value is DetectionPredictions {
  if (!isPlainObject(value) || !isCount(value.detectionId)) return false;
  const { modelPredictions } = value;
  if (modelPredictions === undefined) return true;
  return (
    Array.isArray(modelPredictions) &&
    modelPredictions.every((set: unknown) => {
      if (!isPlainObject(set)) return false;
      if (!fieldsMatch(set, [['modelName', 'string']], [['modelVersion', 'string']])) return false;
      const { predictions } = set;
      return (
        predictions === undefined ||
        (Array.isArray(predictions) &&
          predictions.every(
            (prediction: unknown) =>
              isPlainObject(prediction) &&
              fieldsMatch(
                prediction,
                [
                  ['scientificName', 'string'],
                  ['confidence', 'number'],
                ],
                [
                  ['commonName', 'string'],
                  ['original', 'boolean'],
                ]
              )
          ))
      );
    })
  );
}

/** One row of GET /api/v2/analytics/species/summary. */
export function isSpeciesSummary(value: unknown): value is SpeciesSummary {
  return (
    isPlainObject(value) &&
    fieldsMatch(
      value,
      [
        ['scientific_name', 'string'],
        ['count', 'number'],
      ],
      [
        ['common_name', 'string'],
        ['first_heard', 'string'],
        ['last_heard', 'string'],
        ['avg_confidence', 'number'],
        ['max_confidence', 'number'],
      ]
    )
  );
}

/** GET /api/v2/analytics/time/daily */
export function isDailyCounts(value: unknown): value is DailyCounts {
  if (!isPlainObject(value)) return false;
  const { data } = value;
  const dataValid =
    data === undefined ||
    (Array.isArray(data) &&
      data.every(
        (day: unknown) => isPlainObject(day) && typeof day.date === 'string' && isCount(day.count)
      ));
  return dataValid && (value.total === undefined || isCount(value.total));
}

/** GET /api/v2/analytics/time/distribution/hourly */
export function isHourlyBuckets(value: unknown): value is HourlyBucket[] {
  return (
    Array.isArray(value) &&
    value.every(
      (bucket: unknown) => isPlainObject(bucket) && isCount(bucket.hour) && isCount(bucket.count)
    )
  );
}

/** The rarity block of GET /api/v2/species. */
export function isSpeciesRarity(value: unknown): value is SpeciesRarity {
  return (
    isPlainObject(value) &&
    fieldsMatch(
      value,
      [
        ['status', 'string'],
        ['score', 'number'],
        ['location_based', 'boolean'],
      ],
      [
        ['date', 'string'],
        ['latitude', 'number'],
        ['longitude', 'number'],
      ]
    )
  );
}

/** The taxonomy block of GET /api/v2/species/taxonomy. */
export function isTaxonomy(value: unknown): value is TaxonomyHierarchy {
  return (
    isPlainObject(value) &&
    fieldsMatch(
      value,
      [],
      [
        ['kingdom', 'string'],
        ['phylum', 'string'],
        ['class', 'string'],
        ['order', 'string'],
        ['family', 'string'],
        ['family_common', 'string'],
        ['genus', 'string'],
        ['species', 'string'],
      ]
    )
  );
}

/**
 * GET /api/v2/media/species-image/info. Any field may be missing; a missing one reads
 * as empty. Null when the body is not an object.
 */
export function parseImageAttribution(value: unknown): ImageAttribution | null {
  if (!isPlainObject(value)) return null;
  const text = (key: string): string => {
    const entry = field(value, key);
    return typeof entry === 'string' ? entry : '';
  };
  return {
    authorName: text('authorName'),
    authorURL: text('authorURL'),
    licenseName: text('licenseName'),
    licenseURL: text('licenseURL'),
    sourceProvider: text('sourceProvider'),
  };
}

/** The `total` of POST /api/v2/search, or 0 when the answer has none. */
export function searchTotal(value: unknown): number {
  return isPlainObject(value) && isCount(value.total) ? value.total : 0;
}
