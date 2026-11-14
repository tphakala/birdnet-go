import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import ActivityLog from './ActivityLog.svelte';
import type { DetectionDetailData } from './detailTypes';

const base: DetectionDetailData = {
  id: 7,
  date: '2026-11-14',
  time: '02:57:03',
  timestamp: '2026-11-14T02:57:03+02:00',
  beginTime: '2026-11-14T02:57:00+02:00',
  endTime: '2026-11-14T02:57:03+02:00',
  speciesCode: 'sonthr',
  scientificName: 'Turdus philomelos',
  commonName: 'Song Thrush',
  confidence: 0.87,
  verified: 'unverified',
  locked: false,
};

function renderLog(detection: DetectionDetailData) {
  render(ActivityLog, {
    props: { detection, speciesName: 'Song Thrush', canReview: false, onChanged: vi.fn() },
  });
  return screen.getAllByRole('listitem').map(item => item.textContent);
}

describe('ActivityLog entries', () => {
  afterEach(() => cleanup());

  it('lists the first review and the latest save, oldest first, after the detection', () => {
    const entries = renderLog({
      ...base,
      verified: 'correct',
      firstReviewedAt: '2026-11-14T08:00:00+02:00',
      lastReviewedAt: '2026-11-14T09:00:00+02:00',
    });

    expect(entries).toHaveLength(3);
    expect(entries[0]).toContain('detections.detail.log.detected');
    expect(entries[1]).toContain('detections.detail.log.firstReviewed');
    // The latest save states the verdict; it does not claim the verdict changed, since
    // any save (a species change, the same verdict again) moves its time.
    expect(entries[2]).toContain('detections.detail.log.reviewed');
  });

  it('adds a species change entry when a reviewer replaced the model’s pick', () => {
    const entries = renderLog({
      ...base,
      verified: 'correct',
      firstReviewedAt: '2026-11-14T08:00:00+02:00',
      lastReviewedAt: '2026-11-14T08:00:00+02:00',
      originalSpecies: {
        scientificName: 'Turdus merula',
        commonName: 'Blackbird',
        speciesCode: 'eurbla',
      },
    });

    expect(entries.some(entry => entry.includes('detections.detail.log.speciesChanged'))).toBe(
      true
    );
  });

  it('adds no species change entry once the species is back to the model’s pick', () => {
    const entries = renderLog({
      ...base,
      originalSpecies: {
        scientificName: 'Turdus philomelos',
        commonName: 'Song Thrush',
        speciesCode: 'sonthr',
      },
    });

    expect(entries.some(entry => entry.includes('detections.detail.log.speciesChanged'))).toBe(
      false
    );
  });
});

describe('ActivityLog note while saving', () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  // Regression: text typed while a note was being saved was wiped when the save finished,
  // though only the earlier text had been sent.
  it('holds the note box read-only until the save answers', async () => {
    let answer: ((_response: Response) => void) | undefined;
    vi.stubGlobal(
      'fetch',
      vi.fn(
        () =>
          new Promise<Response>(resolve => {
            answer = resolve;
          })
      )
    );
    render(ActivityLog, {
      props: { detection: base, speciesName: 'Song Thrush', canReview: true, onChanged: vi.fn() },
    });
    const box = screen.getByLabelText<HTMLTextAreaElement>('detections.detail.log.addNote');
    await fireEvent.input(box, { target: { value: 'First note' } });
    await fireEvent.click(screen.getByRole('button', { name: 'detections.detail.log.saveNote' }));

    expect(box.readOnly).toBe(true);
    answer?.(new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }));
  });
});
