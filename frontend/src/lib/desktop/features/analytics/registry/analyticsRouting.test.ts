import { describe, it, expect } from 'vitest';
import { resolveAnalyticsRedirect, stripTrailingSlash } from './analyticsRouting';

describe('resolveAnalyticsRedirect', () => {
  it('maps legacy ?tab= values to the new routes and strips tab', () => {
    expect(resolveAnalyticsRedirect('/ui/analytics', '?tab=patterns')).toBe(
      '/ui/analytics/activity'
    );
    expect(resolveAnalyticsRedirect('/ui/analytics', '?tab=overview')).toBe(
      '/ui/analytics/summary'
    );
    expect(resolveAnalyticsRedirect('/ui/analytics', '?tab=trends')).toBe('/ui/analytics/trends');
    expect(resolveAnalyticsRedirect('/ui/analytics', '?tab=biodiversity')).toBe(
      '/ui/analytics/biodiversity'
    );
    expect(resolveAnalyticsRedirect('/ui/analytics', '?tab=quality')).toBe('/ui/analytics/review');
  });

  it('sends bare /ui/analytics (and trailing slash) to /summary', () => {
    expect(resolveAnalyticsRedirect('/ui/analytics', '')).toBe('/ui/analytics/summary');
    expect(resolveAnalyticsRedirect('/ui/analytics/', '')).toBe('/ui/analytics/summary');
  });

  it('routes an unknown ?tab= value to summary (unknown-tab fallback)', () => {
    expect(resolveAnalyticsRedirect('/ui/analytics', '?tab=bogus')).toBe('/ui/analytics/summary');
  });

  it('preserves non-tab query params', () => {
    expect(resolveAnalyticsRedirect('/ui/analytics', '?tab=trends&range=week&species=A')).toBe(
      '/ui/analytics/trends?range=week&species=A'
    );
  });

  it('maps the retired /advanced path directly to /activity (single hop)', () => {
    expect(resolveAnalyticsRedirect('/ui/analytics/advanced', '')).toBe('/ui/analytics/activity');
    expect(resolveAnalyticsRedirect('/ui/analytics/advanced/', '?range=year')).toBe(
      '/ui/analytics/activity?range=year'
    );
  });

  it('returns null for canonical routes (no redirect)', () => {
    expect(resolveAnalyticsRedirect('/ui/analytics/summary', '')).toBeNull();
    expect(resolveAnalyticsRedirect('/ui/analytics/activity', '?range=week')).toBeNull();
    expect(resolveAnalyticsRedirect('/ui/dashboard', '')).toBeNull();
  });

  it('treats the coming-soon routes as canonical (no redirect)', () => {
    // nocturnal/weather/soundscape are real per-route pages, not legacy ?tab=
    // aliases. They must never be added to TAB_TO_SEGMENT: a stray mapping would
    // synthesize a bogus redirect off the bare hub. Landing directly on them
    // resolves to null, same as any other canonical segment.
    expect(resolveAnalyticsRedirect('/ui/analytics/nocturnal', '')).toBeNull();
    expect(resolveAnalyticsRedirect('/ui/analytics/weather', '?range=week')).toBeNull();
    expect(resolveAnalyticsRedirect('/ui/analytics/soundscape', '')).toBeNull();

    // The load-bearing guard: a bare-hub ?tab= matching one of these slugs must
    // fall back to summary, NOT redirect to that segment. This is the assertion
    // that fails if a stray TAB_TO_SEGMENT entry is ever added for them (the
    // canonical-route checks above pass regardless of the map).
    expect(resolveAnalyticsRedirect('/ui/analytics', '?tab=nocturnal')).toBe(
      '/ui/analytics/summary'
    );
    expect(resolveAnalyticsRedirect('/ui/analytics', '?tab=weather')).toBe('/ui/analytics/summary');
    expect(resolveAnalyticsRedirect('/ui/analytics', '?tab=soundscape')).toBe(
      '/ui/analytics/summary'
    );
  });
});

describe('stripTrailingSlash', () => {
  it('strips a single trailing slash from a canonical route (the #1278 404 case)', () => {
    expect(stripTrailingSlash('/ui/analytics/nocturnal/')).toBe('/ui/analytics/nocturnal');
    expect(stripTrailingSlash('/ui/search/')).toBe('/ui/search');
    expect(stripTrailingSlash('/ui/settings/audio/')).toBe('/ui/settings/audio');
  });

  it('preserves the root "/" so it never collapses to an empty string', () => {
    // The length > 1 guard is load-bearing: without it "/" would become "" and the
    // dashboard root would 404. This test reddens if that guard is dropped.
    expect(stripTrailingSlash('/')).toBe('/');
  });

  it('reduces the /ui/ root to the slashless key the route map also carries', () => {
    expect(stripTrailingSlash('/ui/')).toBe('/ui');
  });

  it('leaves an already-slashless path unchanged', () => {
    expect(stripTrailingSlash('/ui/analytics/nocturnal')).toBe('/ui/analytics/nocturnal');
    expect(stripTrailingSlash('/ui')).toBe('/ui');
  });

  it('removes only the final slash (repeated trailing slashes are not the reported case)', () => {
    expect(stripTrailingSlash('/ui/analytics/summary//')).toBe('/ui/analytics/summary/');
  });
});
