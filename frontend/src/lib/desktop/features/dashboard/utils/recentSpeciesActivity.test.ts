import { describe, expect, it } from 'vitest';
import { validRecentSpeciesActivities } from './recentSpeciesActivity';

const validActivity = {
  scientific_name: 'Turdus migratorius',
  common_name: 'American Robin',
  species_code: 'amerob',
  count: 4,
  latest_heard_at: '2026-05-26T08:42:00-04:00',
  latest_confidence: 0.91,
  max_confidence: 0.94,
  avg_confidence: 0.83,
  confidence_trend: [0, 0.72, 0.91],
  trend_start: '2026-05-26T04:42:00-04:00',
  trend_hours: 4,
  score: 0.86,
  latest_detection_id: 15573,
  thumbnail_url: '/ui/assets/bird-placeholder.svg',
};

describe('validRecentSpeciesActivities', () => {
  it('keeps valid rows when another row is malformed', () => {
    expect(
      validRecentSpeciesActivities([
        validActivity,
        { ...validActivity, latest_detection_id: 'not-a-number' },
      ])
    ).toEqual([validActivity]);
  });

  it('returns an empty array for a non-array response', () => {
    expect(validRecentSpeciesActivities({ data: [validActivity] })).toEqual([]);
  });
});
