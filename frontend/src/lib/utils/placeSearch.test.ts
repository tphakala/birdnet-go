import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  PHOTON_SEARCH_URL,
  PLACE_SEARCH_RESULT_LIMIT,
  PLACE_SEARCH_TIMEOUT_MS,
  photonLanguage,
  searchPlaces,
} from './placeSearch';

const originalFetch = globalThis.fetch;

// Shape of a Photon feature, as probed on photon.komoot.io.
const HELSINKI_FEATURE = {
  type: 'Feature',
  properties: {
    osm_type: 'R',
    osm_id: 34914,
    name: 'Helsinki',
    state: 'Uusimaa',
    country: 'Finland',
  },
  geometry: { type: 'Point', coordinates: [24.9435408, 60.1666204] },
};

function featureCollection(...features: unknown[]) {
  return { type: 'FeatureCollection', features };
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

let fetchMock: ReturnType<typeof vi.fn<typeof fetch>>;

function respondWith(body: unknown, status = 200) {
  fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(body, status)));
}

function lastRequest(): { url: URL; init: RequestInit } {
  const call = fetchMock.mock.calls.at(-1);
  if (!call) throw new Error('fetch was not called');
  const [input, init] = call;
  return { url: new URL(String(input)), init: init ?? {} };
}

/** A fetch that never answers on its own but rejects when its signal aborts. */
function hangUntilAborted() {
  fetchMock.mockImplementation((_input, init) => {
    return new Promise<Response>((_resolve, reject) => {
      init?.signal?.addEventListener('abort', () => {
        reject(new DOMException('Aborted', 'AbortError'));
      });
    });
  });
}

