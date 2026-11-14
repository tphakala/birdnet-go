import { describe, expect, it } from 'vitest';
import {
  isDailyCounts,
  isDetectionDetail,
  isDetectionNeighbors,
  isDetectionPredictions,
  isDetectionWeather,
  isHourlyBuckets,
  isSpeciesRarity,
  isSpeciesSummary,
  isSunTimes,
  isTaxonomy,
  parseImageAttribution,
  searchTotal,
} from './detailGuards';

const detection = {
  id: 7,
  date: '2026-11-14',
  time: '02:57:03',
  scientificName: 'Turdus merula',
  commonName: 'Blackbird',
  confidence: 0.9,
  verified: 'unverified',
  locked: false,
};

// Every guard refuses what is not an object, and what has a read field of the wrong type.
describe('shape guards', () => {
  const cases: [string, (_value: unknown) => boolean, unknown, unknown][] = [
    ['detection', isDetectionDetail, detection, { ...detection, id: '7' }],
    ['detection verdict', isDetectionDetail, detection, { ...detection, verified: 'maybe' }],
    [
      'detection comments',
      isDetectionDetail,
      { ...detection, comments: [{ id: 1, entry: 'x', createdAt: '2026-11-14T08:00:00Z' }] },
      { ...detection, comments: [{ id: 1 }] },
    ],
    ['weather', isDetectionWeather, { hourly: { time: 't', temperature: 3 } }, { hourly: 'warm' }],
    ['sun', isSunTimes, { date: '2026-11-14', sunrise: 'x' }, { sunrise: 'x' }],
    [
      'neighbours',
      isDetectionNeighbors,
      { detectionId: 5, previous: null, next: 6, previousSameSpecies: 2, nextSameSpecies: null },
      { detectionId: 5, previous: '4' },
    ],
    [
      'predictions',
      isDetectionPredictions,
      {
        detectionId: 5,
        modelPredictions: [
          { modelName: 'BirdNET', predictions: [{ scientificName: 'A b', confidence: 0.2 }] },
        ],
      },
      { detectionId: 5, modelPredictions: [{ modelName: 'BirdNET', predictions: [{}] }] },
    ],
    [
      'daily',
      isDailyCounts,
      { data: [{ date: 'd', count: 1 }], total: 1 },
      { data: [{ date: 'd' }] },
    ],
    ['hourly', isHourlyBuckets, [{ hour: 1, count: 2 }], [{ hour: 1 }]],
    [
      'rarity',
      isSpeciesRarity,
      { status: 'rare', score: 0.1, location_based: false },
      { status: 'rare', score: '0.1', location_based: false },
    ],
    ['taxonomy', isTaxonomy, { family: 'Turdidae' }, { family: 3 }],
    [
      'summary row',
      isSpeciesSummary,
      { scientific_name: 'A b', count: 3 },
      { scientific_name: 'A b' },
    ],
  ];

  it.each(cases)('accepts a good %s and refuses a reshaped one', (name, guard, good, bad) => {
    expect(guard(good)).toBe(true);
    expect(guard(bad)).toBe(false);
    expect(guard(null)).toBe(false);
    // An empty list is a good list of hourly buckets; for every other answer it is the wrong shape.
    expect(guard([])).toBe(name === 'hourly');
  });

  it('reads a missing attribution field as empty and a non-object as none', () => {
    expect(parseImageAttribution({ authorName: 'A' })).toEqual({
      authorName: 'A',
      authorURL: '',
      licenseName: '',
      licenseURL: '',
      sourceProvider: '',
    });
    expect(parseImageAttribution(null)).toBeNull();
  });

  it('reads the search total, or 0 when the answer has none', () => {
    expect(searchTotal({ total: 4 })).toBe(4);
    expect(searchTotal({ total: '4' })).toBe(0);
    expect(searchTotal(null)).toBe(0);
  });
});
