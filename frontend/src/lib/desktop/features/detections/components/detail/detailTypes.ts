/**
 * Types for the detection detail page: the extra fields of the single-detection
 * response and the responses of the endpoints the page reads next to it.
 */
import type { Detection } from '$lib/types/detection.types';

/** One model's part in a detection. A hitCount of 0 means the model ran without a hit. */
export interface ModelContribution {
  modelName: string;
  modelVersion: string;
  modelVariant?: string;
  modelType?: string;
  hitCount: number;
  maxConfidence?: number;
  minConfidence?: number;
  meanConfidence?: number;
  firstHitAt?: string;
  lastHitAt?: string;
}

/** The single-detection response, with the fields the list responses do not carry. */
export interface DetectionDetailData extends Detection {
  modelContributions?: ModelContribution[];
  clipDenoised?: boolean;
  /** Time of the first review (RFC3339). */
  reviewedAt?: string;
  /** Time of the latest review change (RFC3339). */
  reviewUpdatedAt?: string;
  /** Time the detection was locked (RFC3339). */
  lockedAt?: string;
}

/** GET /api/v2/detections/:id/neighbors */
export interface DetectionNeighbors {
  detectionId: number;
  previous: number | null;
  next: number | null;
  previousSameSpecies: number | null;
  nextSameSpecies: number | null;
}

/** GET /api/v2/weather/detection/:id */
export interface DetectionWeather {
  daily?: {
    date?: string;
    sunrise?: string;
    sunset?: string;
  };
  hourly?: {
    time?: string;
    temperature?: number;
    feels_like?: number;
    pressure?: number;
    humidity?: number;
    visibility?: number;
    wind_speed?: number;
    wind_deg?: number;
    wind_gust?: number;
    clouds?: number;
    precipitation?: number;
    precipitation_type?: string;
    weather_main?: string;
    weather_desc?: string;
    weather_icon?: string;
  };
  time_of_day?: string;
}

/** GET /api/v2/weather/sun/:date */
export interface SunTimes {
  date: string;
  sunrise?: string;
  sunset?: string;
  civil_dawn?: string;
  civil_dusk?: string;
  timezone?: string;
}

/** One alternate species a model also scored for the clip. */
export interface AlternatePrediction {
  scientificName: string;
  commonName?: string;
  speciesCode?: string;
  confidence: number;
  /** True for the model's own pick, kept on record after a reviewer changed the species. */
  original?: boolean;
}

/** GET /api/v2/detections/:id/predictions */
export interface DetectionPredictions {
  detectionId: number;
  modelPredictions?: {
    modelName: string;
    modelVersion: string;
    modelVariant?: string;
    modelType?: string;
    predictions?: AlternatePrediction[];
  }[];
}

/** One row of GET /api/v2/analytics/species/summary */
export interface SpeciesSummary {
  scientific_name: string;
  common_name?: string;
  count: number;
  first_heard?: string;
  last_heard?: string;
  avg_confidence?: number;
  max_confidence?: number;
}

/** One bucket of GET /api/v2/analytics/time/distribution/hourly */
export interface HourlyBucket {
  hour: number;
  count: number;
}

/** GET /api/v2/analytics/time/daily */
export interface DailyCounts {
  data?: { date: string; count: number }[];
  total?: number;
}

/** How reviewers have judged a species' detections at this station. */
export interface SpeciesReviewCounts {
  correct: number;
  falsePositive: number;
}

/** Rarity block of GET /api/v2/species */
export interface SpeciesRarity {
  status: string;
  score: number;
  location_based: boolean;
  latitude?: number;
  longitude?: number;
  date?: string;
}

/** Taxonomy block of GET /api/v2/species/taxonomy */
export interface TaxonomyHierarchy {
  kingdom?: string;
  phylum?: string;
  class?: string;
  order?: string;
  family?: string;
  family_common?: string;
  genus?: string;
  species?: string;
}
