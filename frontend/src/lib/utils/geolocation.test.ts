import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  COORDINATE_DECIMAL_PLACES,
  GEOLOCATION_TIMEOUT_MS,
  getBrowserLocationSupport,
  requestBrowserLocation,
  roundCoordinate,
  type BrowserLocationResult,
} from './geolocation';
import {
  clearGeolocationGlobals,
  createPosition,
  createPositionError,
  setGeolocation,
  setSecureContext,
} from '../../test/geolocation-fixtures';

const geolocationMock = {
  getCurrentPosition: vi.fn<Geolocation['getCurrentPosition']>(),
  watchPosition: vi.fn<Geolocation['watchPosition']>(),
  clearWatch: vi.fn<Geolocation['clearWatch']>(),
};

/** Run a request and return the single result it reported. */
function collect(): { results: BrowserLocationResult[]; request: () => void } {
  const results: BrowserLocationResult[] = [];
  return { results, request: () => requestBrowserLocation(result => results.push(result)) };
}

/** Deterministic PRNG (mulberry32) so the equivalence test is reproducible. */
function createPrng(seed: number): () => number {
  let state = seed;
  return () => {
    state = (state + 0x6d2b79f5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

describe('roundCoordinate', () => {
  it('rounds to three decimals', () => {
    expect(COORDINATE_DECIMAL_PLACES).toBe(3);
    expect(roundCoordinate(52.1236)).toBe(52.124);
    expect(roundCoordinate(4.9876)).toBe(4.988);
    expect(roundCoordinate(-33.86785)).toBe(-33.868);
    // 1.0005 is stored just below the half point in binary, so it rounds down.
    expect(roundCoordinate(1.0005)).toBe(1);
    expect(roundCoordinate(60)).toBe(60);
  });

  it('keeps the sign of a negative value that rounds to zero', () => {
    expect(Object.is(roundCoordinate(-0.0004), -0)).toBe(true);
    expect(Object.is(roundCoordinate(0.0004), 0)).toBe(true);
  });

  it('matches the previous parseFloat(toFixed(3)) spelling', () => {
    const random = createPrng(1820);
    const values = [
      0, -0, 90, -90, 180, -180, 0.0005, -0.0005, 0.0015, 179.9995, -179.9995, 1e-7, 123456.789,
    ];
    for (let i = 0; i < 10_000; i++) {
      values.push((random() * 2 - 1) * 180);
    }

    for (const value of values) {
      expect(Object.is(roundCoordinate(value), parseFloat(value.toFixed(3)))).toBe(true);
    }
    expect(Object.is(roundCoordinate(Number.NaN), parseFloat(Number.NaN.toFixed(3)))).toBe(true);
  });
});

describe('getBrowserLocationSupport', () => {
  beforeEach(() => {
    setSecureContext(true);
    setGeolocation(geolocationMock);
  });

  afterEach(() => {
    clearGeolocationGlobals();
  });

  it('is available on a secure origin with the API', () => {
    expect(getBrowserLocationSupport()).toBe('available');
  });

  it('reports an insecure origin before a missing API', () => {
    setSecureContext(false);
    setGeolocation(undefined);
    expect(getBrowserLocationSupport()).toBe('insecure');
  });

  it('reports a missing API on a secure origin', () => {
    setGeolocation(undefined);
    expect(getBrowserLocationSupport()).toBe('unsupported');
  });
});

describe('requestBrowserLocation', () => {
  beforeEach(() => {
    geolocationMock.getCurrentPosition.mockReset();
    setSecureContext(true);
    setGeolocation(geolocationMock);
  });

  afterEach(() => {
    clearGeolocationGlobals();
  });

  it('reports an insecure origin synchronously without calling the API', () => {
    setSecureContext(false);
    setGeolocation(undefined);
    const { results, request } = collect();

    request();

    expect(results).toEqual([{ status: 'insecure' }]);
    expect(geolocationMock.getCurrentPosition).not.toHaveBeenCalled();
  });

  it('reports a missing API synchronously', () => {
    setGeolocation(undefined);
    const { results, request } = collect();

    request();

    expect(results).toEqual([{ status: 'unsupported' }]);
  });

  it('rounds the position and adds rounding displacement to accuracy', () => {
    geolocationMock.getCurrentPosition.mockImplementationOnce(success => {
      success(createPosition(52.1236, 4.9876, 7.6));
    });
    const { results, request } = collect();

    request();

    expect(results).toEqual([
      { status: 'success', latitude: 52.124, longitude: 4.988, accuracyMeters: 60 },
    ]);
  });

  it('reports a null accuracy when the browser accuracy is not finite', () => {
    geolocationMock.getCurrentPosition.mockImplementationOnce(success => {
      success(createPosition(52.1, 4.3, Number.POSITIVE_INFINITY));
    });
    const { results, request } = collect();

    request();

    expect(results).toEqual([
      { status: 'success', latitude: 52.1, longitude: 4.3, accuracyMeters: null },
    ]);
  });

  it.each([
    ['NaN latitude', Number.NaN, 4.9],
    ['NaN longitude', 52.1, Number.NaN],
    ['latitude above the maximum', 91, 4.9],
    ['latitude below the minimum', -91, 4.9],
    ['longitude above the maximum', 52.1, 181],
    ['longitude below the minimum', 52.1, -181],
  ] as const)('rejects out-of-range coordinates as invalid (%s)', (_label, latitude, longitude) => {
    geolocationMock.getCurrentPosition.mockImplementationOnce(success => {
      success(createPosition(latitude, longitude, 10));
    });
    const { results, request } = collect();

    request();

    expect(results).toHaveLength(1);
    expect(results[0]?.status).toBe('invalid');
  });

  it.each([
    [1, 'denied'],
    [2, 'unavailable'],
    [3, 'timeout'],
    [99, 'failed'],
  ] as const)('maps geolocation error code %s to %s', (code, status) => {
    const error = createPositionError(code);
    geolocationMock.getCurrentPosition.mockImplementationOnce((_success, failure) => {
      failure?.(error);
    });
    const { results, request } = collect();

    request();

    expect(results).toEqual([{ status, error }]);
  });

  it('reports a synchronous throw as failed', () => {
    const thrown = new DOMException('Blocked by policy', 'SecurityError');
    geolocationMock.getCurrentPosition.mockImplementationOnce(() => {
      throw thrown;
    });
    const { results, request } = collect();

    request();

    expect(results).toEqual([{ status: 'failed', error: thrown }]);
  });

  it('reports only the first of two answers from the browser', () => {
    geolocationMock.getCurrentPosition.mockImplementationOnce((success, failure) => {
      success(createPosition(52.1, 4.3, 10));
      failure?.(createPositionError(1));
    });
    const { results, request } = collect();

    request();

    expect(results).toHaveLength(1);
    expect(results[0]?.status).toBe('success');
  });

  it('reports once and lets a throwing result handler propagate', () => {
    geolocationMock.getCurrentPosition.mockImplementationOnce(success => {
      success(createPosition(52.1, 4.3, 10));
    });
    const onResult = vi.fn(() => {
      throw new Error('handler failed');
    });

    expect(() => requestBrowserLocation(onResult)).toThrow('handler failed');

    expect(onResult).toHaveBeenCalledTimes(1);
  });

  it('passes enableHighAccuracy, a 10 s timeout and maximumAge 0', () => {
    geolocationMock.getCurrentPosition.mockImplementationOnce(() => undefined);
    const { request } = collect();

    request();

    expect(GEOLOCATION_TIMEOUT_MS).toBe(10_000);
    expect(geolocationMock.getCurrentPosition).toHaveBeenCalledWith(
      expect.any(Function),
      expect.any(Function),
      { enableHighAccuracy: true, timeout: 10_000, maximumAge: 0 }
    );
    expect(geolocationMock.watchPosition).not.toHaveBeenCalled();
  });
});
