/**
 * Tests for the false positive filter cadence math.
 *
 * The parity fixture is shared with the Go test TestMinDetections_ParityFixture
 * (internal/analysis/processor/mindetections_test.go), so the estimate shown
 * before saving matches the backend count for the same overlap, level and clip.
 */
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import type { AnalysisCadenceInfo } from '$lib/desktop/features/system/inference.types';
import {
  BAT_MODEL_ID,
  minDetectionsForStep,
  modelStepSeconds,
  overlapSecondsToNs,
  planLagsSettings,
  previewEffectiveOverlapSeconds,
  previewReadout,
  savedReadout,
} from './fpCadence';

interface ParityRow {
  modelId: string;
  clipMs: number;
  overlapSec: number;
  level: number;
  confirmations: number;
}

const FIXTURE_PATH = join(
  dirname(fileURLToPath(import.meta.url)),
  '../../../../../../../internal/analysis/processor/testdata/fp_confirmations.json'
);

function loadFixture(): ParityRow[] {
  const parsed: unknown = JSON.parse(readFileSync(FIXTURE_PATH, 'utf8'));
  if (!Array.isArray(parsed)) throw new Error('fixture is not an array');
  return parsed.filter(
    (r): r is ParityRow =>
      typeof r === 'object' &&
      r !== null &&
      typeof r.modelId === 'string' &&
      typeof r.clipMs === 'number' &&
      typeof r.overlapSec === 'number' &&
      typeof r.level === 'number' &&
      typeof r.confirmations === 'number'
  );
}

function cadence(overrides: Partial<AnalysisCadenceInfo> = {}): AnalysisCadenceInfo {
  return {
    status: 'capped',
    configuredOverlapSec: 2.8,
    effectiveOverlapSec: 1.8,
    minBaseStepMs: 1200,
    estimatedDutyConfigured: 1.4,
    estimatedDutyEffective: 0.58,
    dutyCeiling: 0.75,
    sourceCount: 1,
    modelCount: 3,
    unknownLatencyModels: [],
    models: [
      {
        id: 'BirdNET_V2.4',
        name: 'BirdNET v2.4',
        clipMs: 3000,
        stepMs: 1200,
        probeLatencyMs: 166,
        confirmations: 4,
        windowsInReference: 5,
      },
      {
        id: BAT_MODEL_ID,
        name: 'Bat',
        clipMs: 3000,
        stepMs: 1500,
        confirmations: 4,
        windowsInReference: 4,
      },
      {
        id: 'BirdNET_V3.0',
        name: 'BirdNET v3.0',
        clipMs: 5000,
        stepMs: 2000,
        probeLatencyMs: 874,
        confirmations: 3,
        windowsInReference: 3,
      },
    ],
    ...overrides,
  };
}

describe('fpCadence', () => {
  describe('parity with the backend', () => {
    const rows = loadFixture();

    it('loads the shared fixture', () => {
      expect(rows.length).toBeGreaterThan(100);
    });

    it('matches every backend confirmation count in the fixture', () => {
      const mismatches = rows.filter(row => {
        const step = modelStepSeconds(row.clipMs, overlapSecondsToNs(row.overlapSec));
        return minDetectionsForStep(step, row.level) !== row.confirmations;
      });
      expect(mismatches).toEqual([]);
    });
  });

  describe('minDetectionsForStep', () => {
    it('requires one confirmation with the filter off', () => {
      expect(minDetectionsForStep(0.2, 0)).toBe(1);
    });

    it('reproduces the worked examples', () => {
      expect(minDetectionsForStep(0.2, 5)).toBe(21);
      expect(minDetectionsForStep(1.2, 5)).toBe(4);
      expect(minDetectionsForStep(2.0, 5)).toBe(3);
    });
  });

  describe('savedReadout', () => {
    it('uses the server counts and leaves out the bat model', () => {
      expect(savedReadout(cadence())).toEqual([
        {
          id: 'BirdNET_V2.4',
          name: 'BirdNET v2.4',
          confirmations: 4,
          windows: 5,
          stepSeconds: 1.2,
        },
        { id: 'BirdNET_V3.0', name: 'BirdNET v3.0', confirmations: 3, windows: 3, stepSeconds: 2 },
      ]);
    });
  });

  describe('previewEffectiveOverlapSeconds', () => {
    it('keeps the plan effective overlap when only the level changes', () => {
      expect(previewEffectiveOverlapSeconds(cadence(), 2.8, 4)).toBe(1.8);
    });

    it('caps a changed overlap at the smallest sustainable step', () => {
      expect(previewEffectiveOverlapSeconds(cadence(), 2.4, 3)).toBeCloseTo(1.8, 9);
      expect(previewEffectiveOverlapSeconds(cadence(), 1.5, 3)).toBe(1.5);
    });

    it('applies no cap with the filter off', () => {
      expect(previewEffectiveOverlapSeconds(cadence(), 2.8, 0)).toBe(2.8);
    });

    it('runs an overloaded device at zero overlap', () => {
      expect(
        previewEffectiveOverlapSeconds(cadence({ status: 'overloaded', minBaseStepMs: 0 }), 2.7, 4)
      ).toBe(0);
    });

    it('uses the edited overlap when the plan has no step limit', () => {
      expect(
        previewEffectiveOverlapSeconds(
          cadence({ status: 'filterOff', configuredOverlapSec: 0, minBaseStepMs: 0 }),
          2.4,
          3
        )
      ).toBe(2.4);
    });
  });

  describe('previewReadout', () => {
    it('estimates each bird model at the capped overlap', () => {
      const rows = previewReadout(cadence(), 2.8, 4);
      expect(rows.map(r => [r.id, r.confirmations, Math.round(r.windows)])).toEqual([
        ['BirdNET_V2.4', 3, 5],
        ['BirdNET_V3.0', 2, 3],
      ]);
    });
  });

  describe('planLagsSettings', () => {
    it('is false when the plan matches the saved settings', () => {
      expect(planLagsSettings(cadence(), 2.8, 5)).toBe(false);
    });

    it('is true when the saved overlap differs from the plan', () => {
      expect(planLagsSettings(cadence(), 2.4, 3)).toBe(true);
    });

    it('is true when the server counts were computed for another level', () => {
      // The fixture counts are level 5 (4 of 5, 3 of 3); level 3 needs 3 and 2.
      expect(planLagsSettings(cadence(), 2.8, 3)).toBe(true);
    });

    it('applies the 0.1 s step floor when checking the server counts', () => {
      // Overlap 2.95 s: 120 windows, but the filter floors the step at 0.1 s,
      // so level 5 needs ceil(60 * 0.7) = 42, not 84.
      const fine = cadence({
        status: 'ok',
        configuredOverlapSec: 2.95,
        effectiveOverlapSec: 2.95,
        models: [
          {
            id: 'BirdNET_V2.4',
            name: 'BirdNET v2.4',
            clipMs: 3000,
            stepMs: 50,
            confirmations: 42,
            windowsInReference: 120,
          },
        ],
      });
      expect(planLagsSettings(fine, 2.95, 5)).toBe(false);
    });

    it('is true when the saved level crosses the filter on/off line', () => {
      expect(planLagsSettings(cadence({ status: 'filterOff' }), 2.8, 5)).toBe(true);
      expect(planLagsSettings(cadence(), 2.8, 0)).toBe(true);
    });
  });
});
