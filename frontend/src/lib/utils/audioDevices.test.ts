import { describe, it, expect } from 'vitest';
import { deviceValue, deviceMatches, deviceLabel, type AudioDevice } from './audioDevices';

const usbA: AudioDevice = {
  index: 0,
  name: 'USB Audio Device',
  id: ':1,0',
  stableId: 'usb-path:usb-xhci-hcd.0-1',
  busPath: 'usb-xhci-hcd.0-1',
};
const usbB: AudioDevice = {
  index: 1,
  name: 'USB Audio Device',
  id: ':2,0',
  stableId: 'usb-path:usb-xhci-hcd.0-2',
  busPath: 'usb-xhci-hcd.0-2',
};
const builtin: AudioDevice = { index: 2, name: 'HDA Intel PCH', id: ':0,0' };

describe('deviceValue', () => {
  it('prefers the stable USB token', () => {
    expect(deviceValue(usbA)).toBe('usb-path:usb-xhci-hcd.0-1');
  });
  it('falls back to the ALSA id when no stable token', () => {
    expect(deviceValue(builtin)).toBe(':0,0');
  });
});

describe('deviceMatches', () => {
  it('matches a saved stable token', () => {
    expect(deviceMatches(usbA, 'usb-path:usb-xhci-hcd.0-1')).toBe(true);
  });
  it('matches a saved legacy ALSA id (existing config)', () => {
    expect(deviceMatches(usbA, ':1,0')).toBe(true);
  });
  it('does not match a different device', () => {
    expect(deviceMatches(usbA, ':2,0')).toBe(false);
    expect(deviceMatches(usbA, 'usb-path:usb-xhci-hcd.0-2')).toBe(false);
  });
  it('matches a device without a stable token by id only', () => {
    expect(deviceMatches(builtin, ':0,0')).toBe(true);
    expect(deviceMatches(builtin, '')).toBe(false);
  });
});

describe('deviceLabel', () => {
  it('shows only the name when the device name is unique', () => {
    expect(deviceLabel(usbA, [usbA, builtin])).toBe('USB Audio Device');
  });
  it('appends the bus path only when names collide', () => {
    expect(deviceLabel(usbA, [usbA, usbB])).toBe('USB Audio Device (usb-xhci-hcd.0-1)');
    expect(deviceLabel(usbB, [usbA, usbB])).toBe('USB Audio Device (usb-xhci-hcd.0-2)');
  });
  it('shows just the name when names collide but no bus path is available', () => {
    const noPathA: AudioDevice = { index: 0, name: 'Mic', id: ':1,0' };
    const noPathB: AudioDevice = { index: 1, name: 'Mic', id: ':2,0' };
    expect(deviceLabel(noPathA, [noPathA, noPathB])).toBe('Mic');
  });
});
