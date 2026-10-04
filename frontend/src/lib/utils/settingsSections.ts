/**
 * Keys of each per-section settings payload that the backend never lets the
 * API change (getBlockedFieldMap and getAudioBlockedFields in
 * internal/api/v2): it reverts them and reports them in skippedFields. A
 * section save drops them before sending and before updating the store, so
 * the store never records a value the server refused. rangeFilter is dropped
 * whole because it holds the blocked model, species and lastUpdated fields.
 */
import { isPlainObject } from './security';

/** Server-owned keys of the birdnet section payload. */
export type BirdNetServerOwnedKey = 'rangeFilter';
/** Server-owned keys of the audio section payload. */
export type AudioServerOwnedKey = 'ffmpegPath' | 'soxPath';

const SERVER_OWNED_KEYS: Readonly<Record<string, readonly string[]>> = {
  birdnet: ['rangeFilter'] satisfies readonly BirdNetServerOwnedKey[],
  audio: ['ffmpegPath', 'soxPath'] satisfies readonly AudioServerOwnedKey[],
};

/**
 * Returns a shallow copy of a section payload without the keys the server owns
 * for that section. Type exclusion alone is not enough: a variable typed as a
 * full section object still carries them through structural typing.
 */
export function withoutServerOwnedKeys(section: string, body: unknown): Record<string, unknown> {
  if (!isPlainObject(body)) return {};
  const owned = Object.hasOwn(SERVER_OWNED_KEYS, section)
    ? // eslint-disable-next-line security/detect-object-injection -- guarded by Object.hasOwn
      SERVER_OWNED_KEYS[section]
    : [];
  return Object.fromEntries(Object.entries(body).filter(([key]) => !owned.includes(key)));
}
