import { t } from '$lib/i18n';
import { api } from '$lib/utils/api';
import { Merge, PanelLeft, PanelRight } from '@lucide/svelte';
import type { ChannelAnalysis } from '$lib/stores/settings';

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
    { value: 'downmix', label: t('settings.audio.streams.channelMode.downmix'), icon: Merge },
    { value: 'left', label: t('settings.audio.streams.channelMode.left'), icon: PanelLeft },
    { value: 'right', label: t('settings.audio.streams.channelMode.right'), icon: PanelRight },
  ];
}

export async function analyzeStreamChannels(url: string): Promise<ChannelAnalysis> {
  return api.post<ChannelAnalysis>('/api/v2/streams/analyze-channels', {
    url: url.trim(),
  });
}
