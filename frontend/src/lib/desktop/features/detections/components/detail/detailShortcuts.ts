/**
 * detailShortcuts.ts - the keyboard shortcuts of the detection detail page.
 *
 * One list of definitions for the whole page: stepping between detections, the review
 * actions, the player and the way back to the list. The page registers the list through
 * `useShortcuts`; the context holds closures, so every definition reads the page's live
 * state when a key is pressed, never a value captured at creation.
 *
 * A shortcut acts only on the detection that is shown and settled (loaded, not replaced by a
 * step still out): while the next detection loads, the review and audio keys are held, so a
 * verdict never lands on a detection the user has left.
 */
import { t } from '$lib/i18n';
import type { ShortcutDefinition, ShortcutStatus } from '$lib/stores/shortcuts.svelte';
import type { PlaybackControls } from '$lib/utils/audio';
import { SEEK_STEP_SECONDS } from '$lib/utils/seek';
import { stepBlockedLabel } from './detailFormat';
import type { DetectionNeighbors, ReviewControls, ReviewShortcutAction } from './detailTypes';

/** Id of the scope the detail page registers. */
export const DETAIL_SHORTCUT_SCOPE = 'detection-detail';

/** Ids of the detail page's shortcuts; the page asks the registry for hints by these. */
export const DETAIL_SHORTCUT_IDS = {
  older: 'detail.older',
  newer: 'detail.newer',
  olderSpecies: 'detail.olderSpecies',
  newerSpecies: 'detail.newerSpecies',
  backToList: 'detail.backToList',
  correct: 'detail.correct',
  falsePositive: 'detail.falsePositive',
  wrongSpecies: 'detail.wrongSpecies',
  keep: 'detail.keep',
  focusReview: 'detail.focusReview',
  playPause: 'detail.playPause',
  seekBack: 'detail.seekBack',
  seekForward: 'detail.seekForward',
} as const;

const KEY_ARROW_LEFT = 'ArrowLeft';
const KEY_ARROW_RIGHT = 'ArrowRight';
const KEY_SPACE = ' ';
const KEY_CORRECT = 'c';
const KEY_FALSE_POSITIVE = 'f';
const KEY_WRONG_SPECIES = 'w';
const KEY_KEEP = 'k';
const KEY_FOCUS_REVIEW = 'r';
const KEY_PLAY = 'p';
const KEY_SEEK_BACK = ',';
const KEY_SEEK_FORWARD = '.';
const KEY_BACK_TO_LIST = 'b';

/** The route of the detection list, and the route of one detection without its id. */
const LIST_PATH = '/ui/detections';
const DETECTION_PATH_PREFIX = `${LIST_PATH}/`;

/** The page state the definitions read. Every member reads live state when called. */
export interface DetailShortcutContext {
  /** A detection is shown and no step is loading. */
  settled(): boolean;
  /** The next detection is loading. */
  loading(): boolean;
  canReview(): boolean;
  /** False when the server cannot step between detections. */
  steppingAvailable(): boolean;
  /** Null until the neighbours are known. */
  neighbors(): DetectionNeighbors | null;
  neighborsFailed(): boolean;
  speciesName(): string;
  /** The mounted review panel's controls, null when there is none. */
  review(): ReviewControls | null;
  /** The mounted player's controls, null when there is none. */
  playback(): PlaybackControls | null;
  /** Navigates to an app route (the path without a proxy prefix). */
  go(path: string): void;
}

/** Picks one neighbour's detection id out of the neighbours. */
type NeighborPicker = (neighbors: DetectionNeighbors) => number | null;

/** One review action as the table of definitions below describes it. */
interface ReviewShortcut {
  id: string;
  key: string;
  action: ReviewShortcutAction;
  description: () => string;
}

/**
 * The definitions of the detail page, in the order the cheat sheet lists them.
 */
