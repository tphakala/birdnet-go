import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import ReviewPanel from './ReviewPanel.svelte';
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

function renderPanel(speciesBlockedReason: string, canReview = true) {
  const onWrongSpecies = vi.fn();
  render(ReviewPanel, {
    props: {
      detection,
      speciesName: 'Eurasian Woodcock',
      canReview,
      speciesBlockedReason,
      onWrongSpecies,
      onChanged: vi.fn(),
    },
  });
  return { onWrongSpecies };
}

const wrongSpeciesButton = () =>
  screen.getByRole('button', { name: /detections\.detail\.review\.wrongSpecies/ });

describe('ReviewPanel blocked species change', () => {
  afterEach(() => cleanup());

  it('shows the reason, blocks the button and points it at the reason', async () => {
    const { onWrongSpecies } = renderPanel('detections.detail.review.legacyHelp');

    const button = wrongSpeciesButton();
    expect(button).toHaveAttribute('aria-disabled', 'true');
    const reason = document.getElementById(button.getAttribute('aria-describedby') ?? '');
    expect(reason?.textContent).toContain('detections.detail.review.legacyHelp');

    await fireEvent.click(button);
    expect(onWrongSpecies).not.toHaveBeenCalled();
  });

  it('opens the species dialog when nothing blocks it', async () => {
    const { onWrongSpecies } = renderPanel('');

    const button = wrongSpeciesButton();
    expect(button).not.toHaveAttribute('aria-disabled');
    await fireEvent.click(button);
    expect(onWrongSpecies).toHaveBeenCalledOnce();
  });
});
