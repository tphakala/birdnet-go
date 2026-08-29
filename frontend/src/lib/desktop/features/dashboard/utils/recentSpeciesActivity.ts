import type { RecentSpeciesActivity } from '$lib/types/detection.types';
import { isPlainObject } from '$lib/utils/security';

export function isRecentSpeciesActivity(value: unknown): value is RecentSpeciesActivity {
  if (!isPlainObject(value)) return false;
  const item = value as Record<string, unknown>;
  return (
    typeof item.scientific_name === 'string' &&
    typeof item.common_name === 'string' &&
    (item.species_code === undefined || typeof item.species_code === 'string') &&
    typeof item.count === 'number' &&
    typeof item.latest_heard_at === 'string' &&
    typeof item.latest_confidence === 'number' &&
    typeof item.max_confidence === 'number' &&
    typeof item.avg_confidence === 'number' &&
    Array.isArray(item.confidence_trend) &&
    item.confidence_trend.every(confidence => typeof confidence === 'number') &&
    typeof item.trend_start === 'string' &&
    typeof item.trend_hours === 'number' &&
    typeof item.score === 'number' &&
    typeof item.latest_detection_id === 'number' &&
    (item.thumbnail_url === undefined || typeof item.thumbnail_url === 'string')
  );
}

export function validRecentSpeciesActivities(value: unknown): RecentSpeciesActivity[] {
  return Array.isArray(value) ? value.filter(isRecentSpeciesActivity) : [];
}
