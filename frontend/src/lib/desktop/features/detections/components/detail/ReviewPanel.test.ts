import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import ReviewPanel from './ReviewPanel.svelte';
import { toastActions } from '$lib/stores/toast';
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

describe('ReviewPanel ignore species', () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  // Regression: the panel reset itself whenever the page reloaded the same detection (after
  // a lock or a note), so a species ignored a moment ago was offered for ignoring again.
  it('keeps the species ignored when the same detection is reloaded', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } })
        )
      )
    );
    const rejected: DetectionDetailData = { ...detection, verified: 'false_positive' };
    const { rerender } = render(ReviewPanel, {
      props: {
        detection: rejected,
        speciesName: 'Eurasian Woodcock',
        canReview: true,
        speciesBlockedReason: '',
        onWrongSpecies: vi.fn(),
        onChanged: vi.fn(),
      },
    });

    await fireEvent.click(
      screen.getByRole('button', { name: 'detections.detail.review.ignoreShort' })
    );
    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: 'detections.detail.review.ignoredShort' })
      ).toBeInTheDocument();
    });

    await rerender({ detection: { ...rejected } });
    expect(
      screen.getByRole('button', { name: 'detections.detail.review.ignoredShort' })
    ).toBeInTheDocument();
  });

  it('offers to ignore the new species after a species change on the same detection', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } })
        )
      )
    );
    const rejected: DetectionDetailData = { ...detection, verified: 'false_positive' };
    const { rerender } = render(ReviewPanel, {
      props: {
        detection: rejected,
        speciesName: 'Eurasian Woodcock',
        canReview: true,
        speciesBlockedReason: '',
        onWrongSpecies: vi.fn(),
        onChanged: vi.fn(),
      },
    });
    await fireEvent.click(
      screen.getByRole('button', { name: 'detections.detail.review.ignoreShort' })
    );
    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: 'detections.detail.review.ignoredShort' })
      ).toBeInTheDocument();
    });

    await rerender({
      detection: { ...rejected, scientificName: 'Gallinago gallinago', commonName: 'Snipe' },
    });
    expect(
      screen.getByRole('button', { name: 'detections.detail.review.ignoreShort' })
    ).toBeInTheDocument();
  });
});

describe('ReviewPanel ignore species reload', () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it('tells the page which detection was saved, so it reloads the last-saved time', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } })
        )
      )
    );
    const onChanged = vi.fn();
    render(ReviewPanel, {
      props: {
        detection: { ...detection, verified: 'false_positive' },
        speciesName: 'Eurasian Woodcock',
        canReview: true,
        speciesBlockedReason: '',
        onWrongSpecies: vi.fn(),
        onChanged,
      },
    });

    await fireEvent.click(
      screen.getByRole('button', { name: 'detections.detail.review.ignoreShort' })
    );

    await waitFor(() => {
      expect(onChanged).toHaveBeenCalledWith({
        detectionId: 7,
        scientificName: 'Scolopax rusticola',
        speciesStats: false,
      });
    });
  });
});

describe('ReviewPanel Keep switch', () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.mocked(toastActions.error).mockClear();
  });

  it('shows the saved state again, and the error once, when the lock request fails', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          new Response('{}', { status: 500, headers: { 'Content-Type': 'application/json' } })
        )
      )
    );
    render(ReviewPanel, {
      props: {
        detection,
        speciesName: 'Eurasian Woodcock',
        canReview: true,
        speciesBlockedReason: '',
        onWrongSpecies: vi.fn(),
        onChanged: vi.fn(),
      },
    });
    const keep = screen.getByRole<HTMLInputElement>('switch');

    await fireEvent.click(keep);

    await waitFor(() => {
      expect(vi.mocked(toastActions.error)).toHaveBeenCalledOnce();
    });
    await waitFor(() => {
      expect(keep.checked).toBe(false);
    });
    expect(screen.getByText('common.review.errors.lockStatusFailed')).toBeInTheDocument();
  });

  // Regression: the switch kept the asked value for good when the page could not reload the
  // detection, while the rest of the page still read the old Keep state.
  it('ends on the value the page holds, and says so, when the reload after a save fails', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } })
        )
      )
    );
    const onChanged = vi.fn(() => Promise.resolve(false));
    render(ReviewPanel, {
      props: {
        detection,
        speciesName: 'Eurasian Woodcock',
        canReview: true,
        speciesBlockedReason: '',
        onWrongSpecies: vi.fn(),
        onChanged,
      },
    });
    const keep = screen.getByRole<HTMLInputElement>('switch');

    await fireEvent.click(keep);

    await waitFor(() => {
      expect(onChanged).toHaveBeenCalledOnce();
    });
    await waitFor(() => {
      expect(screen.getByText('detections.detail.review.reloadFailed')).toBeInTheDocument();
    });
    expect(keep.checked).toBe(false);
  });
});

describe('ReviewPanel verdict reload', () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.mocked(toastActions.error).mockClear();
  });

  // Regression: the verdict buttons were free again before the page's reload landed.
  it('stays busy until the page reload settles, and says so when it failed', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } })
        )
      )
    );
    let settleReload: ((_reloaded: boolean) => void) | undefined;
    const onChanged = vi.fn(
      () =>
        new Promise<boolean>(resolve => {
          settleReload = resolve;
        })
    );
    render(ReviewPanel, {
      props: {
        detection,
        speciesName: 'Eurasian Woodcock',
        canReview: true,
        speciesBlockedReason: '',
        onWrongSpecies: vi.fn(),
        onChanged,
      },
    });

    await fireEvent.click(
      screen.getByRole('button', { name: /detections\.detail\.review\.correct/ })
    );
    await waitFor(() => expect(onChanged).toHaveBeenCalledOnce());
    expect(screen.getByText('detections.detail.review.saving')).toBeInTheDocument();

    settleReload?.(false);
    await waitFor(() => {
      expect(screen.getByText('detections.detail.review.reloadFailed')).toBeInTheDocument();
    });
    expect(screen.queryByText('detections.detail.review.saving')).toBeNull();
  });
});

describe('ReviewPanel verdict failure', () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.mocked(toastActions.error).mockClear();
  });

  // After a failed verdict, a later save on the same detection reloads it; the old failure
  // must not stay in the status line next to the fresh data.
  it('clears the failure when the same detection is reloaded after another save', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          new Response('{}', { status: 500, headers: { 'Content-Type': 'application/json' } })
        )
      )
    );
    const { rerender } = render(ReviewPanel, {
      props: {
        detection,
        speciesName: 'Eurasian Woodcock',
        canReview: true,
        speciesBlockedReason: '',
        onWrongSpecies: vi.fn(),
        onChanged: vi.fn(),
      },
    });

    await fireEvent.click(
      screen.getByRole('button', { name: /detections\.detail\.review\.correct/ })
    );
    expect(await screen.findByText('common.review.errors.verificationFailed')).toBeInTheDocument();

    await rerender({ detection: { ...detection } });
    expect(screen.queryByText('common.review.errors.verificationFailed')).toBeNull();
  });
});
