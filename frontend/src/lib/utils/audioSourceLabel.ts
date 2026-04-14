/**
 * Audio source label resolution utilities.
 *
 * The backend populates `detection.source.displayName` when it can resolve a
 * configured friendly name for the audio source (sound card `AudioSourceConfig.name`
 * or RTSP `StreamConfig.name`). However there are cases where the server payload
 * only contains the raw source id:
 *
 *   1. Legacy v1 datastore reads skip `Source` on the Note model (`gorm:"-"`),
 *      so historical detections never carry a friendly name on read paths that
 *      still use v1.
 *   2. If a user renames a source in settings, existing detections keep the old
 *      id/displayName written at save time.
 *
 * These helpers let the UI fall back to the current settings to look up the
 * friendly name from the source id, without requiring an API change.
 */
import type { SourceInfo } from '$lib/types/detection.types';
import type { AudioSourceConfig, StreamConfig } from '$lib/stores/settings';

/**
 * Resolve the friendly name for a detection audio source.
 *
 * Resolution order:
 *   1. `source.displayName` when present and distinct from `source.id` (server
 *      already resolved a friendly label).
 *   2. A matching `AudioSourceConfig.device` in the current sound card list,
 *      returning its configured `name` if non-empty.
 *   3. A matching `StreamConfig.url` in the current RTSP stream list, returning
 *      its configured `name` if non-empty.
 *   4. `source.id` as a bare fallback (which equals `source.displayName` in
 *      the remaining cases).
 *
 * @param source - The detection source payload (may be null/undefined).
 * @param audioSources - Current sound card configuration (optional).
 * @param rtspStreams - Current RTSP stream configuration (optional).
 * @returns The best-effort friendly name, or `null` when nothing is known.
 */
export function getFriendlyAudioSourceName(
  source: SourceInfo | null | undefined,
  audioSources: AudioSourceConfig[] | undefined,
  rtspStreams: StreamConfig[] | undefined
): string | null {
  if (source == null) {
    return null;
  }

  const displayName = source.displayName ?? '';
  const id = source.id;

  // 1. Server already resolved a friendly name distinct from the id.
  if (displayName !== '' && displayName !== id) {
    return displayName;
  }

  // 2. Look up against configured sound cards by device path.
  if (id !== '' && audioSources) {
    for (const entry of audioSources) {
      if (entry.device === id && entry.name !== '') {
        return entry.name;
      }
    }
  }

  // 3. Look up against configured RTSP streams by url.
  if (id !== '' && rtspStreams) {
    for (const stream of rtspStreams) {
      if (stream.url === id && stream.name !== '') {
        return stream.name;
      }
    }
  }

  // 4. Last resort: the raw id (equals displayName in the remaining cases).
  if (id !== '') {
    return id;
  }

  // Nothing usable.
  if (displayName !== '') {
    return displayName;
  }

  return null;
}

/**
 * Convenience wrapper that never returns null - returns the empty string when
 * the source carries no usable information. Intended for direct use in
 * templates.
 *
 * When a friendly name is resolved from settings, the returned string is the
 * friendly name. Otherwise it returns the raw `source.id` (or an empty string
 * when even the id is missing).
 */
export function getAudioSourceDisplayFallback(
  source: SourceInfo | null | undefined,
  audioSources: AudioSourceConfig[] | undefined,
  rtspStreams: StreamConfig[] | undefined
): string {
  const resolved = getFriendlyAudioSourceName(source, audioSources, rtspStreams);
  if (resolved != null) {
    return resolved;
  }
  return source?.id ?? '';
}
