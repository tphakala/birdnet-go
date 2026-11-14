import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen } from '@testing-library/svelte';
import SpeciesStatsCard from './SpeciesStatsCard.svelte';
import type { DetectionDetailData } from './detailTypes';

const detection: DetectionDetailData = {
  id: 7,
  date: '2026-11-14',
  time: '02:57:03',
  beginTime: '2026-11-14T02:57:00+02:00',
  endTime: '2026-11-14T02:57:03+02:00',
  speciesCode: 'eurwoo',
  scientificName: 'Scolopax rusticola',
  commonName: 'Eurasian Woodcock',
  confidence: 0.87,
  verified: 'unverified',
  locked: false,
};

function renderWithReviews(correct: number, falsePositive: number) {
  render(SpeciesStatsCard, {
    props: {
      detection,
      speciesName: 'Eurasian Woodcock',
      dailyDays: 30,
      reviews: { correct, falsePositive },
    },
  });
}

const warning = () => screen.queryByText(/detections\.detail\.stats\.oftenRejected/);

describe('SpeciesStatsCard often-rejected warning', () => {
  afterEach(() => cleanup());

  it('warns when at least half of three or more reviews were false positives', () => {
    renderWithReviews(1, 2);
    expect(warning()).not.toBeNull();
  });

  it('does not warn on fewer than three reviews', () => {
    renderWithReviews(0, 2);
    expect(warning()).toBeNull();
  });

  it('does not warn when most reviews were correct', () => {
    renderWithReviews(3, 2);
    expect(warning()).toBeNull();
  });
});
