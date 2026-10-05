/**
 * Pure logic of the wizard's audio source step: which choice to preselect and
 * which settings payloads a choice produces.
 *
 * The server replaces arrays wholesale on a section PATCH, so every payload is
 * built from the stored arrays with at most one entry changed or appended; the
 * stored data is never mutated. A fresh install has no streams and one template
 * sound card, so the wizard creates exactly one stream; on a rerun it edits the
 * stream it prefilled and never drops the others.
 */
import {
  defaultQuietHoursConfig,
  StreamTypes,
  type AudioSettings,
  type AudioSourceConfig,
  type RTSPSettings,
  type StreamConfig,
} from '$lib/stores/settings';
import type { SettingsSectionPayloads } from '$lib/utils/settingsApi';
import { deviceMatches, deviceValue, type AudioDevice } from '$lib/utils/audioDevices';

/** Name of the sound card entry the wizard creates (the install template's name). */
export const WIZARD_SOUND_CARD_NAME = 'Sound Card 1';
/** Base of the names of streams the wizard creates: "Stream 1", "Stream 2", ... */
const WIZARD_STREAM_NAME_BASE = 'Stream';
/** Transport of the streams the wizard creates. */
const WIZARD_STREAM_TRANSPORT = 'tcp';
/** URL prefixes the backend accepts for an RTSP stream (validateURLScheme in validate_audio.go). */
const RTSP_URL_PREFIXES = ['rtsp://', 'rtsps://'];

export type AudioSourceType = 'soundcard' | 'rtsp';
export type AudioPayload = SettingsSectionPayloads['audio'];
export type RtspPayload = SettingsSectionPayloads['rtsp'];

/** What the step shows when it opens. */
export interface InitialAudioChoice {
  sourceType: AudioSourceType;
  /** Device string of the first configured sound card, or empty. */
  savedDevice: string;
  /** URL of the first rtsp-type stream, or null when there is none. */
  primaryStreamUrl: string | null;
}

/**
 * Whether a URL is acceptable for an RTSP stream: trimmed, an rtsp or rtsps
 * scheme (any case), at least one character after it and no whitespace. Stricter
 * than the backend, which checks only the prefix, so a bare "rtsp://" is refused.
 */
export function isRtspUrl(url: string): boolean {
  const trimmed = url.trim();
  const lower = trimmed.toLowerCase();
  const prefix = RTSP_URL_PREFIXES.find(p => lower.startsWith(p));
  if (prefix === undefined) return false;
  const rest = trimmed.slice(prefix.length);
  return rest.length > 0 && !/\s/.test(rest);
}

function sourcesOf(audio: AudioSettings | undefined): AudioSourceConfig[] {
  return Array.isArray(audio?.sources) ? audio.sources : [];
}

function streamsOf(rtsp: RTSPSettings | undefined): StreamConfig[] {
  return Array.isArray(rtsp?.streams) ? rtsp.streams : [];
}

/** Copy of the list with the entry at `index` merged with `patch`; the list itself is untouched. */
function replaceAt<T extends object>(list: T[], index: number, patch: Partial<T>): T[] {
  return list.map((item, i) => (i === index ? { ...item, ...patch } : item));
}

function hasLegacySource(audio: AudioSettings | undefined): boolean {
  return typeof audio?.source === 'string' && audio.source.trim() !== '';
}

function firstRtspStream(streams: StreamConfig[]): StreamConfig | undefined {
  return streams.find(s => s.type === StreamTypes.RTSP);
}

/**
 * Starting state of the step. With sound cards configured it opens on the sound
 * card option; with none and an rtsp stream it opens on the stream option. The
 * legacy `audio.source` is never used: the backend has already migrated it when
 * `sources` is empty, and ignores it otherwise.
 */
export function initialAudioChoice(
  audio: AudioSettings | undefined,
  rtsp: RTSPSettings | undefined
): InitialAudioChoice {
  const sources = sourcesOf(audio);
  const primary = firstRtspStream(streamsOf(rtsp));
  const primaryStreamUrl = primary?.url ?? null;
  if (sources.length > 0) {
    return { sourceType: 'soundcard', savedDevice: sources[0].device, primaryStreamUrl };
  }
  return { sourceType: primary ? 'rtsp' : 'soundcard', savedDevice: '', primaryStreamUrl };
}

/** The listed device that a saved device string refers to, by stable or legacy id. */
export function findDevice(devices: AudioDevice[], saved: string): AudioDevice | undefined {
  if (saved === '') return undefined;
  return devices.find(d => deviceMatches(d, saved));
}

