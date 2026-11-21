import { describe, expect, it, vi } from 'vitest';
import type { ShortcutDefinition } from '$lib/stores/shortcuts.svelte';
import type { PlaybackControls } from '$lib/utils/audio';
import { SEEK_STEP_SECONDS } from '$lib/utils/seek';
import {
  DETAIL_SHORTCUT_IDS,
  createDetailShortcuts,
  type DetailShortcutContext,
} from './detailShortcuts';
import type { DetectionNeighbors, ReviewControls, ReviewShortcutAction } from './detailTypes';

const NAV = 'detections.detail.nav';
const SPECIES = 'Blackbird';

const NEIGHBORS: DetectionNeighbors = {
  detectionId: 10,
  previous: 9,
  next: 11,
  previousSameSpecies: 4,
  nextSameSpecies: 12,
};

interface Fakes {
  go: ReturnType<typeof vi.fn<(path: string) => void>>;
  review: {
    blockedReason: ReturnType<typeof vi.fn<(action: ReviewShortcutAction) => string>>;
    run: ReturnType<typeof vi.fn<(action: ReviewShortcutAction) => void>>;
    focus: ReturnType<typeof vi.fn<() => void>>;
  };
  playback: {
    togglePlayback: ReturnType<typeof vi.fn<() => void>>;
    seekBy: ReturnType<typeof vi.fn<(seconds: number) => void>>;
  };
}

function makeFakes(): Fakes {
  return {
    go: vi.fn<(path: string) => void>(),
    review: {
      blockedReason: vi.fn<(action: ReviewShortcutAction) => string>(() => ''),
      run: vi.fn<(action: ReviewShortcutAction) => void>(),
      focus: vi.fn<() => void>(),
    },
    playback: {
      togglePlayback: vi.fn<() => void>(),
      seekBy: vi.fn<(seconds: number) => void>(),
    },
  };
}

interface Setup {
  fakes: Fakes;
  ctx: DetailShortcutContext;
  state: {
    settled: boolean;
    loading: boolean;
    canReview: boolean;
    stepping: boolean;
    neighbors: DetectionNeighbors | null;
    failed: boolean;
    review: ReviewControls | null;
    playback: PlaybackControls | null;
  };
  byId: (id: string) => ShortcutDefinition;
}

function setup(): Setup {
  const fakes = makeFakes();
  const state: Setup['state'] = {
    settled: true,
    loading: false,
    canReview: true,
    stepping: true,
    neighbors: NEIGHBORS,
    failed: false,
    review: fakes.review,
    playback: fakes.playback,
  };
  const ctx: DetailShortcutContext = {
    settled: () => state.settled,
    loading: () => state.loading,
    canReview: () => state.canReview,
    steppingAvailable: () => state.stepping,
    neighbors: () => state.neighbors,
    neighborsFailed: () => state.failed,
    speciesName: () => SPECIES,
    review: () => state.review,
    playback: () => state.playback,
    go: fakes.go,
  };
  const definitions = createDetailShortcuts(ctx);
  const byId = (id: string): ShortcutDefinition => {
    const found = definitions.find(definition => definition.id === id);
    if (!found) throw new Error(`no definition ${id}`);
    return found;
  };
  return { fakes, ctx, state, byId };
}

const STEP_CASES = [
  {
    id: DETAIL_SHORTCUT_IDS.older,
    target: '/ui/detections/9',
    empty: `${NAV}.noOlder`,
    blank: { ...NEIGHBORS, previous: null },
  },
  {
    id: DETAIL_SHORTCUT_IDS.newer,
    target: '/ui/detections/11',
    empty: `${NAV}.noNewer`,
    blank: { ...NEIGHBORS, next: null },
  },
  {
    id: DETAIL_SHORTCUT_IDS.olderSpecies,
    target: '/ui/detections/4',
    empty: `${NAV}.noOlderSpecies`,
    blank: { ...NEIGHBORS, previousSameSpecies: null },
  },
  {
    id: DETAIL_SHORTCUT_IDS.newerSpecies,
    target: '/ui/detections/12',
    empty: `${NAV}.noNewerSpecies`,
    blank: { ...NEIGHBORS, nextSameSpecies: null },
  },
] as const;

