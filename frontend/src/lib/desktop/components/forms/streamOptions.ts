import { t } from '$lib/i18n';
import { api } from '$lib/utils/api';
import { validateProtocolURL } from '$lib/utils/security';
import { StreamTypes, type ChannelAnalysis, type StreamType } from '$lib/stores/settings';

/** Longest stream URL the editors accept. */
export const MAX_STREAM_URL_LENGTH = 2048;

/**
 * Longest stream name the server accepts, in UTF-8 bytes (MaxStreamNameLength,
 * internal/conf/validate_audio.go:168, compared with len at :252). The fields
 * keep maxlength as a soft cap in characters and check the bytes with
 * isStreamNameTooLong.
 */
export const MAX_STREAM_NAME_LENGTH = 64;

/** True when the name is longer than the server accepts, counted in UTF-8 bytes. */
export function isStreamNameTooLong(name: string): boolean {
  return new TextEncoder().encode(name).length > MAX_STREAM_NAME_LENGTH;
}

/** The reason a name is too long, for the add form and the card. */
export function streamNameTooLongMessage(): string {
  return t('settings.audio.streams.errors.nameTooLong', { max: MAX_STREAM_NAME_LENGTH });
}

/** Rate a stream is assumed to deliver until a test reports its own. */
export const DEFAULT_SAMPLE_RATE = 48000;

export const streamTypeOptions = [
  { value: 'rtsp', label: 'RTSP' },
  { value: 'http', label: 'HTTP' },
  { value: 'hls', label: 'HLS' },
  { value: 'rtmp', label: 'RTMP' },
  { value: 'udp', label: 'UDP/RTP' },
];

export const transportOptions = [
  { value: 'tcp', label: 'TCP' },
  { value: 'udp', label: 'UDP' },
];

export function getChannelModeOptions() {
  return [
    { value: 'downmix', label: t('settings.audio.streams.channelMode.downmix') },
    { value: 'left', label: t('settings.audio.streams.channelMode.left') },
    { value: 'right', label: t('settings.audio.streams.channelMode.right') },
  ];
}

export function getMediaModeOptions() {
  return [
    { value: 'full-stream', label: t('settings.audio.streams.mediaMode.fullStream') },
    { value: 'auto', label: t('settings.audio.streams.mediaMode.auto') },
    { value: 'audio-only', label: t('settings.audio.streams.mediaMode.audioOnly') },
  ];
}

export async function analyzeStreamChannels(url: string): Promise<ChannelAnalysis> {
  return api.post<ChannelAnalysis>('/api/v2/streams/analyze-channels', {
    url: url.trim(),
  });
}

/** The stream type a URL names by its scheme or extension; RTSP when it names none. */
export function detectStreamType(url: string): StreamType {
  const urlLower = url.toLowerCase();
  if (urlLower.startsWith('rtsp://') || urlLower.startsWith('rtsps://')) return 'rtsp';
  if (urlLower.startsWith('rtmp://') || urlLower.startsWith('rtmps://')) return 'rtmp';
  if (urlLower.startsWith('udp://') || urlLower.startsWith('rtp://')) return 'udp';
  if (urlLower.includes('.m3u8')) return 'hls';
  if (urlLower.startsWith('http://') || urlLower.startsWith('https://')) return 'http';
  return 'rtsp';
}

/** URL schemes each stream type accepts. */
function streamProtocols(type: StreamType): string[] {
  switch (type) {
    case 'rtmp':
      return ['rtmp', 'rtmps'];
    case 'http':
    case 'hls':
      return ['http', 'https'];
    case 'udp':
      return ['udp', 'rtp'];
    case 'rtsp':
    default:
      return ['rtsp', 'rtsps'];
  }
}

/** True when the URL is well formed and uses a scheme of the stream type. */
export function isValidStreamUrl(url: string, type: StreamType): boolean {
  return validateProtocolURL(url, streamProtocols(type), MAX_STREAM_URL_LENGTH);
}

/** True for the stream types that take a transport (TCP or UDP). */
export function streamHasTransport(type: StreamType): boolean {
  return type === 'rtsp' || type === 'rtmp';
}

/** The stream type a select option value names, or undefined for an unknown value. */
export function toStreamType(value: string | string[]): StreamType | undefined {
  return Object.values(StreamTypes).find(type => type === value);
}
