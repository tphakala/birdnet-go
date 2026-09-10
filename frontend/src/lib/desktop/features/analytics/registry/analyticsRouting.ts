/**
 * Pure redirect resolution for the analytics routes. Maps legacy ?tab= deep
 * links and the retired /advanced path onto the per-view routes, preserving all
 * other query params and stripping `tab`. Returns null when the URL is already
 * canonical so the caller performs no navigation.
 *
 * Kept pure (no navigation-store import) so it is unit-testable and reusable.
 */
export const ANALYTICS_BASE = '/ui/analytics';

const TAB_TO_SEGMENT: Record<string, string> = {
  overview: 'summary',
  patterns: 'activity',
  trends: 'trends',
  biodiversity: 'biodiversity',
  quality: 'review',
};

/**
 * Strips a single trailing slash from a path while preserving the root "/".
 * Exported so the App.svelte router can canonicalize a trailing slash before its
 * pathToRouteMap lookup (the map keys are slashless), reusing one implementation
 * rather than hand-rolling the guarded slice at each call site.
 */
export function stripTrailingSlash(path: string): string {
  return path.length > 1 && path.endsWith('/') ? path.slice(0, -1) : path;
}

function withQuery(path: string, sp: URLSearchParams): string {
  const qs = sp.toString();
  return qs ? `${path}?${qs}` : path;
}

export function resolveAnalyticsRedirect(pathname: string, search: string): string | null {
  const path = stripTrailingSlash(pathname);
  const sp = new URLSearchParams(search);

  // Retired Advanced Analytics page -> Activity, single hop.
  if (path === `${ANALYTICS_BASE}/advanced`) {
    sp.delete('tab');
    return withQuery(`${ANALYTICS_BASE}/activity`, sp);
  }

  // Bare hub: map a legacy ?tab= to its segment, else default to summary.
  if (path === ANALYTICS_BASE) {
    const tab = sp.get('tab');
    sp.delete('tab');
    const guard = tab && Object.prototype.hasOwnProperty.call(TAB_TO_SEGMENT, tab);
    // eslint-disable-next-line security/detect-object-injection -- tab is guarded by hasOwnProperty against a fixed const map
    const segment = guard ? TAB_TO_SEGMENT[tab] : 'summary';
    return withQuery(`${ANALYTICS_BASE}/${segment}`, sp);
  }

  // Already on a canonical /ui/analytics/<segment> route (or unrelated path).
  return null;
}
