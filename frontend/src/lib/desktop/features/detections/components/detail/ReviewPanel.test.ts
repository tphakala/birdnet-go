import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import ReviewPanel from './ReviewPanel.svelte';
import { toastActions } from '$lib/stores/toast';
import type { DetectionDetailData, ReviewControls, ReviewShortcutAction } from './detailTypes';
import type { ShortcutHint } from '$lib/utils/keyboard';

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

describe('ReviewPanel species change during a save', () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  // Regression: a species change could start while a verdict was still being saved.
  it('blocks Wrong species while a verdict is saved', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => new Promise<Response>(() => {}))
    );
    const { onWrongSpecies } = renderPanel('');

    await fireEvent.click(
      screen.getByRole('button', { name: /detections\.detail\.review\.correct/ })
    );
    await fireEvent.click(wrongSpeciesButton());

    expect(wrongSpeciesButton()).toHaveAttribute('aria-disabled', 'true');
    expect(onWrongSpecies).not.toHaveBeenCalled();
  });

  // Regression: the other controls ignored clicks during a save without showing it.
  it('shows every review control as blocked, pointing at the status, while a save is out', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => new Promise<Response>(() => {}))
    );
    render(ReviewPanel, {
      props: {
        detection: { ...detection, verified: 'false_positive' },
        speciesName: 'Eurasian Woodcock',
        canReview: true,
        speciesBlockedReason: '',
        onWrongSpecies: vi.fn(),
        onChanged: vi.fn(),
      },
    });
    const correct = screen.getByRole('button', { name: /detections\.detail\.review\.correct/ });

    await fireEvent.click(correct);

    const status = screen.getByText('detections.detail.review.saving');
    for (const control of [
      correct,
      screen.getByRole('button', { name: /detections\.detail\.review\.falsePositive/ }),
      screen.getByRole('button', { name: 'detections.detail.review.ignoreShort' }),
      screen.getByRole('switch'),
    ]) {
      expect(control).toHaveAttribute('aria-disabled', 'true');
      expect(control.getAttribute('aria-describedby')?.split(' ')).toContain(status.id);
    }
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

  // The exclude list is kept by scientific name, which every detection has; a common name
  // can be empty or shared by two species.
  it('names the species to ignore by its scientific name', async () => {
    const bodies: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
        bodies.push(String(init?.body));
        return Promise.resolve(
          new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } })
        );
      })
    );
    render(ReviewPanel, {
      props: {
        detection: { ...detection, verified: 'false_positive', commonName: '' },
        speciesName: 'Scolopax rusticola',
        canReview: true,
        speciesBlockedReason: '',
        onWrongSpecies: vi.fn(),
        onChanged: vi.fn(),
      },
    });

    await fireEvent.click(
      screen.getByRole('button', { name: 'detections.detail.review.ignoreShort' })
    );

    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(JSON.parse(bodies[0])).toMatchObject({
      verified: 'false_positive',
      ignore_species: 'Scolopax rusticola',
    });
  });

  // Regression: Ignore was free again before the page's reload landed, and a failed reload
  // went unreported.
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
    await waitFor(() => expect(onChanged).toHaveBeenCalledOnce());
    expect(screen.getByText('detections.detail.review.saving')).toBeInTheDocument();

    settleReload?.(false);
    await waitFor(() => {
      expect(screen.getByText('detections.detail.review.reloadFailed')).toBeInTheDocument();
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

describe('ReviewPanel shortcut controls', () => {
  const LOGIN_REQUIRED = 'detections.detail.review.loginRequired';
  const SAVING = 'detections.detail.review.saving';
  const LOCKED_HELP = 'detections.detail.review.lockedHelp';
  const SPECIES_REASON = 'detections.detail.review.legacyHelp';
  const ACTIONS: ReviewShortcutAction[] = ['correct', 'falsePositive', 'wrongSpecies', 'keep'];

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  function renderWithControls(
    overrides: {
      detection?: DetectionDetailData;
      canReview?: boolean;
      speciesBlockedReason?: string;
    } = {}
  ) {
    let controls: ReviewControls | undefined;
    const onWrongSpecies = vi.fn();
    render(ReviewPanel, {
      props: {
        detection: overrides.detection ?? detection,
        speciesName: 'Eurasian Woodcock',
        canReview: overrides.canReview ?? true,
        speciesBlockedReason: overrides.speciesBlockedReason ?? SPECIES_REASON,
        onWrongSpecies,
        onChanged: vi.fn(),
        onControls: (given: ReviewControls) => {
          controls = given;
          return () => {};
        },
      },
    });
    if (!controls) throw new Error('the panel never handed out its controls');
    return { controls, onWrongSpecies };
  }

  function stubFetch(respond: () => Promise<Response>) {
    const calls: { url: string; body: unknown }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        calls.push({ url: String(input), body: JSON.parse(String(init?.body ?? 'null')) });
        return respond();
      })
    );
    return calls;
  }

  const okResponse = () =>
    Promise.resolve(
      new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } })
    );

  it.each([
    {
      name: 'a visitor without review permission',
      options: { canReview: false },
      expected: [LOGIN_REQUIRED, LOGIN_REQUIRED, LOGIN_REQUIRED, LOGIN_REQUIRED],
    },
    {
      name: 'a visitor looking at a kept detection (login first)',
      options: { canReview: false, detection: { ...detection, locked: true } },
      expected: [LOGIN_REQUIRED, LOGIN_REQUIRED, LOGIN_REQUIRED, LOGIN_REQUIRED],
    },
    {
      name: 'a kept detection',
      options: { detection: { ...detection, locked: true } },
      // Keep stays available: a kept detection can be released
      expected: [LOCKED_HELP, LOCKED_HELP, SPECIES_REASON, ''],
    },
    {
      name: 'an open detection with the species change blocked',
      options: {},
      expected: ['', '', SPECIES_REASON, ''],
    },
    {
      name: 'a verdict that is already given',
      options: { detection: { ...detection, verified: 'correct' as const } },
      expected: ['', '', SPECIES_REASON, ''],
    },
  ])('gives the reasons for $name', ({ options, expected }) => {
    const { controls } = renderWithControls(options);

    expect(ACTIONS.map(action => controls.blockedReason(action))).toEqual(expected);
  });

  it('gives the saving text for every action, before the locked and species reasons, while an unlock is out', async () => {
    stubFetch(() => new Promise<Response>(() => {}));
    const { controls } = renderWithControls({ detection: { ...detection, locked: true } });
    expect(controls.blockedReason('correct')).toBe(LOCKED_HELP);

    controls.run('keep');

    await waitFor(() => {
      expect(controls.blockedReason('keep')).toBe(SAVING);
    });
    expect(ACTIONS.map(action => controls.blockedReason(action))).toEqual([
      SAVING,
      SAVING,
      SAVING,
      SAVING,
    ]);
  });

  it('posts nothing for a verdict that is already given', async () => {
    const calls = stubFetch(okResponse);
    const { controls } = renderWithControls({
      detection: { ...detection, verified: 'correct' },
    });

    controls.run('correct');
    await Promise.resolve();

    expect(calls).toEqual([]);
  });

  it('runs false positive and keep on the detection the panel shows', async () => {
    const calls = stubFetch(okResponse);
    const { controls } = renderWithControls();

    controls.run('falsePositive');
    await waitFor(() => expect(calls).toHaveLength(1));
    expect(calls[0]).toEqual({
      url: '/api/v2/detections/7/review',
      body: { verified: 'false_positive' },
    });

    // The save settles before Keep can run: a save out blocks it
    await waitFor(() => expect(controls.blockedReason('keep')).toBe(''));
    controls.run('keep');
    await waitFor(() => expect(calls).toHaveLength(2));
    expect(calls[1]).toEqual({ url: '/api/v2/detections/7/lock', body: { locked: true } });
  });

  it('opens the species dialog for wrong species unless the page blocks it', () => {
    const open = renderWithControls({ speciesBlockedReason: '' });
    open.controls.run('wrongSpecies');
    expect(open.onWrongSpecies).toHaveBeenCalledOnce();

    cleanup();
    const blocked = renderWithControls();
    blocked.controls.run('wrongSpecies');
    expect(blocked.onWrongSpecies).not.toHaveBeenCalled();
  });

  it('moves focus to the verdict group, not to a button', () => {
    const { controls } = renderWithControls();

    controls.focus();

    const group = screen.getByRole('group', { name: 'detections.detail.review.title' });
    expect(group).toHaveFocus();
    expect(document.activeElement?.tagName).not.toBe('BUTTON');
  });

  it('detaches the controls when the panel unmounts', () => {
    const detach = vi.fn();
    const { unmount } = render(ReviewPanel, {
      props: {
        detection,
        speciesName: 'Eurasian Woodcock',
        canReview: true,
        speciesBlockedReason: '',
        onWrongSpecies: vi.fn(),
        onChanged: vi.fn(),
        onControls: () => detach,
      },
    });
    expect(detach).not.toHaveBeenCalled();

    unmount();

    expect(detach).toHaveBeenCalledTimes(1);
  });
});

describe('ReviewPanel shortcut badges', () => {
  afterEach(() => cleanup());

  const hint = (key: string): ShortcutHint => ({
    aria: key,
    keys: [key],
    display: key,
    label: key,
  });

  it('shows the keys on the verdict buttons without changing their names', () => {
    render(ReviewPanel, {
      props: {
        detection,
        speciesName: 'Eurasian Woodcock',
        canReview: true,
        speciesBlockedReason: '',
        onWrongSpecies: vi.fn(),
        onChanged: vi.fn(),
        shortcutHints: { correct: hint('C'), falsePositive: hint('F'), wrongSpecies: hint('W') },
      },
    });

    const cases = [
      ['detections.detail.review.correct', 'C'],
      ['detections.detail.review.falsePositive', 'F'],
      ['detections.detail.review.wrongSpecies', 'W'],
    ] as const;
    for (const [name, key] of cases) {
      const button = screen.getByRole('button', { name });
      expect(button).toHaveAttribute('aria-keyshortcuts', key);
      const badge = button.querySelector('kbd');
      expect(badge).toHaveTextContent(key);
      expect(badge).toHaveAttribute('aria-hidden', 'true');
    }
  });
});
