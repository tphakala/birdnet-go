import { describe, expect, it } from 'vitest';
import { getMoonPhaseIconName } from './weather';

describe('getMoonPhaseIconName', () => {
  it('maps every phase name the API sends to its icon', () => {
    expect(getMoonPhaseIconName('New Moon')).toBe('moon-new');
    expect(getMoonPhaseIconName('Waxing Crescent')).toBe('moon-waxing-crescent');
    expect(getMoonPhaseIconName('First Quarter')).toBe('moon-first-quarter');
    expect(getMoonPhaseIconName('Waxing Gibbous')).toBe('moon-waxing-gibbous');
    expect(getMoonPhaseIconName('Full Moon')).toBe('moon-full');
    expect(getMoonPhaseIconName('Waning Gibbous')).toBe('moon-waning-gibbous');
    expect(getMoonPhaseIconName('Last Quarter')).toBe('moon-last-quarter');
    expect(getMoonPhaseIconName('Waning Crescent')).toBe('moon-waning-crescent');
  });

  it('falls back to the new moon for a name it does not know', () => {
    expect(getMoonPhaseIconName('Blue Moon')).toBe('moon-new');
  });
});
