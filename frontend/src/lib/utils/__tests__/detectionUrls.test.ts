import { describe, it, expect, beforeEach } from 'vitest';
import {
  buildHourlyDetectionUrl,
  buildSpeciesDetectionUrl,
  buildSpeciesHourUrl,
} from '../detectionUrls';
import { resetBasePath, setBasePath } from '../urlHelpers';

describe('detectionUrls', () => {
  beforeEach(() => {
    resetBasePath();
  });

  describe('buildHourlyDetectionUrl', () => {
    it('returns unprefixed URL when no basepath is set', () => {
      const url = buildHourlyDetectionUrl('2026-04-15', 7, 1);
      expect(url).toBe('/ui/detections?queryType=hourly&date=2026-04-15&hour=7&duration=1');
    });

    it('prepends /birdnet when setBasePath("/birdnet") is active', () => {
      setBasePath('/birdnet');
      const url = buildHourlyDetectionUrl('2026-04-15', 7, 1);
      expect(url).toBe('/birdnet/ui/detections?queryType=hourly&date=2026-04-15&hour=7&duration=1');
    });

    it('prepends Home Assistant ingress basepath', () => {
      setBasePath('/api/hassio_ingress/TOKEN');
      const url = buildHourlyDetectionUrl('2026-04-15', 7, 1);
      expect(url).toBe(
        '/api/hassio_ingress/TOKEN/ui/detections?queryType=hourly&date=2026-04-15&hour=7&duration=1'
      );
    });

    it('is idempotent when called twice with same args', () => {
      setBasePath('/birdnet');
      const a = buildHourlyDetectionUrl('2026-04-15', 7, 1);
      const b = buildHourlyDetectionUrl('2026-04-15', 7, 1);
      expect(a).toBe(b);
    });

    it('includes numResults and offset when provided', () => {
      const url = buildHourlyDetectionUrl('2026-04-15', 7, 1, 50, 0);
      expect(url).toBe(
        '/ui/detections?queryType=hourly&date=2026-04-15&hour=7&duration=1&numResults=50&offset=0'
      );
    });
  });

  describe('buildSpeciesDetectionUrl', () => {
    it('returns unprefixed URL with URL-encoded species name', () => {
      const url = buildSpeciesDetectionUrl('Turdus merula', '2026-04-15');
      expect(url).toBe('/ui/detections?queryType=species&species=Turdus+merula&date=2026-04-15');
    });

    it('prepends basepath when set', () => {
      setBasePath('/birdnet');
      const url = buildSpeciesDetectionUrl('Turdus merula', '2026-04-15');
      expect(url).toBe(
        '/birdnet/ui/detections?queryType=species&species=Turdus+merula&date=2026-04-15'
      );
    });

    it('includes numResults and offset when provided', () => {
      const url = buildSpeciesDetectionUrl('Turdus merula', '2026-04-15', 50, 10);
      expect(url).toBe(
        '/ui/detections?queryType=species&species=Turdus+merula&date=2026-04-15&numResults=50&offset=10'
      );
    });
  });

  describe('buildSpeciesHourUrl', () => {
    it('returns unprefixed URL with hour and duration', () => {
      const url = buildSpeciesHourUrl('Turdus merula', '2026-04-15', 7, 2);
      expect(url).toBe(
        '/ui/detections?queryType=species&species=Turdus+merula&date=2026-04-15&hour=7&duration=2'
      );
    });

    it('prepends basepath when set', () => {
      setBasePath('/birdnet');
      const url = buildSpeciesHourUrl('Turdus merula', '2026-04-15', 7, 2);
      expect(url).toBe(
        '/birdnet/ui/detections?queryType=species&species=Turdus+merula&date=2026-04-15&hour=7&duration=2'
      );
    });

    it('encodes species names with non-ASCII characters', () => {
      const url = buildSpeciesHourUrl('Pöllö lajinimi', '2026-04-15', 7, 1);
      expect(url).toBe(
        '/ui/detections?queryType=species&species=P%C3%B6ll%C3%B6+lajinimi&date=2026-04-15&hour=7&duration=1'
      );
    });
  });
});
