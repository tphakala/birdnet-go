import { describe, it, expect } from 'vitest';
import {
  diffPendingSnapshot,
  shouldDedup,
  promoteFromQueue,
  nextYSlot,
  buildQueuedLabel,
  LABEL_LEAD_IN_SECONDS,
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

  it('allows repeat when wall-clock time advances past 6s (regression: was broken with firstDetected)', () => {
    const lastSeen = new Map([['Blue Tit', 100]]);
    // Using wall-clock (107) correctly allows the repeat
    expect(shouldDedup('Blue Tit', 107, lastSeen)).toBe(false);
    // Using firstDetected (100) would incorrectly dedup (100-100=0 < 6) — the original bug
    expect(shouldDedup('Blue Tit', 100, lastSeen)).toBe(true);
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
    // birthTime is back-dated by (wallClockAtPlayhead - firstDetected) = (105 - 100) = 5s = 5000ms
    expect(promoted[0].birthTime).toBe(50000 - 5000);
    expect(promoted[0].firstDetected).toBe(100);
    expect(promoted[0].promotionDelta).toBe(5);
    expect(remaining).toHaveLength(1);
    expect(remaining[0].text).toBe('Great Tit');
  });

  it('promotes with exact birthTime when playhead equals firstDetected', () => {
    const queue: QueuedLabel[] = [{ text: 'Blue Tit', firstDetected: 100, ySlot: 0 }];
    const { promoted } = promoteFromQueue(queue, 100, 50000);
    expect(promoted).toHaveLength(1);
    // No back-dating when playhead == firstDetected
    expect(promoted[0].birthTime).toBe(50000);
    expect(promoted[0].promotionDelta).toBe(0);
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

describe('buildQueuedLabel', () => {
  const det = {
    species: 'American Robin',
    scientificName: 'Turdus migratorius',
    firstDetected: 1000,
  };

  // Stand-in for localizeSpeciesName: returns a localized name only for the
  // mapped scientific name, otherwise the server-locale fallback.
  const localize = (scientificName: string | undefined, fallback: string): string =>
    scientificName === 'Turdus migratorius' ? 'Punarinta' : fallback;

  it('localizes the label text via the injected localizer', () => {
    const label = buildQueuedLabel(det, 2, localize);
    expect(label.text).toBe('Punarinta');
    expect(label.ySlot).toBe(2);
  });

  it('falls back to the server-locale common name when no localized name exists', () => {
    const label = buildQueuedLabel(
      { ...det, scientificName: 'Troglodytes troglodytes' },
      0,
      localize
    );
    expect(label.text).toBe('American Robin');
  });

  it('back-dates firstDetected by the lead-in to align the label with the sound', () => {
    const label = buildQueuedLabel(det, 0, localize);
    expect(label.firstDetected).toBe(1000 - LABEL_LEAD_IN_SECONDS);
  });

  it('prefers audioCapturedAt over firstDetected for back-dating when present', () => {
    const label = buildQueuedLabel({ ...det, audioCapturedAt: 950 }, 0, localize);
    expect(label.firstDetected).toBe(950 - LABEL_LEAD_IN_SECONDS);
  });
});
