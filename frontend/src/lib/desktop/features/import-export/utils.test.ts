import { describe, it, expect } from 'vitest';
import {
  deriveSourceStepState,
  importProgressPercent,
  isUnreadable,
  shouldReconcileStalledStream,
  STREAM_STALL_THRESHOLD,
} from './utils';
import type { ImportProgress, ImportSourcesResponse, SourceCandidate } from './types';

const baseResp: ImportSourcesResponse = {
  environment: 'Bare Metal',
  containerized: false,
  run_as_user: 'birdnet',
  run_as_uid: 1000,
  candidates: [],
  guidance: null,
};

describe('deriveSourceStepState', () => {
  it('returns zero-candidates when no candidates', () => {
    expect(deriveSourceStepState(baseResp)).toBe('zero-candidates');
  });

  it('returns candidates when one or more candidates are present', () => {
    const resp: ImportSourcesResponse = {
      ...baseResp,
      candidates: [{ path: '/home/pi/BirdNET-Pi/birds.db' } as SourceCandidate],
    };
    expect(deriveSourceStepState(resp)).toBe('candidates');
  });

  it('returns zero-candidates for null', () => {
    expect(deriveSourceStepState(null)).toBe('zero-candidates');
  });
});

describe('isUnreadable', () => {
  it('returns true for a permission_denied invalid candidate', () => {
    const c = { valid: false, reason: 'permission_denied' } as SourceCandidate;
    expect(isUnreadable(c)).toBe(true);
  });

  it('returns false for a valid candidate', () => {
    const c = { valid: true, reason: '' } as SourceCandidate;
    expect(isUnreadable(c)).toBe(false);
  });

  it('returns false for an invalid candidate with a non-permission reason', () => {
    const c = { valid: false, reason: 'invalid_schema' } as SourceCandidate;
    expect(isUnreadable(c)).toBe(false);
  });
});

describe('importProgressPercent', () => {
  const progress = (total: number, processed: number): ImportProgress => ({
    total,
    processed,
    inserted: 0,
    skipped: 0,
    errors: 0,
    phase: 'import',
  });

  it('returns 0 for null progress', () => {
    expect(importProgressPercent(null)).toBe(0);
  });

  it('returns 0 when total is zero or negative', () => {
    expect(importProgressPercent(progress(0, 5))).toBe(0);
    expect(importProgressPercent(progress(-10, 5))).toBe(0);
  });

  it('returns 0 for non-finite fields', () => {
    expect(importProgressPercent(progress(Number.NaN, 5))).toBe(0);
    expect(importProgressPercent(progress(100, Number.POSITIVE_INFINITY))).toBe(0);
  });

  it('rounds to the nearest whole percent', () => {
    expect(importProgressPercent(progress(3, 1))).toBe(33);
    expect(importProgressPercent(progress(3, 2))).toBe(67);
  });

  it('clamps above 100 when processed exceeds total', () => {
    expect(importProgressPercent(progress(10, 15))).toBe(100);
  });

  it('clamps below 0 when processed is negative', () => {
    expect(importProgressPercent(progress(10, -5))).toBe(0);
  });
});

describe('shouldReconcileStalledStream', () => {
  it('returns false below the threshold', () => {
    for (let attempts = 0; attempts < STREAM_STALL_THRESHOLD; attempts++) {
      expect(shouldReconcileStalledStream(attempts)).toBe(false);
    }
  });

  it('returns true at the threshold and its multiples only', () => {
    expect(shouldReconcileStalledStream(STREAM_STALL_THRESHOLD)).toBe(true);
    expect(shouldReconcileStalledStream(STREAM_STALL_THRESHOLD + 1)).toBe(false);
    expect(shouldReconcileStalledStream(STREAM_STALL_THRESHOLD * 2)).toBe(true);
    expect(shouldReconcileStalledStream(STREAM_STALL_THRESHOLD * 3)).toBe(true);
  });
});
