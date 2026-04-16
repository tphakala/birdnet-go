import { describe, it, expect, beforeEach, vi } from 'vitest';
import {
  buildHourlyDetectionUrl,
  buildSpeciesDetectionUrl,
  buildSpeciesHourUrl,
} from '../detectionUrls';
import { resetBasePath, setBasePath } from '../urlHelpers';

// Shared test fixtures so a change to the date, basepath, or species value
// only needs to happen in one place.
const TEST_DATE = '2026-04-15';
const TEST_BASE_PATH = '/birdnet';
const TEST_SPECIES = 'Turdus merula';

describe('detectionUrls', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    resetBasePath();
  });

  describe('buildHourlyDetectionUrl', () => {
    it('returns unprefixed URL when no basepath is set', () => {
      const url = buildHourlyDetectionUrl(TEST_DATE, 7, 1);
      expect(url).toBe(`/ui/detections?queryType=hourly&date=${TEST_DATE}&hour=7&duration=1`);
    });

    it(`prepends ${TEST_BASE_PATH} when setBasePath(${TEST_BASE_PATH}) is active`, () => {
      setBasePath(TEST_BASE_PATH);
      const url = buildHourlyDetectionUrl(TEST_DATE, 7, 1);
      expect(url).toBe(
        `${TEST_BASE_PATH}/ui/detections?queryType=hourly&date=${TEST_DATE}&hour=7&duration=1`
      );
    });

    it('prepends Home Assistant ingress basepath', () => {
      setBasePath('/api/hassio_ingress/TOKEN');
      const url = buildHourlyDetectionUrl(TEST_DATE, 7, 1);
      expect(url).toBe(
        `/api/hassio_ingress/TOKEN/ui/detections?queryType=hourly&date=${TEST_DATE}&hour=7&duration=1`
      );
    });

    it('is idempotent when called twice with same args', () => {
      setBasePath(TEST_BASE_PATH);
      const a = buildHourlyDetectionUrl(TEST_DATE, 7, 1);
      const b = buildHourlyDetectionUrl(TEST_DATE, 7, 1);
      expect(a).toBe(b);
    });

    it('includes numResults and offset when provided', () => {
      const url = buildHourlyDetectionUrl(TEST_DATE, 7, 1, 50, 0);
      expect(url).toBe(
        `/ui/detections?queryType=hourly&date=${TEST_DATE}&hour=7&duration=1&numResults=50&offset=0`
      );
    });
  });

  describe('buildSpeciesDetectionUrl', () => {
    it('returns unprefixed URL with URL-encoded species name', () => {
      const url = buildSpeciesDetectionUrl(TEST_SPECIES, TEST_DATE);
      expect(url).toBe(`/ui/detections?queryType=species&species=Turdus+merula&date=${TEST_DATE}`);
    });

    it('prepends basepath when set', () => {
      setBasePath(TEST_BASE_PATH);
      const url = buildSpeciesDetectionUrl(TEST_SPECIES, TEST_DATE);
      expect(url).toBe(
        `${TEST_BASE_PATH}/ui/detections?queryType=species&species=Turdus+merula&date=${TEST_DATE}`
      );
    });

    it('includes numResults and offset when provided', () => {
      const url = buildSpeciesDetectionUrl(TEST_SPECIES, TEST_DATE, 50, 10);
      expect(url).toBe(
        `/ui/detections?queryType=species&species=Turdus+merula&date=${TEST_DATE}&numResults=50&offset=10`
      );
    });
  });

  describe('buildSpeciesHourUrl', () => {
    it('returns unprefixed URL with hour and duration', () => {
      const url = buildSpeciesHourUrl(TEST_SPECIES, TEST_DATE, 7, 2);
      expect(url).toBe(
        `/ui/detections?queryType=species&species=Turdus+merula&date=${TEST_DATE}&hour=7&duration=2`
      );
    });

    it('prepends basepath when set', () => {
      setBasePath(TEST_BASE_PATH);
      const url = buildSpeciesHourUrl(TEST_SPECIES, TEST_DATE, 7, 2);
      expect(url).toBe(
        `${TEST_BASE_PATH}/ui/detections?queryType=species&species=Turdus+merula&date=${TEST_DATE}&hour=7&duration=2`
      );
    });

    it('encodes species names with non-ASCII characters', () => {
      const url = buildSpeciesHourUrl('Pöllö lajinimi', TEST_DATE, 7, 1);
      expect(url).toBe(
        `/ui/detections?queryType=species&species=P%C3%B6ll%C3%B6+lajinimi&date=${TEST_DATE}&hour=7&duration=1`
      );
    });
  });
});
