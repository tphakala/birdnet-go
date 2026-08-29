import { describe, expect, it } from 'vitest';

import {
  decodeSpectrumColumn,
  hasSpectrumEnergy,
  nextSpectrumRender,
  selectSpectrumColumn,
  trimSpectrumQueue,
  type SpectrumColumn,
  type SpectrumRenderState,
} from './serverSpectrum';

function column(time: number, first = 0): SpectrumColumn {
  const bins = new Uint8Array(4);
  bins[0] = first;
  return { time, bins };
}

function encode(bytes: number[]): string {
  return globalThis.btoa(String.fromCharCode(...bytes));
}

describe('decodeSpectrumColumn', () => {
  it('decodes base64 into the original bytes', () => {
    const bytes = [0, 1, 127, 128, 255];
    const decoded = decodeSpectrumColumn(encode(bytes));
    expect(decoded).not.toBeNull();
    expect(Array.from(decoded ?? [])).toEqual(bytes);
  });

  it('round-trips a full 512-bin column', () => {
    const bytes = Array.from({ length: 512 }, (_, i) => i % 256);
    const decoded = decodeSpectrumColumn(encode(bytes));
    expect(decoded).toHaveLength(512);
    expect(Array.from(decoded ?? [])).toEqual(bytes);
  });

  it('returns null for an empty payload', () => {
    expect(decodeSpectrumColumn('')).toBeNull();
  });

  it('returns null for a malformed payload instead of throwing', () => {
    expect(decodeSpectrumColumn('not base64!!')).toBeNull();
  });
});

describe('hasSpectrumEnergy', () => {
  it('is false for digital silence', () => {
    expect(hasSpectrumEnergy(new Uint8Array(512), 8)).toBe(false);
  });

  it('is false when every bin sits at or below the threshold', () => {
    expect(hasSpectrumEnergy(Uint8Array.from([1, 5, 8, 8]), 8)).toBe(false);
  });

  it('is true as soon as one bin exceeds the threshold', () => {
    expect(hasSpectrumEnergy(Uint8Array.from([0, 0, 9, 0]), 8)).toBe(true);
  });
});

describe('trimSpectrumQueue', () => {
  it('leaves a queue inside the window untouched', () => {
    const queue = [column(100), column(105), column(110)];
    expect(trimSpectrumQueue(queue, 30, 800)).toBe(queue);
  });

  it('drops columns older than the window', () => {
    const queue = [column(10), column(60), column(95), column(100)];
    expect(trimSpectrumQueue(queue, 30, 800).map(c => c.time)).toEqual([95, 100]);
  });

  it('always keeps the newest column, however old the queue is', () => {
    const queue = [column(10), column(20)];
    expect(trimSpectrumQueue(queue, 1, 800).map(c => c.time)).toEqual([20]);
  });

  it('handles an empty or single-entry queue', () => {
    expect(trimSpectrumQueue([], 30, 800)).toEqual([]);
    expect(trimSpectrumQueue([column(1)], 30, 800).map(c => c.time)).toEqual([1]);
  });
});

describe('selectSpectrumColumn', () => {
  const queue = [column(100, 1), column(101, 2), column(102, 3)];

  it('returns -1 for an empty queue', () => {
    expect(selectSpectrumColumn([], 100)).toBe(-1);
  });

  it('uses the newest column when the playhead is unknown', () => {
    expect(selectSpectrumColumn(queue, 0)).toBe(2);
  });

  it('picks the newest column at or before the playhead', () => {
    expect(selectSpectrumColumn(queue, 101.5)).toBe(1);
    expect(selectSpectrumColumn(queue, 101)).toBe(1);
  });

  it('holds the whole queue back when the playhead is behind all of it', () => {
    expect(selectSpectrumColumn(queue, 99)).toBe(-1);
  });

  it('does not run ahead of the playhead once it catches up to live', () => {
    expect(selectSpectrumColumn(queue, 500)).toBe(2);
  });

  it('walks forward one column at a time as the playhead advances', () => {
    expect(queue.map(c => selectSpectrumColumn(queue, c.time))).toEqual([0, 1, 2]);
  });
});

