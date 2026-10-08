/**
 * Place search
 *
 * Looks a place name up on Photon (https://photon.komoot.io), a geocoder over
 * OpenStreetMap data, straight from the browser.
 *
 * Privacy: the request carries only the query text and, for German, English
 * and French, the interface language. It never carries coordinates, cookies,
 * a referrer or the CSRF token, which is why it uses plain `fetch` and not
 * `api` or `fetchWithCSRF`. The browser still sends an `Origin` header and the
 * user's public IP address, which the search UI does not mention.
 */

/** Photon search endpoint. */
export const PHOTON_SEARCH_URL = 'https://photon.komoot.io/api/';
/** Photon home page, linked from the search disclosure. */
export const PHOTON_SITE_URL = 'https://photon.komoot.io/';
/**
 * Languages Photon accepts in `lang`. Any other value is answered with a 400,
 * so other interface languages send no `lang` and get local place names.
 */
export const PHOTON_SUPPORTED_LANGUAGES = ['de', 'en', 'fr'] as const;
/** Shortest query that is searched while typing; Enter searches any non-empty query. */
export const PLACE_SEARCH_MIN_AUTOCOMPLETE_LENGTH = 3;
/** Most results requested and shown. */
export const PLACE_SEARCH_RESULT_LIMIT = 5;
/** Pause after the last keystroke before an autocomplete search starts. */
export const PLACE_SEARCH_DEBOUNCE_MS = 400;
/** A search that has not answered after this long is reported as a timeout. */
export const PLACE_SEARCH_TIMEOUT_MS = 8000;

const HTTP_TOO_MANY_REQUESTS = 429;
const LATITUDE_LIMIT = 90;
const LONGITUDE_LIMIT = 180;

/** Kinds of place that get a label, so places with the same name can be told apart. */
export type PlaceKind = 'city' | 'town' | 'village' | 'station' | 'airport';

/** One place found by a search. */
export interface PlaceResult {
  /** Stable identity: OpenStreetMap element type and id. */
  id: string;
  name: string;
  /** City, state and country parts that differ from the name, or an empty string. */
  detail: string;
  /** Present only for the kinds in `PlaceKind`. */
  kind?: PlaceKind;
  latitude: number;
  longitude: number;
}

/** Why a search failed. */
export type PlaceSearchFailure = 'network' | 'rateLimited' | 'unavailable' | 'timeout' | 'invalid';

/** Result of one search. `aborted` means the caller cancelled it. */
export type PlaceSearchOutcome =
  | { status: 'ok'; results: PlaceResult[] }
  | { status: 'error'; reason: PlaceSearchFailure }
  | { status: 'aborted' };

