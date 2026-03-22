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
  firstDetected?: number; // Unix seconds — original detection time (for debug display)
  promotionDelta?: number; // seconds — frozen offset at promotion (wallClock - firstDetected)
}

/** Minimal pending detection shape for diffing. */
interface PendingEntry {
  species: string;
  sourceID: string;
  firstDetected: number;
  audioCapturedAt?: number;
  lastUpdated?: number;
  status: 'active' | 'approved' | 'rejected';
  hitCount?: number;
}

const DEDUP_INTERVAL_SECONDS = 6;

/** How long (seconds) to keep a species in the dedup map after last label.
 *  Generous buffer beyond DEDUP_INTERVAL_SECONDS to avoid premature cleanup. */
export const STALE_DEDUP_PRUNE_SECONDS = 10;

/** Seconds to subtract from firstDetected when queuing a label.
 *  Compensates for the delay between audio capture and detection arrival,
 *  so the label appears closer to the actual sound on the waterfall. */
export const LABEL_LEAD_IN_SECONDS = 1.5;

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
 * Check if a species label should be deduplicated (last label < 6s ago).
 * The timestamp parameter should be the current wall-clock time (Unix seconds),
 * NOT the detection's firstDetected — using firstDetected was the original bug
 * that caused all repeat labels to be silently deduped.
 */
export function shouldDedup(
  species: string,
  timestamp: number,
  lastSeenMap: Map<string, number>
): boolean {
  const last = lastSeenMap.get(species);
  if (last === undefined) return false;
  return timestamp - last < DEDUP_INTERVAL_SECONDS;
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
      // Back-date birthTime so the label appears at the waterfall position
      // corresponding to firstDetected, not at the right edge.
      const ageOffsetSec = Math.max(0, wallClockAtPlayheadUnix - label.firstDetected);
      promoted.push({
        text: label.text,
        birthTime: performanceNow - ageOffsetSec * 1000,
        ySlot: label.ySlot,
        firstDetected: label.firstDetected,
        promotionDelta: ageOffsetSec,
      });
    } else {
      remaining.push(label);
    }
  }

  return { promoted, remaining };
}

/**
 * Compute the wall-clock time (Unix seconds) at the current playhead position.
 * Prefers hls.playingDate (accurate, interpolated from #EXT-X-PROGRAM-DATE-TIME).
 * Falls back to seekable-based live lag estimate for native HLS (Safari/iOS).
 * Returns 0 if playhead time is unavailable.
 */
export function computeWallClockAtPlayhead(
  audioElement: HTMLAudioElement,
  hlsPlayingDate: Date | null,
  nowUnix: number
): number {
  if (hlsPlayingDate) {
    return hlsPlayingDate.getTime() / 1000;
  }
  if (audioElement.currentTime > 0 && audioElement.seekable.length > 0) {
    const liveEdge = audioElement.seekable.end(audioElement.seekable.length - 1);
    const liveLagSeconds = Math.max(0, liveEdge - audioElement.currentTime);
    return nowUnix - liveLagSeconds;
  }
  return 0;
}

/**
 * Assign the next available Y slot, cycling through maxSlots.
 */
export function nextYSlot(counter: number, maxSlots: number): { slot: number; next: number } {
  const slot = counter % maxSlots;
  return { slot, next: counter + 1 };
}
