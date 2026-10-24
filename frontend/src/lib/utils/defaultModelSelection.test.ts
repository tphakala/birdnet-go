import { describe, it, expect } from 'vitest';
import {
  defaultModelSelection,
  legacyDefaultSelection,
  type ModelSelectionOption,
} from './defaultModelSelection';
import type { AcousticModelAvailability } from '$lib/types/models';

// Options as GET /api/v2/models lists them: config alias `id`, registry ID
// `registryId`. Perch is listed first so registry-ID order (v2.4 first) can be
// told apart from option order.
const PERCH: ModelSelectionOption = { id: 'perch_v2', registryId: 'Perch_V2' };
const BIRDNET: ModelSelectionOption = { id: 'birdnet', registryId: 'BirdNET_V2.4' };
const BAT: ModelSelectionOption = { id: 'bat', registryId: 'BatDetect2' };
const OPTIONS = [PERCH, BIRDNET, BAT];

const UNKNOWN: AcousticModelAvailability = { kind: 'unknown' };
const NONE: AcousticModelAvailability = { kind: 'none', reason: 'none_installed' };
const FAILED: AcousticModelAvailability = { kind: 'none', reason: 'load_failed' };
function ready(defaultTargets: string[]): AcousticModelAvailability {
  return { kind: 'ready', defaultTargets };
}

describe('defaultModelSelection', () => {
  describe('none (N=0 or load failure)', () => {
    it('selects nothing even when models are offered', () => {
      expect(defaultModelSelection(NONE, OPTIONS)).toEqual([]);
      expect(defaultModelSelection(FAILED, OPTIONS)).toEqual([]);
    });
  });

  describe('ready', () => {
    it('maps registry IDs to aliases in default-target order (v2.4 first)', () => {
      expect(defaultModelSelection(ready(['BirdNET_V2.4', 'Perch_V2']), OPTIONS)).toEqual([
        'birdnet',
        'perch_v2',
      ]);
    });

    it('falls back to a case-insensitive alias match when registryId is absent', () => {
      const legacyServer: ModelSelectionOption[] = [{ id: 'perch_v2' }, { id: 'birdnet' }];
      expect(defaultModelSelection(ready(['BIRDNET']), legacyServer)).toEqual(['birdnet']);
    });

    it('drops targets that map to nothing and de-duplicates repeats', () => {
      expect(defaultModelSelection(ready(['Perch_V2', 'Ghost_V9', 'Perch_V2']), OPTIONS)).toEqual([
        'perch_v2',
      ]);
    });

    it('uses the legacy pick when no target maps onto the offered options', () => {
      expect(defaultModelSelection(ready(['Ghost_V9']), OPTIONS)).toEqual(['birdnet']);
    });

    it('selects nothing when there are no options to map onto', () => {
      expect(defaultModelSelection(ready(['BirdNET_V2.4']), [])).toEqual([]);
    });
  });

  describe('unknown (no verdict)', () => {
    it('prefers BirdNET when it is offered', () => {
      expect(defaultModelSelection(UNKNOWN, OPTIONS)).toEqual(['birdnet']);
    });

    it('takes the first option when BirdNET is not offered', () => {
      expect(defaultModelSelection(UNKNOWN, [PERCH, BAT])).toEqual(['perch_v2']);
    });

    it('never invents a phantom BirdNET when nothing is offered', () => {
      expect(defaultModelSelection(UNKNOWN, [])).toEqual([]);
    });
  });
});

describe('legacyDefaultSelection', () => {
  it('matches the pre-v4 heuristic exactly', () => {
    expect(legacyDefaultSelection(OPTIONS)).toEqual(['birdnet']);
    expect(legacyDefaultSelection([BAT, PERCH])).toEqual(['bat']);
    expect(legacyDefaultSelection([])).toEqual([]);
  });
});
