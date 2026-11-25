/**
 * Banner descriptor for the range filter health shown on the Species page.
 *
 * Mirrors the order and severity of the backend range_filter health check
 * (internal/health/checks/range_filter_check.go): a loaded model that needs the
 * BirdNET Geomodel without it is never healthy, whether or not a location is set.
 */

export type GeomodelIssue = 'none' | 'not_installed' | 'not_selected' | 'load_failed';

/** The subset of GET /api/v2/range/status the banner reads. */
export interface RangeFilterStatusSummary {
  participantsLoaded: boolean;
  active: boolean;
  fellBack: boolean;
  mappedSpecies: number;
  locationConfigured: boolean;
  geomodel: { version: string } | null;
  geomodelIssue: GeomodelIssue;
  reloadFailed: boolean;
  classifiers: { name: string; coveredByBackend: boolean }[];
}

export type RangeFilterBannerKey =
  | 'geomodelNotInstalled'
  | 'geomodelNotSelected'
  | 'geomodelLoadFailed'
  | 'reloadFailed'
  | 'inactive'
  | 'fellBack'
  | 'mappedZero';

export interface RangeFilterBanner {
  level: 'error' | 'warning';
  key: RangeFilterBannerKey;
  /**
   * Names of the loaded models the geomodel does not serve, comma separated. Set only for
   * the geomodel issue banners, whose texts name them; empty for the others.
   */
  models: string;
  /** Whether the banner links to the Models tab (install or reinstall guidance). */
  showModelsLink: boolean;
}

const ISSUE_KEYS: Record<Exclude<GeomodelIssue, 'none'>, RangeFilterBannerKey> = {
  not_installed: 'geomodelNotInstalled',
  not_selected: 'geomodelNotSelected',
  load_failed: 'geomodelLoadFailed',
};

/** Returns the banner to show, or null when the range filter is healthy or not applicable. */
export function rangeFilterBanner(
  status: RangeFilterStatusSummary | null
): RangeFilterBanner | null {
  // No loaded model participates in range filtering (for example a bat-only install):
  // not applicable, as in the health check. A geomodel issue implies a participant.
  if (!status?.participantsLoaded) return null;

  if (status.geomodelIssue !== 'none') {
    return {
      level: 'warning',
      key: ISSUE_KEYS[status.geomodelIssue],
      models: status.classifiers
        .filter(c => !c.coveredByBackend)
        .map(c => c.name)
        .join(', '),
      showModelsLink: status.geomodelIssue !== 'not_selected',
    };
  }
  const models = '';
  if (status.reloadFailed && status.active) {
    return { level: 'warning', key: 'reloadFailed', models, showModelsLink: false };
  }
  if (!status.locationConfigured) return null;
  if (!status.active) return { level: 'error', key: 'inactive', models, showModelsLink: false };
  if (status.fellBack) return { level: 'warning', key: 'fellBack', models, showModelsLink: false };
  if (status.geomodel != null && status.mappedSpecies === 0) {
    return { level: 'warning', key: 'mappedZero', models, showModelsLink: false };
  }
  return null;
}