describe('detail shortcuts: stepping', () => {
  it.each(STEP_CASES)('$id runs towards its own neighbour', ({ id, target }) => {
    const { byId, fakes } = setup();
    const definition = byId(id);

    expect(definition.status?.()).toBe('ready');
    definition.run();

    expect(fakes.go).toHaveBeenCalledExactlyOnceWith(target);
  });

  it('binds the older and newer steps to the arrows and the species steps to Shift and the arrows', () => {
    const { byId } = setup();

    expect(byId(DETAIL_SHORTCUT_IDS.older).binding).toEqual({ key: 'ArrowLeft' });
    expect(byId(DETAIL_SHORTCUT_IDS.newer).binding).toEqual({ key: 'ArrowRight' });
    expect(byId(DETAIL_SHORTCUT_IDS.olderSpecies).binding).toEqual({
      key: 'ArrowLeft',
      shift: true,
    });
    expect(byId(DETAIL_SHORTCUT_IDS.newerSpecies).binding).toEqual({
      key: 'ArrowRight',
      shift: true,
    });
  });

  it.each(STEP_CASES)(
    '$id says why there is nowhere to go, as the link does',
    ({ id, empty, blank }) => {
      const { byId, state } = setup();
      state.neighbors = blank;

      const status = byId(id).status?.();

      expect(status).toMatchObject({ blocked: expect.stringContaining(empty) });
    }
  );

  it('gives the loading and the failed reason of the link while the neighbours are not known', () => {
    const { byId, state } = setup();
    state.neighbors = null;

    expect(byId(DETAIL_SHORTCUT_IDS.older).status?.()).toMatchObject({
      blocked: expect.stringContaining(`${NAV}.stepLoading`),
    });

    state.failed = true;
    expect(byId(DETAIL_SHORTCUT_IDS.older).status?.()).toMatchObject({
      blocked: expect.stringContaining(`${NAV}.stepFailed`),
    });
  });

  it.each(STEP_CASES)('$id holds quietly while the detection is not settled', ({ id }) => {
    const { byId, state } = setup();
    state.settled = false;

    expect(byId(id).status?.()).toBe('hold');
  });

  it.each(STEP_CASES)('$id passes and is hidden when the server cannot step', ({ id }) => {
    const { byId, state } = setup();
    state.stepping = false;

    expect(byId(id).status?.()).toBe('pass');
    expect(byId(id).visible?.()).toBe(false);
  });
});

const REVIEW_IDS = [
  { id: DETAIL_SHORTCUT_IDS.correct, key: 'c', action: 'correct' },
  { id: DETAIL_SHORTCUT_IDS.falsePositive, key: 'f', action: 'falsePositive' },
  { id: DETAIL_SHORTCUT_IDS.wrongSpecies, key: 'w', action: 'wrongSpecies' },
  { id: DETAIL_SHORTCUT_IDS.keep, key: 'k', action: 'keep' },
] as const;

describe('detail shortcuts: review', () => {
  it.each(REVIEW_IDS)('$id acts on the review panel for its own action', ({ id, key, action }) => {
    const { byId, fakes } = setup();
    const definition = byId(id);

    expect(definition.binding).toEqual({ key });
    expect(definition.status?.()).toBe('ready');
    definition.run();

    expect(fakes.review.run).toHaveBeenCalledExactlyOnceWith(action);
  });

  it.each(REVIEW_IDS)(
    '$id is hidden without permission but still announces the reason',
    ({ id, action }) => {
      const { byId, state, fakes } = setup();
      state.canReview = false;
      fakes.review.blockedReason.mockReturnValue('Log in to review this detection.');

      expect(byId(id).visible?.()).toBe(false);
      expect(byId(id).status?.()).toEqual({ blocked: 'Log in to review this detection.' });
      expect(fakes.review.blockedReason).toHaveBeenCalledWith(action);
    }
  );

  it.each(REVIEW_IDS)('$id holds while the detection is not settled', ({ id }) => {
    const { byId, state, fakes } = setup();
    state.settled = false;
    fakes.review.blockedReason.mockReturnValue('Saving...');

    expect(byId(id).status?.()).toBe('hold');
  });

  it.each(REVIEW_IDS)('$id holds without review controls', ({ id }) => {
    const { byId, state } = setup();
    state.review = null;

    expect(byId(id).status?.()).toBe('hold');
  });

  it('r focuses the review buttons, is listed only with permission, and holds while not settled', () => {
    const { byId, state, fakes } = setup();
    const definition = byId(DETAIL_SHORTCUT_IDS.focusReview);

    expect(definition.visible?.()).toBe(true);
    state.canReview = false;
    expect(definition.visible?.()).toBe(false);
    state.canReview = true;
    expect(definition.status?.()).toBe('ready');
    definition.run();
    expect(fakes.review.focus).toHaveBeenCalledOnce();

    state.settled = false;
    expect(definition.status?.()).toBe('hold');
    state.settled = true;
    state.review = null;
    expect(definition.status?.()).toBe('hold');
  });
});

