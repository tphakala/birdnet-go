import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import DetailNavBar from './DetailNavBar.svelte';
import type { DetectionNeighbors } from './detailTypes';

function renderBar(neighbors: DetectionNeighbors | null) {
  render(DetailNavBar, {
    props: {
      speciesName: 'Blackbird',
      date: '2026-11-14',
      time: '02:57:03',
      neighbors,
      available: true,
    },
  });
}

describe('DetailNavBar step links', () => {
  afterEach(() => cleanup());

  // Regression: while the neighbours were still being looked up, every step link was named
  // as if there were no older or newer detection.
  it('keeps their own names, blocked, while the neighbours are not known', () => {
    renderBar(null);

    const older = screen.getByRole('link', { name: 'detections.detail.nav.older' });
    expect(older).toHaveAttribute('aria-disabled', 'true');
    expect(older).not.toHaveAttribute('href');
    expect(screen.queryByRole('link', { name: 'detections.detail.nav.noOlder' })).toBeNull();
  });

  it('names a step with nowhere to go once the neighbours are known', () => {
    renderBar({
      detectionId: 5,
      previous: null,
      next: 6,
      previousSameSpecies: null,
      nextSameSpecies: null,
    });

    expect(screen.getByRole('link', { name: 'detections.detail.nav.noOlder' })).toHaveAttribute(
      'aria-disabled',
      'true'
    );
    expect(screen.getByRole('link', { name: 'detections.detail.nav.newer' })).toHaveAttribute(
      'href',
      '/ui/detections/6'
    );
  });

  // Regression: a step by mouse left focus where it was (Safari does not focus a clicked
  // link), so it was lost when the page made the previous detection inert.
  it('takes focus when clicked', async () => {
    renderBar({
      detectionId: 5,
      previous: 4,
      next: null,
      previousSameSpecies: null,
      nextSameSpecies: null,
    });
    const older = screen.getByRole('link', { name: 'detections.detail.nav.older' });

    await fireEvent.click(older);

    expect(older).toHaveFocus();
  });
});
