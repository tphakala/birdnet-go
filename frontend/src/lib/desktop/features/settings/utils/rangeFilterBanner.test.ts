import { describe, it, expect } from 'vitest';
import {
  rangeFilterBanner,
  type GeomodelIssue,
  type RangeFilterStatusSummary,
} from './rangeFilterBanner';

const base: RangeFilterStatusSummary = {
  active: true,
  fellBack: false,
  mappedSpecies: 100,
  locationConfigured: true,
  geomodel: null,
  geomodelIssue: 'none',
  reloadFailed: false,
  classifiers: [
    { name: 'BirdNET v2.4', coveredByBackend: true },
    { name: 'Google Perch v2', coveredByBackend: false },
  ],
};

describe('rangeFilterBanner', () => {
  it('returns null without status', () => {
    expect(rangeFilterBanner(null)).toBeNull();
  });

  const issues: [Exclude<GeomodelIssue, 'none'>, string, boolean][] = [
    ['not_installed', 'geomodelNotInstalled', true],
    ['not_selected', 'geomodelNotSelected', false],
    ['load_failed', 'geomodelLoadFailed', true],
  ];

  it.each(issues)('%s: severity follows location and activity', (issue, key, link) => {
    const make = (over: Partial<RangeFilterStatusSummary>) =>
      rangeFilterBanner({ ...base, geomodelIssue: issue, ...over });

    expect(make({ active: false })).toEqual({
      level: 'error',
      key,
      models: 'Google Perch v2',
      showModelsLink: link,
    });
    expect(make({ active: true })?.level).toBe('warning');
    expect(make({ active: false, locationConfigured: false })).toMatchObject({
      level: 'warning',
      key,
    });
  });

  it('reload failure with an active backend warns even without a location', () => {
    expect(rangeFilterBanner({ ...base, reloadFailed: true, locationConfigured: false })).toEqual({
      level: 'warning',
      key: 'reloadFailed',
      models: 'Google Perch v2',
      showModelsLink: false,
    });
  });

  it('is silent without a location when there is no geomodel issue', () => {
    expect(rangeFilterBanner({ ...base, locationConfigured: false, active: false })).toBeNull();
  });

  it.each([
    ['inactive', { active: false }, 'error'],
    ['fellBack', { fellBack: true }, 'warning'],
    ['mappedZero', { geomodel: { version: 'v3.0' }, mappedSpecies: 0 }, 'warning'],
  ] as const)('keeps the existing %s rule', (key, over, level) => {
    expect(rangeFilterBanner({ ...base, ...over })).toMatchObject({ key, level });
  });

  it('is silent when healthy', () => {
    expect(rangeFilterBanner(base)).toBeNull();
  });
});
