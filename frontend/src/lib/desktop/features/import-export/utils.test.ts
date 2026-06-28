import { describe, it, expect } from 'vitest';
import { deriveSourceStepState, isUnreadable } from './utils';
import type { ImportSourcesResponse, SourceCandidate } from './types';

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
