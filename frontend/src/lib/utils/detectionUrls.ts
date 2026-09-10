/**
 * Detection URL builders for the dashboard.
 *
 * These functions wrap buildAppUrl so every anchor the dashboard emits carries
 * the current basepath (handles YAML webserver.basepath, X-Forwarded-Prefix, and
 * Home Assistant X-Ingress-Path). Extracted from DailySummaryCard.svelte which
 * previously built these URLs inline without the basepath wrap, causing
 * hourly-grid and species-row anchors to 404 through reverse proxies when the
 * DB had detection rows (Forgejo #446).
 */

import { buildAppUrl } from './urlHelpers';

/**
 * Builds a detection-list URL filtered to one hour (or a range) on the given date.
 * Used by the dashboard hourly-grid cells at one, two, and six hour groupings.
 */
export function buildHourlyDetectionUrl(
  date: string,
  hour: number,
  durationHours: number,
  numResults?: number,
  offset?: number
): string {
  const params = new URLSearchParams({
    queryType: 'hourly',
    date,
    hour: hour.toString(),
    duration: durationHours.toString(),
  });
  if (numResults !== undefined) params.set('numResults', numResults.toString());
  if (offset !== undefined) params.set('offset', offset.toString());
  return buildAppUrl(`/ui/detections?${params.toString()}`);
}

/**
 * Builds a detection-list URL filtered to all detections of a species on a date.
 * Used by the dashboard species-row anchors.
 */
export function buildSpeciesDetectionUrl(
  scientificName: string,
  date: string,
  numResults?: number,
  offset?: number
): string {
  const params = new URLSearchParams({
    queryType: 'species',
    species: scientificName,
    date,
  });
  if (numResults !== undefined) params.set('numResults', numResults.toString());
  if (offset !== undefined) params.set('offset', offset.toString());
  return buildAppUrl(`/ui/detections?${params.toString()}`);
}

/**
 * Builds a detection-list URL filtered to a species within a specific hour window.
 * Used by the dashboard per-species hourly cells.
 */
/**
 * Internal path for a species detections search (no proxy/basepath prefix).
 * Use with navigation.navigate(); buildSpeciesSearchUrl wraps this for hrefs.
 */
export function buildSpeciesSearchPath(searchTerm: string): string {
  const params = new URLSearchParams({ search: searchTerm });
  return `/ui/detections?${params.toString()}`;
}

/**
 * Builds a detection-list URL that searches for a species across all dates.
 * Used by analytics species cards/rows (search mode omits the date default on
 * the detections page). The search term should be the visitor-localized common
 * name when available so it matches what the user sees in the UI.
 */
export function buildSpeciesSearchUrl(searchTerm: string): string {
  return buildAppUrl(buildSpeciesSearchPath(searchTerm));
}

export function buildSpeciesHourUrl(
  scientificName: string,
  date: string,
  hour: number,
  durationHours: number,
  numResults?: number,
  offset?: number
): string {
  const params = new URLSearchParams({
    queryType: 'species',
    species: scientificName,
    date,
    hour: hour.toString(),
    duration: durationHours.toString(),
  });
  if (numResults !== undefined) params.set('numResults', numResults.toString());
  if (offset !== undefined) params.set('offset', offset.toString());
  return buildAppUrl(`/ui/detections?${params.toString()}`);
}
