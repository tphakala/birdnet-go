import { describe, expect, it } from 'vitest';
import {
  isDailyCounts,
  isDetectionDetail,
  isDetectionNeighbors,
  isDetectionPredictions,
  isDetectionWeather,
  isHourlyBuckets,
  isSpeciesRarity,
  isSpeciesStats,
  isSunTimes,
  isTaxonomy,
  parseImageAttribution,
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
    [
      'detection original species',
      isDetectionDetail,
      {
        ...detection,
        originalSpecies: { scientificName: 'A b', commonName: 'Ab', speciesCode: 'ab' },
      },
      { ...detection, originalSpecies: {} },
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
      'species statistics',
      isSpeciesStats,
      {
        scientificName: 'A b',
        count: 3,
        firstHeard: '2026-01-01T00:00:00Z',
        lastHeard: null,
        avgConfidence: 0.5,
        maxConfidence: null,
        reviews: { correct: 1, falsePositive: 0 },
      },
      {
        scientificName: 'A b',
        count: 3,
        firstHeard: null,
        lastHeard: null,
        avgConfidence: null,
        maxConfidence: null,
        reviews: { correct: 1 },
      },
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

  const contribution = {
    modelName: 'BirdNET',
    modelVersion: '2.4',
    hitCount: 2,
    modelVariant: 'fp32',
    modelType: 'bird',
    minConfidence: 0.4,
    maxConfidence: 0.9,
    meanConfidence: 0.6,
    firstHitAt: '2026-11-14T02:57:00+02:00',
    lastHitAt: '2026-11-14T02:57:03+02:00',
  };

  // The Models card reads every one of these fields; each must have its type when present.
  it.each([
    ['a model type that is not text', { modelType: 3 }],
    ['a lowest confidence that is not a number', { minConfidence: '0.4' }],
    ['a highest confidence that is not a number', { maxConfidence: '0.9' }],
    ['a mean confidence that is not a number', { meanConfidence: null }],
  ])('refuses a model contribution with %s', (_name, change) => {
    expect(isDetectionDetail({ ...detection, modelContributions: [contribution] })).toBe(true);
    expect(
      isDetectionDetail({ ...detection, modelContributions: [{ ...contribution, ...change }] })
    ).toBe(false);
  });

  const hourly = {
    time: '2026-11-14T03:00:00+02:00',
    temperature: 2,
    pressure: 1006,
    wind_gust: 5.3,
    precipitation: 0.3,
    precipitation_type: 'snow',
  };

  // The conditions card reads these weather fields; each must have its type when present.
  it.each([
    ['a gust that is not a number', { wind_gust: '5.3' }],
    ['a pressure that is not a number', { pressure: '1006' }],
    ['a precipitation type that is not text', { precipitation_type: 1 }],
  ])('refuses an hour of weather with %s', (_name, change) => {
    expect(isDetectionWeather({ hourly })).toBe(true);
    expect(isDetectionWeather({ hourly: { ...hourly, ...change } })).toBe(false);
  });

  const detectionWeather = {
    weatherIcon: '13n',
    description: 'light snow',
    temperature: 1,
    windSpeed: 2.5,
    windGust: 5.3,
    humidity: 98,
    units: 'metric',
    moonPhaseName: 'Waxing Crescent',
  };

  // The detection's own weather block fills in what the hourly answer leaves out.
  it.each([
    ['a wind speed that is not a number', { windSpeed: '2.5' }],
    ['units the formatters do not know', { units: 'kelvin' }],
    ['a moon phase that is not text', { moonPhaseName: 4 }],
  ])('refuses a detection whose weather block has %s', (_name, change) => {
    expect(isDetectionDetail({ ...detection, weather: detectionWeather })).toBe(true);
    expect(isDetectionDetail({ ...detection, weather: { ...detectionWeather, ...change } })).toBe(
      false
    );
  });

  const goodStats = {
    scientificName: 'A b',
    count: 3,
    firstHeard: '2026-01-01T00:00:00Z',
    lastHeard: '2026-02-01T00:00:00Z',
    avgConfidence: 0.5,
    maxConfidence: 0.9,
    reviews: { correct: 1, falsePositive: 0 },
  };

  // Each field on its own, so every check of the guard is needed: the page reads all of them.
  it.each([
    ['a scientific name that is not text', { scientificName: 3 }],
    ['a count that is not a number', { count: '3' }],
    ['a first heard time that is not text', { firstHeard: 5 }],
    ['a last heard time that is not text', { lastHeard: false }],
    ['an average confidence that is not a number', { avgConfidence: '0.5' }],
    ['a highest confidence that is not a number', { maxConfidence: '0.9' }],
    ['no review counts', { reviews: null }],
    ['a correct count that is not a number', { reviews: { correct: '1', falsePositive: 0 } }],
    ['a false positive count that is missing', { reviews: { correct: 1 } }],
    // Times and confidences are null for a species without detections, never absent.
    ['no first heard time', { firstHeard: undefined }],
    ['no last heard time', { lastHeard: undefined }],
    ['no average confidence', { avgConfidence: undefined }],
    ['no highest confidence', { maxConfidence: undefined }],
  ])('refuses species statistics with %s', (_name, change) => {
    expect(isSpeciesStats(goodStats)).toBe(true);
    expect(isSpeciesStats({ ...goodStats, ...change })).toBe(false);
  });
});
