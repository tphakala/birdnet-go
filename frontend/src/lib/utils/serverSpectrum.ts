/**
 * Server-computed spectrum column helpers.
 *
 * WebKit cannot route an HLS-backed media element into the Web Audio graph
 * (https://bugs.webkit.org/show_bug.cgi?id=180696), so on Safari the live
 * spectrogram's AnalyserNode reports nothing and the waterfall stays blank.
 * `useSpectrogramAnalyser` falls back to magnitude columns computed on the
 * server and delivered over the audio-level SSE stream; the decoding and
 * queueing decisions live here so they stay pure and unit testable.
 */

/** One magnitude column: capture time (Unix seconds) and 0-255 bin values. */
export interface SpectrumColumn {
  time: number;
  bins: Uint8Array<ArrayBuffer>;
}

/** Decode a base64 spectrum column into bytes, or null if it is unusable. */
export function decodeSpectrumColumn(encoded: string): Uint8Array<ArrayBuffer> | null {
  try {
    const binary = globalThis.atob(encoded);
    if (binary.length === 0) return null;
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) {
      // eslint-disable-next-line security/detect-object-injection -- i is a loop counter bounded by the decoded length
      bytes[i] = binary.charCodeAt(i);
    }
    return bytes;
  } catch {
    return null;
  }
}

/** True when any bin is loud enough to count as audio rather than silence. */
export function hasSpectrumEnergy(bins: Uint8Array, threshold: number): boolean {
  return bins.some(v => v > threshold);
}

/**
 * Drop columns older than `windowSeconds` before the newest one.
 *
 * The queue only has to span the gap between live capture and buffered
 * playback, plus enough history to survive a small backward correction of the
 * playhead; holding more would grow memory to redraw audio nobody is listening
 * to. The newest column is always kept, so the queue never empties.
 *
 * `maxColumns` is a backstop for a server clock that jumps backwards, which
 * would otherwise make the newest timestamp a useless cutoff reference.
 */
export function trimSpectrumQueue(
  queue: SpectrumColumn[],
  windowSeconds: number,
  maxColumns: number
): SpectrumColumn[] {
  if (queue.length < 2) return queue;
  const cutoff = queue[queue.length - 1].time - windowSeconds;
  let drop = 0;
  // eslint-disable-next-line security/detect-object-injection -- drop is a loop counter bounded by the queue length
  while (drop < queue.length - 1 && queue[drop].time < cutoff) drop++;
  drop = Math.max(drop, queue.length - maxColumns);
  return drop > 0 ? queue.slice(drop) : queue;
}

/**
 * Pick the column to render for a given playhead wall-clock time.
 *
 * Columns are stamped with capture time, but HLS playback runs several seconds
 * behind live. Rendering each column on arrival would put the waterfall ahead
 * of the audio, and ahead of the detection labels overlaid on it, which are
 * already aligned to the playhead. So take the newest column at or before the
 * playhead.
 *
 * @returns the index to render, or -1 when nothing is due yet. A playhead of 0
 *   means "unknown" (no program date, nothing seekable), in which case the
 *   newest column is used — a slightly early waterfall beats a blank one.
 */
export function selectSpectrumColumn(queue: SpectrumColumn[], playhead: number): number {
  if (queue.length === 0) return -1;
  if (playhead <= 0) return queue.length - 1;
  for (let i = queue.length - 1; i >= 0; i--) {
    // eslint-disable-next-line security/detect-object-injection -- i is a loop counter bounded by the queue length
    if (queue[i].time <= playhead) return i;
  }
  return -1;
}

/** What the render loop should do with the queue on this tick. */
export interface SpectrumRenderState {
  /** Capture time of the column currently in the render buffer; 0 when blank. */
  renderedTime: number;
  /** Clock reading (ms) when the rendered column last advanced. */
  advancedAt: number;
  /** True once playhead alignment has been abandoned as unusable. */
  unaligned: boolean;
}

export interface SpectrumRenderStep {
  /** Queue index to copy into the render buffer, or -1 to leave it alone. */
  index: number;
  /** Clear the render buffer: nothing new has arrived for too long. */
  blank: boolean;
  state: SpectrumRenderState;
}

/**
 * Decide what the fallback waterfall shows on one tick.
 *
 * Three things have to hold at once, which is why this is a single reducer
 * rather than three checks scattered through the render loop:
 *
 * - a column is shown only when the playhead has reached it, so the waterfall
 *   matches the buffered audio and the detection labels drawn over it;
 * - the buffer is blanked when no *new* column has been shown for
 *   `stallTimeoutMs`, so a dropped stream reads as silence instead of smearing
 *   one frozen column across the canvas;
 * - if the playhead never reaches the queue at all — a browser and server whose
 *   clocks disagree, which the seekable-range playhead estimate cannot detect —
 *   alignment is abandoned rather than leaving the user with a blank waterfall
 *   forever. That is the pre-alignment behaviour, and it is still far better
 *   than the blank canvas this whole fallback exists to fix.
 */
export function nextSpectrumRender(
  queue: SpectrumColumn[],
  playhead: number,
  state: SpectrumRenderState,
  now: number,
  stallTimeoutMs: number
): SpectrumRenderStep {
  const stalled = now - state.advancedAt > stallTimeoutMs;
  const hold = (): SpectrumRenderStep =>
    stalled && state.renderedTime !== 0
      ? { index: -1, blank: true, state: { ...state, renderedTime: 0 } }
      : { index: -1, blank: false, state };

  if (queue.length === 0) return hold();

  const index = selectSpectrumColumn(queue, state.unaligned ? 0 : playhead);

  if (index < 0) {
    if (!stalled) return { index: -1, blank: false, state };
    const newest = queue.length - 1;
    return {
      index: newest,
      blank: false,
      // eslint-disable-next-line security/detect-object-injection -- newest is the queue's last index
      state: { renderedTime: queue[newest].time, advancedAt: now, unaligned: true },
    };
  }

  // eslint-disable-next-line security/detect-object-injection -- index comes from selectSpectrumColumn, bounded by the queue
  const time = queue[index].time;
  if (time === state.renderedTime) return hold();

  return { index, blank: false, state: { ...state, renderedTime: time, advancedAt: now } };
}
