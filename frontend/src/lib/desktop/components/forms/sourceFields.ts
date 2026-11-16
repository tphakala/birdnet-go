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
export function sourceListField(prefix: string): string {
  return `${prefix}list`;
}

/** Field key of adding and deleting sound card sources. */
export const AUDIO_SOURCES_LIST_FIELD = sourceListField(AUDIO_SOURCES_FIELD_PREFIX);

/** A disabled equalizer without filters is stored as undefined. */
export function normalizeEqualizer<F>(equalizer: { enabled: boolean; filters: F[] }) {
  return equalizer.enabled || equalizer.filters.length > 0
    ? { enabled: equalizer.enabled, filters: equalizer.filters }
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