/** Options for `searchPlaces`. */
export interface PlaceSearchOptions {
  /** UI locale, used to pick the result language. */
  locale: string;
  /** Cancels the search; the outcome is then `aborted`. */
  signal?: AbortSignal;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isLanguageSupported(
  language: string
): language is (typeof PHOTON_SUPPORTED_LANGUAGES)[number] {
  return PHOTON_SUPPORTED_LANGUAGES.some(supported => supported === language);
}

/**
 * The `lang` value to send for a UI locale, or `undefined` when Photon does not
 * support the locale's language (the parameter is then left out).
 */
export function photonLanguage(locale: string): string | undefined {
  const language = locale.split(/[-_]/)[0]?.toLowerCase() ?? '';
  return isLanguageSupported(language) ? language : undefined;
}

function readString(properties: Record<string, unknown>, key: string): string | undefined {
  const value = Object.entries(properties).find(([name]) => name === key)?.[1];
  return typeof value === 'string' && value.trim() !== '' ? value.trim() : undefined;
}

/** OpenStreetMap key and value pairs that map to a `PlaceKind`. */
const PLACE_KINDS: readonly { key: string; value: string; kind: PlaceKind }[] = [
  { key: 'place', value: 'city', kind: 'city' },
  { key: 'place', value: 'town', kind: 'town' },
  { key: 'place', value: 'village', kind: 'village' },
  { key: 'railway', value: 'station', kind: 'station' },
  { key: 'aeroway', value: 'aerodrome', kind: 'airport' },
];

function readKind(properties: Record<string, unknown>): PlaceKind | undefined {
  const key = readString(properties, 'osm_key');
  const value = readString(properties, 'osm_value');
  return PLACE_KINDS.find(entry => entry.key === key && entry.value === value)?.kind;
}

function readIdentity(properties: Record<string, unknown>): string | undefined {
  const type = readString(properties, 'osm_type');
  const id = Object.entries(properties).find(([name]) => name === 'osm_id')?.[1];
  if (type === undefined || typeof id !== 'number' || !Number.isFinite(id)) return undefined;
  return `${type}:${id}`;
}

/** Photon GeoJSON coordinates are `[longitude, latitude]`. */
function readCoordinates(geometry: unknown): { latitude: number; longitude: number } | undefined {
  if (!isRecord(geometry)) return undefined;
  const coordinates = geometry['coordinates'];
  if (!Array.isArray(coordinates) || coordinates.length < 2) return undefined;
  const longitude: unknown = coordinates[0];
  const latitude: unknown = coordinates[1];
  if (typeof longitude !== 'number' || typeof latitude !== 'number') return undefined;
  if (!Number.isFinite(longitude) || !Number.isFinite(latitude)) return undefined;
  if (Math.abs(latitude) > LATITUDE_LIMIT || Math.abs(longitude) > LONGITUDE_LIMIT) {
    return undefined;
  }
  return { latitude, longitude };
}

function parseFeature(feature: unknown): PlaceResult | undefined {
  if (!isRecord(feature)) return undefined;
  const properties = feature['properties'];
  if (!isRecord(properties)) return undefined;

  const id = readIdentity(properties);
  const coordinates = readCoordinates(feature['geometry']);
  if (id === undefined || coordinates === undefined) return undefined;

  const city = readString(properties, 'city');
  const name = readString(properties, 'name') ?? readString(properties, 'street') ?? city;
  if (name === undefined) return undefined;

  const detailParts: string[] = [];
  for (const part of [city, readString(properties, 'state'), readString(properties, 'country')]) {
    if (part !== undefined && part !== name && !detailParts.includes(part)) {
      detailParts.push(part);
    }
  }

  const kind = readKind(properties);
  return { id, name, detail: detailParts.join(', '), ...(kind && { kind }), ...coordinates };
}

function parseResults(body: unknown): PlaceResult[] | undefined {
  if (!isRecord(body)) return undefined;
  const features = body['features'];
  if (!Array.isArray(features)) return undefined;

  const results: PlaceResult[] = [];
  const seen = new Set<string>();
  for (const feature of features as unknown[]) {
    const result = parseFeature(feature);
    if (result === undefined || seen.has(result.id)) continue;
    seen.add(result.id);
    results.push(result);
  }
  return results;
}

function buildSearchUrl(query: string, locale: string): string {
  // Built from an allowlist: nothing but these parameters ever goes out.
  const params = new URLSearchParams({ q: query, limit: String(PLACE_SEARCH_RESULT_LIMIT) });
  const lang = photonLanguage(locale);
  if (lang !== undefined) params.set('lang', lang);
  return `${PHOTON_SEARCH_URL}?${params.toString()}`;
}

/** Search Photon for places matching `query`. Never throws. */
export async function searchPlaces(
  query: string,
  options: PlaceSearchOptions
): Promise<PlaceSearchOutcome> {
  const { locale, signal } = options;
  // A function, so each check reads the live flag instead of a narrowed value.
  const callerAborted = () => signal?.aborted === true;
  if (callerAborted()) return { status: 'aborted' };
  // Photon rejects an empty query, so there is nothing to ask.
  if (query.trim() === '') return { status: 'ok', results: [] };

  // An own controller serves the timeout without AbortSignal.any/timeout.
  const controller = new AbortController();
  let timedOut = false;
  const hasTimedOut = () => timedOut;
  const timer = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, PLACE_SEARCH_TIMEOUT_MS);
  const forwardAbort = () => controller.abort();
  signal?.addEventListener('abort', forwardAbort);

  try {
    const response = await fetch(buildSearchUrl(query.trim(), locale), {
      method: 'GET',
      credentials: 'omit',
      referrerPolicy: 'no-referrer',
      signal: controller.signal,
    });
    if (callerAborted()) return { status: 'aborted' };
    if (response.status === HTTP_TOO_MANY_REQUESTS) {
      return { status: 'error', reason: 'rateLimited' };
    }
    if (!response.ok) return { status: 'error', reason: 'unavailable' };

    let body: unknown;
    try {
      body = await response.json();
    } catch {
      // A stream cut by an abort or the timeout is not a service fault.
      if (callerAborted()) return { status: 'aborted' };
      if (hasTimedOut()) return { status: 'error', reason: 'timeout' };
      return { status: 'error', reason: 'invalid' };
    }
    if (callerAborted()) return { status: 'aborted' };

    const results = parseResults(body);
    return results === undefined
      ? { status: 'error', reason: 'invalid' }
      : { status: 'ok', results };
  } catch {
    if (callerAborted()) return { status: 'aborted' };
    if (hasTimedOut()) return { status: 'error', reason: 'timeout' };
    return { status: 'error', reason: 'network' };
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener('abort', forwardAbort);
  }
}