describe('nextSpectrumRender', () => {
  const STALL = 1500;
  const fresh: SpectrumRenderState = { renderedTime: 0, advancedAt: 1000, unaligned: false };

  it('renders the column the playhead has reached', () => {
    const queue = [column(100), column(101), column(102)];
    const step = nextSpectrumRender(queue, 101, fresh, 1000, STALL);
    expect(step).toMatchObject({ index: 1, blank: false });
    expect(step.state).toMatchObject({ renderedTime: 101, advancedAt: 1000, unaligned: false });
  });

  it('holds without re-rendering while the playhead sits on the same column', () => {
    const queue = [column(100), column(101)];
    const shown: SpectrumRenderState = { renderedTime: 101, advancedAt: 1000, unaligned: false };
    expect(nextSpectrumRender(queue, 101.2, shown, 1400, STALL)).toMatchObject({
      index: -1,
      blank: false,
      state: shown,
    });
  });

  it('blanks the waterfall when no new column arrives before the stall timeout', () => {
    const queue = [column(100), column(101)];
    const shown: SpectrumRenderState = { renderedTime: 101, advancedAt: 1000, unaligned: false };
    const step = nextSpectrumRender(queue, 101.2, shown, 1000 + STALL + 1, STALL);
    expect(step).toMatchObject({ index: -1, blank: true });
    expect(step.state.renderedTime).toBe(0);
  });

  it('blanks only once, not on every tick after the stall', () => {
    const queue = [column(100)];
    const blanked: SpectrumRenderState = { renderedTime: 0, advancedAt: 1000, unaligned: false };
    expect(nextSpectrumRender(queue, 100.5, blanked, 9000, STALL).blank).toBe(false);
  });

  it('recovers as soon as a newer column becomes due', () => {
    const queue = [column(100), column(105)];
    const blanked: SpectrumRenderState = { renderedTime: 0, advancedAt: 1000, unaligned: false };
    expect(nextSpectrumRender(queue, 105, blanked, 9000, STALL)).toMatchObject({
      index: 1,
      blank: false,
    });
  });

  it('blanks when the stream stops delivering columns entirely', () => {
    const shown: SpectrumRenderState = { renderedTime: 101, advancedAt: 1000, unaligned: false };
    expect(nextSpectrumRender([], 200, shown, 1000 + STALL + 1, STALL)).toMatchObject({
      index: -1,
      blank: true,
    });
  });

  it('waits, then abandons alignment when the playhead never reaches the queue', () => {
    // Browser clock behind the server: every queued column looks like the future.
    const queue = [column(500), column(501)];
    expect(nextSpectrumRender(queue, 100, fresh, 1200, STALL)).toMatchObject({
      index: -1,
      blank: false,
      state: { unaligned: false },
    });

    const step = nextSpectrumRender(queue, 100, fresh, 1000 + STALL + 1, STALL);
    expect(step).toMatchObject({ index: 1, blank: false });
    expect(step.state.unaligned).toBe(true);
  });

  it('keeps rendering the newest column once alignment is abandoned', () => {
    const queue = [column(500), column(501), column(502)];
    const unaligned: SpectrumRenderState = { renderedTime: 501, advancedAt: 1000, unaligned: true };
    expect(nextSpectrumRender(queue, 0, unaligned, 1000, STALL)).toMatchObject({ index: 2 });
  });

  it('replays history when the playhead jumps backwards', () => {
    const queue = [column(100), column(101), column(102)];
    const shown: SpectrumRenderState = { renderedTime: 102, advancedAt: 1000, unaligned: false };
    expect(nextSpectrumRender(queue, 100, shown, 1000, STALL)).toMatchObject({ index: 0 });
  });

  it('advances one column per tick as the playhead runs forward', () => {
    const queue = [column(100), column(101), column(102)];
    let state = fresh;
    const seen: number[] = [];
    for (const playhead of [100, 101, 102]) {
      const step = nextSpectrumRender(queue, playhead, state, 1000, STALL);
      state = step.state;
      seen.push(step.index);
    }
    expect(seen).toEqual([0, 1, 2]);
  });
});