describe('placeSearch', () => {
  beforeEach(() => {
    fetchMock = vi.fn<typeof fetch>();
    globalThis.fetch = fetchMock;
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.useRealTimers();
  });

  describe('photonLanguage', () => {
    it('returns the language for the three languages Photon supports', () => {
      expect(photonLanguage('de')).toBe('de');
      expect(photonLanguage('en')).toBe('en');
      expect(photonLanguage('fr')).toBe('fr');
    });

    it('returns undefined for languages Photon rejects with a 400', () => {
      expect(photonLanguage('fi')).toBeUndefined();
      expect(photonLanguage('sv')).toBeUndefined();
      expect(photonLanguage('')).toBeUndefined();
    });

    it('matches the base language of a regional locale', () => {
      expect(photonLanguage('de-AT')).toBe('de');
      expect(photonLanguage('FR_ca')).toBe('fr');
    });
  });

  describe('request', () => {
    it('builds a query with q and limit only for an unsupported locale', async () => {
      respondWith(featureCollection());
      await searchPlaces('Helsinki', { locale: 'fi' });

      const { url } = lastRequest();
      expect(`${url.origin}${url.pathname}`).toBe(PHOTON_SEARCH_URL);
      expect([...url.searchParams.keys()].sort()).toEqual(['limit', 'q']);
      expect(url.searchParams.get('q')).toBe('Helsinki');
      expect(url.searchParams.get('limit')).toBe(String(PLACE_SEARCH_RESULT_LIMIT));
    });

    it('adds lang for de, en and fr', async () => {
      respondWith(featureCollection());
      for (const locale of ['de', 'en', 'fr']) {
        await searchPlaces('Helsinki', { locale });
        expect(lastRequest().url.searchParams.get('lang')).toBe(locale);
      }
    });

    it('answers a blank query without a request', async () => {
      for (const query of ['', '   ']) {
        expect(await searchPlaces(query, { locale: 'en' })).toEqual({ status: 'ok', results: [] });
      }
      expect(fetchMock).not.toHaveBeenCalled();
    });

    it('trims the query', async () => {
      respondWith(featureCollection());
      await searchPlaces('  Helsinki  ', { locale: 'en' });
      expect(lastRequest().url.searchParams.get('q')).toBe('Helsinki');
    });

    it('never sends coordinates', async () => {
      respondWith(featureCollection());
      for (const locale of ['de', 'fi']) {
        await searchPlaces('Helsinki', { locale });
        const params = lastRequest().url.searchParams;
        expect(params.has('lat')).toBe(false);
        expect(params.has('lon')).toBe(false);
        expect(params.has('location_bias_scale')).toBe(false);
      }
    });

    it('sends no credentials, no referrer and no custom headers', async () => {
      respondWith(featureCollection());
      await searchPlaces('Helsinki', { locale: 'en' });

      const { init } = lastRequest();
      expect(init.method).toBe('GET');
      expect(init.credentials).toBe('omit');
      expect(init.referrerPolicy).toBe('no-referrer');
      expect(init.headers).toBeUndefined();
    });
  });

  describe('results', () => {
    it('reads Photon coordinates as longitude, latitude', async () => {
      respondWith(featureCollection(HELSINKI_FEATURE));
      const outcome = await searchPlaces('Helsinki', { locale: 'en' });

      expect(outcome).toEqual({
        status: 'ok',
        results: [
          {
            id: 'R:34914',
            name: 'Helsinki',
            detail: 'Uusimaa, Finland',
            latitude: 60.1666204,
            longitude: 24.9435408,
          },
        ],
      });
    });

    it('leaves out detail parts that repeat the name', async () => {
      respondWith(
        featureCollection({
          ...HELSINKI_FEATURE,
          properties: { ...HELSINKI_FEATURE.properties, city: 'Helsinki' },
        })
      );
      const outcome = await searchPlaces('Helsinki', { locale: 'en' });

      expect(outcome.status === 'ok' && outcome.results[0]?.detail).toBe('Uusimaa, Finland');
    });

    it('skips features with missing or out-of-range coordinates or ids', async () => {
      const valid = { ...HELSINKI_FEATURE, properties: { ...HELSINKI_FEATURE.properties } };
      respondWith(
        featureCollection(
          valid,
          { ...valid, geometry: { type: 'Point', coordinates: [24.9] } },
          { ...valid, geometry: { type: 'Point', coordinates: ['24.9', '60.1'] } },
          { ...valid, geometry: { type: 'Point', coordinates: [24.9, 91] } },
          { ...valid, geometry: { type: 'Point', coordinates: [181, 60] } },
          { ...valid, geometry: null },
          { ...valid, properties: { name: 'No id' } },
          { ...valid, properties: { ...valid.properties, osm_id: undefined, osm_type: 'N' } },
          { ...valid, properties: { ...valid.properties, osm_id: 7, name: undefined } },
          'not a feature',
          null
        )
      );
      const outcome = await searchPlaces('Helsinki', { locale: 'en' });

      expect(outcome.status).toBe('ok');
      expect(outcome.status === 'ok' && outcome.results.map(result => result.id)).toEqual([
        'R:34914',
      ]);
    });

    it('drops duplicate results', async () => {
      respondWith(featureCollection(HELSINKI_FEATURE, HELSINKI_FEATURE));
      const outcome = await searchPlaces('Helsinki', { locale: 'en' });

      expect(outcome.status === 'ok' && outcome.results).toHaveLength(1);
    });

    it('returns an empty list for a query nothing matches', async () => {
      respondWith(featureCollection());
      expect(await searchPlaces('zzzzzz', { locale: 'en' })).toEqual({
        status: 'ok',
        results: [],
      });
    });
  });

  describe('failures', () => {
    it('reports 429 as rate limited', async () => {
      respondWith({}, 429);
      expect(await searchPlaces('Helsinki', { locale: 'en' })).toEqual({
        status: 'error',
        reason: 'rateLimited',
      });
    });

    it('reports other HTTP errors as unavailable', async () => {
      for (const status of [400, 500, 503]) {
        respondWith({}, status);
        expect(await searchPlaces('Helsinki', { locale: 'en' })).toEqual({
          status: 'error',
          reason: 'unavailable',
        });
      }
    });

    it('reports a network failure as network', async () => {
      fetchMock.mockRejectedValue(new TypeError('Failed to fetch'));
      expect(await searchPlaces('Helsinki', { locale: 'en' })).toEqual({
        status: 'error',
        reason: 'network',
      });
    });

    it('reports a slow response as timeout', async () => {
      vi.useFakeTimers();
      hangUntilAborted();

      const pending = searchPlaces('Helsinki', { locale: 'en' });
      await vi.advanceTimersByTimeAsync(PLACE_SEARCH_TIMEOUT_MS);

      expect(await pending).toEqual({ status: 'error', reason: 'timeout' });
    });

    it('reports a caller abort as aborted', async () => {
      hangUntilAborted();
      const controller = new AbortController();

      const pending = searchPlaces('Helsinki', { locale: 'en', signal: controller.signal });
      controller.abort();

      expect(await pending).toEqual({ status: 'aborted' });
    });

    it('reports a signal that is already aborted without calling fetch', async () => {
      const controller = new AbortController();
      controller.abort();

      expect(await searchPlaces('Helsinki', { locale: 'en', signal: controller.signal })).toEqual({
        status: 'aborted',
      });
      expect(fetchMock).not.toHaveBeenCalled();
    });

    it('reports a response that resolves after the caller aborted as aborted', async () => {
      const controller = new AbortController();
      fetchMock.mockImplementation(() => {
        controller.abort();
        return Promise.resolve(jsonResponse(featureCollection(HELSINKI_FEATURE)));
      });

      expect(await searchPlaces('Helsinki', { locale: 'en', signal: controller.signal })).toEqual({
        status: 'aborted',
      });
    });

    it('reports malformed JSON as invalid', async () => {
      fetchMock.mockResolvedValue(new Response('<html>nope</html>', { status: 200 }));
      expect(await searchPlaces('Helsinki', { locale: 'en' })).toEqual({
        status: 'error',
        reason: 'invalid',
      });
    });

    it('reports JSON of the wrong shape as invalid', async () => {
      for (const body of [[], { features: 'x' }, { type: 'FeatureCollection' }, null]) {
        respondWith(body);
        expect(await searchPlaces('Helsinki', { locale: 'en' })).toEqual({
          status: 'error',
          reason: 'invalid',
        });
      }
    });
  });
});
