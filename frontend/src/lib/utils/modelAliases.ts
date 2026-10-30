/**
 * Cross-references between a settings form and the model aliases the server
 * has enabled (models.enabled).
 */
import type { SettingsFormData } from '$lib/stores/settings';
import { isPlainObject } from './security';

/** Returns the lowercased aliases of a models.enabled list, or null when settings carry no such list. */
function enabledModelAliases(settings: SettingsFormData): Set<string> | null {
  const models: unknown = Reflect.get(settings, 'models');
  const enabled = isPlainObject(models) ? models.enabled : undefined;
  if (!Array.isArray(enabled)) return null;
  return new Set(
    enabled.filter((v): v is string => typeof v === 'string').map(v => v.toLowerCase())
  );
}

/**
 * Removes from the audio source and RTSP stream model lists the aliases the server dropped
 * from models.enabled between base and fresh (an uninstall). Without this an
 * edited list would win a rebase and write the removed alias back. The server
 * compares aliases case-insensitively, so this does too. Inputs are not mutated.
 */
export function pruneRemovedModelAliases(
  form: SettingsFormData,
  base: SettingsFormData,
  fresh: SettingsFormData
): SettingsFormData {
  const baseEnabled = enabledModelAliases(base);
  const freshEnabled = enabledModelAliases(fresh);
  if (!baseEnabled || !freshEnabled) return form;
  const removed = new Set([...baseEnabled].filter(alias => !freshEnabled.has(alias)));
  if (removed.size === 0) return form;

  const prune = <T extends { models?: string[] }>(items: T[]): T[] =>
    Array.isArray(items)
      ? items.map(item =>
          Array.isArray(item.models)
            ? { ...item, models: item.models.filter(alias => !removed.has(alias.toLowerCase())) }
            : item
        )
      : items;
  const realtime = form.realtime;
  if (!realtime) return form;
  return {
    ...form,
    realtime: {
      ...realtime,
      ...(realtime.audio && {
        audio: { ...realtime.audio, sources: prune(realtime.audio.sources) },
      }),
      ...(realtime.rtsp && { rtsp: { ...realtime.rtsp, streams: prune(realtime.rtsp.streams) } }),
    },
  };
}
