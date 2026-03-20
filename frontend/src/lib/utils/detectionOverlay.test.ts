import { describe, it, expect } from 'vitest';
import {
  diffPendingSnapshot,
  shouldDedup,
  promoteFromQueue,
  nextYSlot,
  type QueuedLabel,
} from './detectionOverlay';

describe('diffPendingSnapshot', () => {
  it('detects new species in snapshot', () => {
    const prev = [
      { species: 'Blue Tit', sourceID: 'src1', firstDetected: 100, status: 'active' as const },
    ];
    const curr = [
      { species: 'Blue Tit', sourceID: 'src1', firstDetected: 100, status: 'active' as const },
      { species: 'Great Tit', sourceID: 'src1', firstDetected: 103, status: 'active' as const },
    ];
    const newDetections = diffPendingSnapshot(prev, curr, 'src1');
    expect(newDetections).toHaveLength(1);
    expect(newDetections[0].species).toBe('Great Tit');
  });

  it('filters by sourceID', () => {
    const curr = [
      { species: 'Robin', sourceID: 'src1', firstDetected: 100, status: 'active' as const },
      { species: 'Wren', sourceID: 'src2', firstDetected: 100, status: 'active' as const },
    ];
    const newDetections = diffPendingSnapshot([], curr, 'src1');
    expect(newDetections).toHaveLength(1);
    expect(newDetections[0].species).toBe('Robin');
  });

  it('ignores rejected status', () => {
    const curr = [
      { species: 'Robin', sourceID: 'src1', firstDetected: 100, status: 'rejected' as const },
    ];
    const newDetections = diffPendingSnapshot([], curr, 'src1');
    expect(newDetections).toHaveLength(0);
  });

  it('allows approved status', () => {
    const curr = [
      { species: 'Robin', sourceID: 'src1', firstDetected: 100, status: 'approved' as const },
    ];
    const newDetections = diffPendingSnapshot([], curr, 'src1');
    expect(newDetections).toHaveLength(1);
  });

  it('detects increased hitCount as new activity', () => {
    const prev = [
      {
        species: 'Blue Tit',
        sourceID: 'src1',
        firstDetected: 100,
        status: 'active' as const,
        hitCount: 3,
      },
    ];
    const curr = [
      {
        species: 'Blue Tit',
        sourceID: 'src1',
        firstDetected: 100,
        status: 'active' as const,
        hitCount: 5,
      },
    ];
    const newDetections = diffPendingSnapshot(prev, curr, 'src1');
    expect(newDetections).toHaveLength(1);
    expect(newDetections[0].species).toBe('Blue Tit');
  });

  it('ignores unchanged hitCount', () => {
    const prev = [
      {
        species: 'Blue Tit',
        sourceID: 'src1',
        firstDetected: 100,
        status: 'active' as const,
        hitCount: 3,
      },
    ];
    const curr = [
      {
        species: 'Blue Tit',
        sourceID: 'src1',
        firstDetected: 100,
        status: 'active' as const,
        hitCount: 3,
      },
    ];
    const newDetections = diffPendingSnapshot(prev, curr, 'src1');
    expect(newDetections).toHaveLength(0);
  });
});

describe('shouldDedup', () => {
  it('returns true when same species within 6 seconds', () => {
    const lastSeen = new Map([['Blue Tit', 100]]);
    expect(shouldDedup('Blue Tit', 105, lastSeen)).toBe(true);
  });

  it('returns false when same species after 6 seconds', () => {
    const lastSeen = new Map([['Blue Tit', 100]]);
    expect(shouldDedup('Blue Tit', 107, lastSeen)).toBe(false);
  });

  it('returns false for new species', () => {
    const lastSeen = new Map<string, number>();
    expect(shouldDedup('Great Tit', 100, lastSeen)).toBe(false);
  });
});

describe('promoteFromQueue', () => {
  it('promotes labels when playhead reaches their time', () => {
    const queue: QueuedLabel[] = [
      { text: 'Blue Tit', firstDetected: 100, ySlot: 0 },
      { text: 'Great Tit', firstDetected: 110, ySlot: 1 },
    ];
    const { promoted, remaining } = promoteFromQueue(queue, 105, 50000);
    expect(promoted).toHaveLength(1);
    expect(promoted[0].text).toBe('Blue Tit');
    expect(promoted[0].birthTime).toBe(50000);
    expect(remaining).toHaveLength(1);
    expect(remaining[0].text).toBe('Great Tit');
  });

  it('promotes nothing when playhead is behind all labels', () => {
    const queue: QueuedLabel[] = [{ text: 'Blue Tit', firstDetected: 100, ySlot: 0 }];
    const { promoted, remaining } = promoteFromQueue(queue, 95, 50000);
    expect(promoted).toHaveLength(0);
    expect(remaining).toHaveLength(1);
  });
});

describe('nextYSlot', () => {
  it('cycles through slots', () => {
    expect(nextYSlot(0, 4)).toEqual({ slot: 0, next: 1 });
    expect(nextYSlot(1, 4)).toEqual({ slot: 1, next: 2 });
    expect(nextYSlot(4, 4)).toEqual({ slot: 0, next: 5 });
  });
});
