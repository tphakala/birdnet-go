/**
 * Auto-save field keys of the sound card tab. Every key starts with
 * AUDIO_SOURCES_FIELD_PREFIX, so the tab can scope its save state to them.
 * Row ids are UI-only (generateId) and never persisted.
 */

/** Prefix of every field key on the sound card tab. */
export const AUDIO_SOURCES_FIELD_PREFIX = 'audio:sources:';

/** Field key of adding and deleting sources. */
export const AUDIO_SOURCES_LIST_FIELD = `${AUDIO_SOURCES_FIELD_PREFIX}list`;

/** The groups of a source card that save separately. */
export type AudioSourceFieldPart =
  'name' | 'capture' | 'gain' | 'models' | 'equalizer' | 'quietHours';

/** Field key of one group of one source row. */
export function audioSourceField(rowId: string, part: AudioSourceFieldPart): string {
  return `${AUDIO_SOURCES_FIELD_PREFIX}${rowId}:${part}`;
}
