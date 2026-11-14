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
      stats: {
        scientificName: detection.scientificName,
        count: correct,
        firstHeard: null,
        lastHeard: null,
        avgConfidence: null,
        maxConfidence: null,
        reviews: { correct, falsePositive },
      },
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

  it('warns when exactly half of the reviews were false positives', () => {
    renderWithReviews(2, 2);
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

describe('SpeciesStatsCard daily counts', () => {
  afterEach(() => cleanup());

  // Regression: counts that did not load were drawn as an empty chart with a total of 0.
  it('shows the 30 day total as not recorded, and no chart, when the counts did not load', () => {
    const { container } = render(SpeciesStatsCard, {
      props: { detection, speciesName: 'Eurasian Woodcock', dailyDays: 30, daily: null },
    });

    const total = screen.getByText('detections.detail.stats.windowTotal').nextElementSibling;
    expect(total?.textContent.trim()).toBe('-');
    expect(container.querySelector('.dd-daily')).toBeNull();
  });

  it('draws zero days as bars when the counts loaded empty', () => {
    const { container } = render(SpeciesStatsCard, {
      props: { detection, speciesName: 'Eurasian Woodcock', dailyDays: 30, daily: { data: [] } },
    });

    expect(container.querySelectorAll('.dd-daily-bar')).toHaveLength(30);
  });
});
