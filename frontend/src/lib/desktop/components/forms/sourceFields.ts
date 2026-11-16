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
