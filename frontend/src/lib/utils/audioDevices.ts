/**
 * Helpers for presenting and persisting audio capture device selections.
 *
 * A device is persisted in `realtime.audio.sources[].device`. Historically this
 * was the ALSA card index (":1,0"), which is unstable across reboots for USB
 * devices (GH #3651). The backend now also reports a reboot-stable USB token
 * (`stableId`, e.g. "usb-path:usb-xhci-hcd.0-1"); new selections persist that
 * token while existing ":1,0"/name configs keep working via the backend's
 * legacy match tiers.
 */

export interface AudioDevice {
  index: number;
  name: string;
  id: string;
  /** Reboot-stable USB token to persist in preference to `id` (GH #3651). */
  stableId?: string;
  /** USB bus path for display; present only for USB devices on Linux. */
  busPath?: string;
}

/** The identifier to persist for a device: the stable USB token when available. */
export function deviceValue(device: AudioDevice): string {
  // stableId is omitted (undefined) when no stable identity exists; it is never
  // an empty string, so ?? correctly falls back to the legacy id.
  return device.stableId ?? device.id;
}

/**
 * Whether a saved `device` string refers to this device, matching either the
 * stable token (new configs) or the legacy ALSA id (existing configs).
 */
export function deviceMatches(device: AudioDevice, saved: string): boolean {
  return device.id === saved || (!!device.stableId && device.stableId === saved);
}

/**
 * Dropdown label for a device: its name, with the USB bus path appended only
 * when another enumerated device shares the same name. Identical USB cards are
 * thus distinguishable without showing a kernel bus path to every user.
 */
export function deviceLabel(device: AudioDevice, all: AudioDevice[]): string {
  const ambiguous = all.filter(d => d.name === device.name).length > 1;
  return ambiguous && device.busPath ? `${device.name} (${device.busPath})` : device.name;
}
