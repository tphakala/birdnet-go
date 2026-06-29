/**
 * Pure transforms for the seasonal density heatmap.
 *
 * The server sends a columnar, sparse payload ({ dates, slotResolutionMinutes, cells }); these
 * helpers turn it into the row objects the D3 chart draws, fold it down to an hourly grid for the
 * narrow-viewport fallback, and format slot labels. Kept framework-free so they are unit-tested
 * directly rather than through the Svelte component.
 */

const MINUTES_PER_HOUR = 60;
const MINUTES_PER_DAY = 24 * 60;
const HOURLY_RESOLUTION_MINUTES = 60;

export interface HeatmapData {
  /** Calendar dates (station-local), ascending; forms the x-axis. */
  dates: string[];
  /** Intra-day slot width in minutes (15, 30, or 60). */
  slotResolutionMinutes: number;
  /** Parallel sparse cell arrays: cell i is dates[dateIndex[i]] at slot[i] with count[i]. */
  cells: { dateIndex: number[]; slot: number[]; count: number[] };
}

export interface HeatmapCell {
  dateIndex: number;
  slot: number;
  count: number;
}

/**
 * Zips the parallel columnar arrays into cell objects. Truncates to the shortest array so a
 * malformed payload can never index past an array's end.
 */
export function heatmapCells(data: HeatmapData): HeatmapCell[] {
  const { dateIndex, slot, count } = data.cells;
  const n = Math.min(dateIndex.length, slot.length, count.length);
  const cells: HeatmapCell[] = [];
  for (let i = 0; i < n; i++) {
    // eslint-disable-next-line security/detect-object-injection -- i is a bounded loop index over our own arrays
    cells.push({ dateIndex: dateIndex[i], slot: slot[i], count: count[i] });
  }
  return cells;
}

/** Number of intra-day slots for a resolution (e.g. 15 -> 96, 60 -> 24). */
export function slotsPerDay(resolutionMinutes: number): number {
  return Math.floor(MINUTES_PER_DAY / resolutionMinutes);
}

/**
 * Folds a payload of any resolution down to 24 hourly slots, summing counts that share an hour.
 * Used by the narrow-viewport fallback so a year at 15-minute resolution still renders 24 readable
 * rows. A no-op when the payload is already hourly.
 */
export function toHourlyResolution(data: HeatmapData): HeatmapData {
  if (data.slotResolutionMinutes === HOURLY_RESOLUTION_MINUTES) {
    return data;
  }

  const merged = new Map<string, HeatmapCell>();
  for (const cell of heatmapCells(data)) {
    const hour = Math.floor((cell.slot * data.slotResolutionMinutes) / MINUTES_PER_HOUR);
    const key = `${cell.dateIndex}:${hour}`;
    const existing = merged.get(key);
    if (existing) {
      existing.count += cell.count;
    } else {
      merged.set(key, { dateIndex: cell.dateIndex, slot: hour, count: cell.count });
    }
  }

  // Preserve (dateIndex, slot) ordering for a deterministic, render-friendly result.
  const ordered = [...merged.values()].sort((a, b) =>
    a.dateIndex === b.dateIndex ? a.slot - b.slot : a.dateIndex - b.dateIndex
  );

  return {
    dates: data.dates,
    slotResolutionMinutes: HOURLY_RESOLUTION_MINUTES,
    cells: {
      dateIndex: ordered.map(c => c.dateIndex),
      slot: ordered.map(c => c.slot),
      count: ordered.map(c => c.count),
    },
  };
}

/** Formats a slot's wall-clock start time as HH:MM (e.g. slot 95 at 15-min resolution -> "23:45"). */
export function slotStartLabel(slot: number, resolutionMinutes: number): string {
  const startMinutes = slot * resolutionMinutes;
  const hh = Math.floor(startMinutes / MINUTES_PER_HOUR);
  const mm = startMinutes % MINUTES_PER_HOUR;
  return `${String(hh).padStart(2, '0')}:${String(mm).padStart(2, '0')}`;
}

/** Largest count across the cells, or 0 when there are none. Drives the color scale's domain. */
export function maxCellCount(cells: HeatmapCell[]): number {
  let max = 0;
  for (const cell of cells) {
    if (cell.count > max) max = cell.count;
  }
  return max;
}
