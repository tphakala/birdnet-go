/**
 * Detection overlay utilities — pure functions for managing spectrogram labels.
 *
 * Handles snapshot diffing, deduplication, and waiting queue promotion.
 */

/** A detection waiting in the queue (not yet visible on spectrogram). */
export interface QueuedLabel {
  text: string;
  firstDetected: number; // Unix seconds
  ySlot: number;
}

/** A label actively scrolling on the spectrogram canvas. */
export interface OverlayLabel {
  text: string;
  birthTime: number; // performance.now() when promoted
  ySlot: number;
}

/** Minimal pending detection shape for diffing. */
interface PendingEntry {
  species: string;
  sourceID: string;
  firstDetected: number;
  status: 'active' | 'approved' | 'rejected';
  hitCount?: number;
}

const DEDUP_INTERVAL_SECONDS = 6;

/**
 * Diff two pending snapshots, returning species with new activity for a given source.
 * Returns species that are newly appeared OR have an increased hitCount (new inference hit).
 * Filters by sourceID and ignores rejected status.
 */
export function diffPendingSnapshot(
  prev: PendingEntry[],
  curr: PendingEntry[],
  activeSourceID: string
): PendingEntry[] {
  const prevBySpecies = new Map(
    prev.filter(d => d.sourceID === activeSourceID).map(d => [d.species, d])
  );

  return curr.filter(d => {
    if (d.sourceID !== activeSourceID || d.status === 'rejected') return false;
    const prevEntry = prevBySpecies.get(d.species);
    if (!prevEntry) return true; // New species
    // Existing species with increased hit count means a new inference hit
    return (d.hitCount ?? 0) > (prevEntry.hitCount ?? 0);
  });
}

/**
 * Check if a species label should be deduplicated (last label < 6s ago in media time).
 */
export function shouldDedup(
  species: string,
  firstDetected: number,
  lastSeenMap: Map<string, number>
): boolean {
  const last = lastSeenMap.get(species);
  if (last === undefined) return false;
  return firstDetected - last < DEDUP_INTERVAL_SECONDS;
}

/**
 * Promote labels from the waiting queue when the playhead reaches their time.
 * Returns promoted labels (stamped with birthTime) and remaining queue.
 */
export function promoteFromQueue(
  queue: QueuedLabel[],
  wallClockAtPlayheadUnix: number,
  performanceNow: number
): { promoted: OverlayLabel[]; remaining: QueuedLabel[] } {
  const promoted: OverlayLabel[] = [];
  const remaining: QueuedLabel[] = [];

  for (const label of queue) {
    if (wallClockAtPlayheadUnix >= label.firstDetected) {
      promoted.push({
        text: label.text,
        birthTime: performanceNow,
        ySlot: label.ySlot,
      });
    } else {
      remaining.push(label);
    }
  }

  return { promoted, remaining };
}

/**
 * Assign the next available Y slot, cycling through maxSlots.
 */
export function nextYSlot(counter: number, maxSlots: number): { slot: number; next: number } {
  const slot = counter % maxSlots;
  return { slot, next: counter + 1 };
}