export function createDetailShortcuts(ctx: DetailShortcutContext): ShortcutDefinition[] {
  function stepStatus(
    pick: NeighborPicker,
    label: () => string,
    emptyLabel: () => string
  ): ShortcutStatus {
    if (!ctx.steppingAvailable()) return 'pass';
    if (!ctx.settled()) return 'hold';
    const neighbors = ctx.neighbors();
    if ((neighbors ? pick(neighbors) : null) === null) {
      return {
        blocked: stepBlockedLabel(label(), emptyLabel(), neighbors, ctx.neighborsFailed()),
      };
    }
    return 'ready';
  }

  function step(pick: NeighborPicker): void {
    const neighbors = ctx.neighbors();
    const id = neighbors ? pick(neighbors) : null;
    if (id !== null) ctx.go(`${DETECTION_PATH_PREFIX}${id}`);
  }

  function stepDefinition(
    id: string,
    binding: ShortcutDefinition['binding'],
    pick: NeighborPicker,
    description: () => string,
    label: () => string,
    emptyLabel: () => string
  ): ShortcutDefinition {
    return {
      id,
      group: 'navigation',
      binding,
      description,
      visible: ctx.steppingAvailable,
      status: () => stepStatus(pick, label, emptyLabel),
      run: () => step(pick),
    };
  }

  function reviewDefinition({ id, key, action, description }: ReviewShortcut): ShortcutDefinition {
    return {
      id,
      group: 'review',
      binding: { key },
      description,
      visible: ctx.canReview,
      status: () => {
        const review = ctx.review();
        if (!ctx.settled() || !review) return 'hold';
        const reason = review.blockedReason(action);
        return reason ? { blocked: reason } : 'ready';
      },
      run: () => ctx.review()?.run(action),
    };
  }

  function audioStatus(): ShortcutStatus {
    if (!ctx.settled()) return 'hold';
    return ctx.playback() ? 'ready' : 'pass';
  }

  const species = (): string => ctx.speciesName();

  return [
    stepDefinition(
      DETAIL_SHORTCUT_IDS.older,
      { key: KEY_ARROW_LEFT },
      neighbors => neighbors.previous,
      () => t('detections.detail.nav.older'),
      () => t('detections.detail.nav.older'),
      () => t('detections.detail.nav.noOlder')
    ),
    stepDefinition(
      DETAIL_SHORTCUT_IDS.newer,
      { key: KEY_ARROW_RIGHT },
      neighbors => neighbors.next,
      () => t('detections.detail.nav.newer'),
      () => t('detections.detail.nav.newer'),
      () => t('detections.detail.nav.noNewer')
    ),
    stepDefinition(
      DETAIL_SHORTCUT_IDS.olderSpecies,
      { key: KEY_ARROW_LEFT, shift: true },
      neighbors => neighbors.previousSameSpecies,
      () => t('shortcuts.detectionDetail.olderSpecies'),
      () => t('detections.detail.nav.olderSpecies', { species: species() }),
      () => t('detections.detail.nav.noOlderSpecies', { species: species() })
    ),
    stepDefinition(
      DETAIL_SHORTCUT_IDS.newerSpecies,
      { key: KEY_ARROW_RIGHT, shift: true },
      neighbors => neighbors.nextSameSpecies,
      () => t('shortcuts.detectionDetail.newerSpecies'),
      () => t('detections.detail.nav.newerSpecies', { species: species() }),
      () => t('detections.detail.nav.noNewerSpecies', { species: species() })
    ),
    reviewDefinition({
      id: DETAIL_SHORTCUT_IDS.correct,
      key: KEY_CORRECT,
      action: 'correct',
      description: () => t('shortcuts.detectionDetail.correct'),
    }),
    reviewDefinition({
      id: DETAIL_SHORTCUT_IDS.falsePositive,
      key: KEY_FALSE_POSITIVE,
      action: 'falsePositive',
      description: () => t('shortcuts.detectionDetail.falsePositive'),
    }),
    reviewDefinition({
      id: DETAIL_SHORTCUT_IDS.wrongSpecies,
      key: KEY_WRONG_SPECIES,
      action: 'wrongSpecies',
      description: () => t('shortcuts.detectionDetail.wrongSpecies'),
    }),
    reviewDefinition({
      id: DETAIL_SHORTCUT_IDS.keep,
      key: KEY_KEEP,
      action: 'keep',
      description: () => t('shortcuts.detectionDetail.keep'),
    }),
    {
      id: DETAIL_SHORTCUT_IDS.focusReview,
      group: 'review',
      binding: { key: KEY_FOCUS_REVIEW },
      description: () => t('shortcuts.detectionDetail.focusReview'),
      status: () => (ctx.settled() && ctx.review() ? 'ready' : 'hold'),
      run: () => ctx.review()?.focus(),
    },
    {
      id: DETAIL_SHORTCUT_IDS.playPause,
      group: 'audio',
      binding: { key: KEY_PLAY },
      secondaryBinding: { key: KEY_SPACE },
      description: () => t('shortcuts.detectionDetail.playPause'),
      status: audioStatus,
      run: () => ctx.playback()?.togglePlayback(),
    },
    {
      id: DETAIL_SHORTCUT_IDS.seekBack,
      group: 'audio',
      binding: { key: KEY_SEEK_BACK },
      allowRepeat: true,
      description: () => t('shortcuts.detectionDetail.seekBack'),
      status: audioStatus,
      run: () => ctx.playback()?.seekBy(-SEEK_STEP_SECONDS),
    },
    {
      id: DETAIL_SHORTCUT_IDS.seekForward,
      group: 'audio',
      binding: { key: KEY_SEEK_FORWARD },
      allowRepeat: true,
      description: () => t('shortcuts.detectionDetail.seekForward'),
      status: audioStatus,
      run: () => ctx.playback()?.seekBy(SEEK_STEP_SECONDS),
    },
    {
      id: DETAIL_SHORTCUT_IDS.backToList,
      group: 'navigation',
      binding: { key: KEY_BACK_TO_LIST },
      description: () => t('shortcuts.detectionDetail.backToList'),
      // Works on the error state too, where no detection is shown
      status: () => (ctx.loading() ? 'hold' : 'ready'),
      run: () => ctx.go(LIST_PATH),
    },
  ];
}