/** The first name "Stream 1", "Stream 2", ... not used by any stream, ignoring case. */
export function firstFreeStreamName(streams: StreamConfig[]): string {
  const taken = new Set(streams.map(s => s.name.toLowerCase()));
  for (let n = 1; ; n++) {
    const candidate = `${WIZARD_STREAM_NAME_BASE} ${n}`;
    if (!taken.has(candidate.toLowerCase())) return candidate;
  }
}

/** The `audio` payload for choosing `device`, or null when the stored sources already match. */
function soundCardAudioPayload(
  audio: AudioSettings | undefined,
  device: AudioDevice
): AudioPayload | null {
  const sources = sourcesOf(audio);
  const value = deviceValue(device);
  const matchIndex = sources.findIndex(s => deviceMatches(device, s.device));

  if (matchIndex >= 0) {
    // Already configured. A legacy id is upgraded to the stable token in place;
    // the entry's position is kept so per-source identity is not disturbed.
    // eslint-disable-next-line security/detect-object-injection -- matchIndex comes from findIndex on sources
    if (sources[matchIndex].device !== value) {
      return { sources: replaceAt(sources, matchIndex, { device: value }), source: '' };
    }
    return hasLegacySource(audio) ? { source: '' } : null;
  }
  if (sources.length > 0) {
    // A sample rate chosen for the previous device may not suit this one
    const first: AudioSourceConfig = { ...sources[0], device: value };
    delete first.sampleRate;
    return { sources: [first, ...sources.slice(1)], source: '' };
  }
  return {
    sources: [
      {
        name: WIZARD_SOUND_CARD_NAME,
        device: value,
        gain: 0,
        models: [],
        quietHours: { ...defaultQuietHoursConfig },
      },
    ],
    source: '',
  };
}

/**
 * Payloads for choosing a sound card. `audio` always carries `source: ''` when
 * it can leave `sources` unchanged or empty, because the backend recreates a
 * sound card from a non-empty legacy `source` whenever `sources` is empty.
 * `streamOwnedByStep` says the step opened on the stream option or has saved a
 * stream itself, in which case the stream it showed (`primaryStreamUrl`) is
 * turned off, not deleted.
 * Either payload is null when the stored settings already match the choice.
 */
export function soundCardPayloads(
  audio: AudioSettings | undefined,
  rtsp: RTSPSettings | undefined,
  device: AudioDevice,
  streamOwnedByStep: boolean,
  primaryStreamUrl: string | null
): { audio: AudioPayload | null; rtsp: RtspPayload | null } {
  let rtspPayload: RtspPayload | null = null;
  if (streamOwnedByStep && primaryStreamUrl !== null) {
    const streams = streamsOf(rtsp);
    const index = streams.findIndex(s => s.url === primaryStreamUrl);
    // eslint-disable-next-line security/detect-object-injection -- index comes from findIndex on streams
    if (index >= 0 && streams[index].enabled) {
      rtspPayload = { streams: replaceAt(streams, index, { enabled: false }) };
    }
  }

  return { audio: soundCardAudioPayload(audio, device), rtsp: rtspPayload };
}

/**
 * Payloads for choosing a stream (`url` is trimmed). A stream whose URL is
 * already stored is reused (and enabled); otherwise the stream the step showed
 * (`primaryStreamUrl`) gets the new URL; otherwise one stream is appended.
 * Every sound card is cleared, with the legacy source, so the stream is what
 * captures. Either payload is null when the stored settings already match.
 */
export function streamPayloads(
  audio: AudioSettings | undefined,
  rtsp: RTSPSettings | undefined,
  url: string,
  primaryStreamUrl: string | null
): { rtsp: RtspPayload | null; audio: AudioPayload | null } {
  const streams = streamsOf(rtsp);

  let rtspPayload: RtspPayload | null;
  const sameIndex = streams.findIndex(s => s.url === url);
  const primaryIndex =
    primaryStreamUrl === null ? -1 : streams.findIndex(s => s.url === primaryStreamUrl);
  if (sameIndex >= 0) {
    // eslint-disable-next-line security/detect-object-injection -- sameIndex comes from findIndex on streams
    rtspPayload = streams[sameIndex].enabled
      ? null
      : { streams: replaceAt(streams, sameIndex, { enabled: true }) };
  } else if (primaryIndex >= 0) {
    rtspPayload = { streams: replaceAt(streams, primaryIndex, { url, enabled: true }) };
  } else {
    rtspPayload = {
      streams: [
        ...streams,
        {
          name: firstFreeStreamName(streams),
          url,
          enabled: true,
          type: StreamTypes.RTSP,
          transport: WIZARD_STREAM_TRANSPORT,
        },
      ],
    };
  }

  const audioPayload: AudioPayload | null =
    sourcesOf(audio).length > 0 || hasLegacySource(audio) ? { sources: [], source: '' } : null;

  return { rtsp: rtspPayload, audio: audioPayload };
}
