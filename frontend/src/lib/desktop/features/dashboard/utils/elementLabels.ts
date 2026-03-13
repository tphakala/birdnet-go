import { t } from '$lib/i18n';

const elementLabelKeys = new Map<string, string>([
  ['banner', 'dashboard.elements.banner'],
  ['daily-summary', 'dashboard.elements.dailySummary'],
  ['currently-hearing', 'dashboard.elements.currentlyHearing'],
  ['detections-grid', 'dashboard.elements.detectionsGrid'],
  ['video-embed', 'dashboard.elements.videoEmbed'],
]);

export function getElementLabel(type: string): string {
  const key = elementLabelKeys.get(type);
  return key ? t(key) : type;
}
