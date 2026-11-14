import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import ActivityLog from './ActivityLog.svelte';
import { t } from '$lib/i18n';
import { toastActions } from '$lib/stores/toast';
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

  it('orders the entries by their time, not by kind', () => {
    // Kept at 07:00, before the review at 08:00, though lock entries are listed after reviews.
    const entries = renderLog({
      ...base,
      verified: 'correct',
      locked: true,
      lockedAt: '2026-11-14T07:00:00+02:00',
      firstReviewedAt: '2026-11-14T08:00:00+02:00',
      lastReviewedAt: '2026-11-14T08:00:00+02:00',
    });

    const locked = entries.findIndex(entry => entry.includes('detections.detail.log.locked'));
    const reviewed = entries.findIndex(entry => entry.includes('detections.detail.log.reviewed'));
    expect(locked).toBeGreaterThanOrEqual(0);
    expect(reviewed).toBeGreaterThan(locked);
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
    // The detection entry names what the model heard; the change names both species.
    expect(vi.mocked(t)).toHaveBeenCalledWith('detections.detail.log.detected', {
      species: 'Blackbird',
      confidence: 87,
    });
    expect(vi.mocked(t)).toHaveBeenCalledWith('detections.detail.log.speciesChanged', {
      from: 'Blackbird',
      to: 'Song Thrush',
    });
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

describe('ActivityLog names and icons', () => {
  afterEach(() => {
    cleanup();
    vi.mocked(t).mockClear();
  });

  it('names the model’s pick in the detected entry and both species in the change entry', () => {
    renderLog({
      ...base,
      scientificName: 'Turdus philomelos',
      commonName: 'Song Thrush',
      verified: 'correct',
      firstReviewedAt: '2026-11-14T08:00:00+02:00',
      lastReviewedAt: '2026-11-14T08:00:00+02:00',
      originalSpecies: {
        scientificName: 'Turdus merula',
        commonName: 'Blackbird',
        speciesCode: 'eurbla',
      },
    });

    expect(vi.mocked(t)).toHaveBeenCalledWith('detections.detail.log.detected', {
      species: 'Blackbird',
      confidence: 87,
    });
    expect(vi.mocked(t)).toHaveBeenCalledWith('detections.detail.log.speciesChanged', {
      from: 'Blackbird',
      to: 'Song Thrush',
    });
  });

  // The first verdict is not kept, so the first entry must not claim the current one.
  it('gives the first review a neutral icon and the latest save the verdict’s', () => {
    const { container } = render(ActivityLog, {
      props: {
        detection: {
          ...base,
          verified: 'false_positive',
          firstReviewedAt: '2026-11-14T08:00:00+02:00',
          lastReviewedAt: '2026-11-14T09:00:00+02:00',
        },
        speciesName: 'Song Thrush',
        canReview: false,
        onChanged: vi.fn(),
      },
    });

    const icons = [...container.querySelectorAll('.dd-log-icon')].map(icon => icon.className);
    expect(icons[1]).toContain('dd-log-icon-reviewed');
    expect(icons[1]).not.toContain('dd-log-icon-false_positive');
    expect(icons[2]).toContain('dd-log-icon-false_positive');
  });
});

describe('ActivityLog note form', () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.mocked(toastActions.error).mockClear();
  });

  function renderForm(detection: DetectionDetailData = base) {
    return render(ActivityLog, {
      props: { detection, speciesName: 'Song Thrush', canReview: true, onChanged: vi.fn() },
    });
  }

  const saveButton = () => screen.getByRole('button', { name: 'detections.detail.log.saveNote' });

  it('stays enabled for an empty note and says why nothing was saved', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    renderForm();

    expect(saveButton()).not.toHaveAttribute('aria-disabled');
    await fireEvent.click(saveButton());

    expect(await screen.findByRole('alert')).toHaveTextContent('detections.detail.log.noteEmpty');
    expect(screen.getByRole('textbox')).toHaveAttribute('aria-invalid', 'true');
    expect(fetchMock).not.toHaveBeenCalled();

    await fireEvent.input(screen.getByRole('textbox'), { target: { value: 'a note' } });
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('points the blocked button at the visible reason when the detection is locked', () => {
    renderForm({ ...base, locked: true });

    const reason = document.getElementById(saveButton().getAttribute('aria-describedby') ?? '');
    expect(saveButton()).toHaveAttribute('aria-disabled', 'true');
    expect(reason?.textContent).toContain('detections.detail.log.lockedHelp');
  });

  // The page keys the log by detection: stepping away destroys it while a save may be out.
  it('reports a failed save as a toast when the log was destroyed during the save', async () => {
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
    const { unmount } = renderForm();
    await fireEvent.input(screen.getByRole('textbox'), { target: { value: 'a note' } });
    await fireEvent.click(saveButton());
    await waitFor(() => expect(answer).toBeDefined());

    unmount();
    answer?.(new Response('{}', { status: 500, headers: { 'Content-Type': 'application/json' } }));

    await waitFor(() => {
      expect(vi.mocked(toastActions.error)).toHaveBeenCalledWith(
        'common.review.errors.commentFailed'
      );
    });
  });
});