describe('detail shortcuts: audio', () => {
  it('p and Space toggle playback', () => {
    const { byId, fakes } = setup();
    const definition = byId(DETAIL_SHORTCUT_IDS.playPause);

    expect(definition.binding).toEqual({ key: 'p' });
    expect(definition.secondaryBinding).toEqual({ key: ' ' });
    expect(definition.status?.()).toBe('ready');
    definition.run();

    expect(fakes.playback.togglePlayback).toHaveBeenCalledOnce();
  });

  it('comma and period seek by the seek step in their own direction and repeat', () => {
    const { byId, fakes } = setup();
    const back = byId(DETAIL_SHORTCUT_IDS.seekBack);
    const forward = byId(DETAIL_SHORTCUT_IDS.seekForward);

    expect(back.binding).toEqual({ key: ',' });
    expect(forward.binding).toEqual({ key: '.' });
    expect(back.allowRepeat).toBe(true);
    expect(forward.allowRepeat).toBe(true);

    back.run();
    forward.run();
    expect(fakes.playback.seekBy.mock.calls).toEqual([[-SEEK_STEP_SECONDS], [SEEK_STEP_SECONDS]]);
  });

  it.each([
    DETAIL_SHORTCUT_IDS.playPause,
    DETAIL_SHORTCUT_IDS.seekBack,
    DETAIL_SHORTCUT_IDS.seekForward,
  ])('%s passes without a player, so Space scrolls as usual, and holds while loading', id => {
    const { byId, state } = setup();
    state.playback = null;
    expect(byId(id).status?.()).toBe('pass');

    state.settled = false;
    expect(byId(id).status?.()).toBe('hold');
  });
});

describe('detail shortcuts: back to the list', () => {
  it('b goes to the list, also on the error state where nothing is settled', () => {
    const { byId, state, fakes } = setup();
    const definition = byId(DETAIL_SHORTCUT_IDS.backToList);
    state.settled = false;
    state.loading = false;

    expect(definition.binding).toEqual({ key: 'b' });
    expect(definition.status?.()).toBe('ready');
    definition.run();
    expect(fakes.go).toHaveBeenCalledExactlyOnceWith('/ui/detections');
  });

  it('b holds while a detection loads', () => {
    const { byId, state } = setup();
    state.loading = true;

    expect(byId(DETAIL_SHORTCUT_IDS.backToList).status?.()).toBe('hold');
  });
});

describe('detail shortcuts: definitions', () => {
  it('lists the definitions in display order with unique ids', () => {
    const { ctx } = setup();
    const ids = createDetailShortcuts(ctx).map(definition => definition.id);

    expect(ids).toEqual([
      DETAIL_SHORTCUT_IDS.older,
      DETAIL_SHORTCUT_IDS.newer,
      DETAIL_SHORTCUT_IDS.olderSpecies,
      DETAIL_SHORTCUT_IDS.newerSpecies,
      DETAIL_SHORTCUT_IDS.correct,
      DETAIL_SHORTCUT_IDS.falsePositive,
      DETAIL_SHORTCUT_IDS.wrongSpecies,
      DETAIL_SHORTCUT_IDS.keep,
      DETAIL_SHORTCUT_IDS.focusReview,
      DETAIL_SHORTCUT_IDS.playPause,
      DETAIL_SHORTCUT_IDS.seekBack,
      DETAIL_SHORTCUT_IDS.seekForward,
      DETAIL_SHORTCUT_IDS.backToList,
    ]);
    expect(new Set(ids).size).toBe(ids.length);
  });
});
