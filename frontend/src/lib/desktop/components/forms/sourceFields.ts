import type { EqualizerSettings, QuietHoursConfig } from '$lib/stores/settings';

/**
 * Auto-save field keys of the source tabs (sound cards, streams). A tab picks a
 * prefix; every key of its controls starts with it, so the tab can scope its
 * save state to them (SettingsTabs autoSaveScope). Row ids are UI-only
 * (generateId) and never persisted.
 */

/** Prefix of the field keys of the sound card tab. */
export const AUDIO_SOURCES_FIELD_PREFIX = 'audio:sources:';

/** The groups of a source card that save separately. */
export type SourceFieldPart =
  'name' | 'capture' | 'connection' | 'enabled' | 'gain' | 'models' | 'equalizer' | 'quietHours';

/** Field key of one group of one row of a source tab. */
export function sourceField(prefix: string, rowId: string, part: SourceFieldPart): string {
  return `${prefix}${rowId}:${part}`;
}

/** Field key of adding and deleting rows of a source tab. */
function sourceListField(prefix: string): string {
  return `${prefix}list`;
}

/** Field key of adding and deleting sound card sources. */
export const AUDIO_SOURCES_LIST_FIELD = sourceListField(AUDIO_SOURCES_FIELD_PREFIX);

/** Prefix of the field keys of the streams tab. */
export const STREAMS_FIELD_PREFIX = 'rtsp:streams:';

/** Field key of adding and deleting streams. */
export const STREAMS_LIST_FIELD = sourceListField(STREAMS_FIELD_PREFIX);

/** A source equalizer as it is saved: a disabled one without filters is stored as undefined. */
export function savedEqualizer(updated: EqualizerSettings): EqualizerSettings | undefined {
  return updated.enabled || updated.filters.length > 0
    ? { enabled: updated.enabled, filters: updated.filters }
    : undefined;
}

/** The row ids and open rows of a source list at one moment. */
export interface RowSnapshot {
  ids: string[];
  expanded: string[];
}

/**
 * Remembers the rows as they were before the manager's own add or delete, so a
 * refused save that takes the list back can restore them instead of giving
 * every card a fresh id (which would drop its open state and unapplied drafts).
 * The engine coalesces list edits made while a save runs, so the rows stay those
 * from before the first unconfirmed edit until the save is confirmed or the
 * list returns to them.
 */
export class RowRollback {
  private snapshot: RowSnapshot | null = null;

  /** Records the rows before an add or delete, unless an earlier edit is still unconfirmed. */
  remember(ids: readonly string[], expanded: Iterable<string>): void {
    this.snapshot ??= { ids: [...ids], expanded: [...expanded] };
  }

  /**
   * Returns the remembered rows, and forgets them, when the list is back at
   * their length. Any other length leaves them for a later edit to undo.
   */
  take(count: number): RowSnapshot | null {
    if (this.snapshot?.ids.length !== count) return null;
    const snapshot = this.snapshot;
    this.snapshot = null;
    return snapshot;
  }

  /** Forgets the remembered rows once the save went through. */
  clear(): void {
    this.snapshot = null;
  }
}

/**
 * True when an equalizer change is continuous (a filter value) and waits for a
 * pause; switching it or adding and removing filters is discrete.
 */
export function isContinuousEqualizerChange(
  updated: EqualizerSettings,
  current: EqualizerSettings
): boolean {
  return updated.enabled === current.enabled && updated.filters.length === current.filters.length;
}

const QUIET_HOURS_DISCRETE_KEYS = ['enabled', 'mode', 'startEvent', 'endEvent'] as const;

/**
 * True when a quiet hours change only touches times and offsets, which are typed
 * or stepped and wait for a pause.
 */
export function isContinuousQuietHoursChange(
  updated: QuietHoursConfig,
  current: QuietHoursConfig
): boolean {
  return QUIET_HOURS_DISCRETE_KEYS.every(
    // eslint-disable-next-line security/detect-object-injection -- key is a fixed QuietHoursConfig key
    key => updated[key] === current[key]
  );
}
