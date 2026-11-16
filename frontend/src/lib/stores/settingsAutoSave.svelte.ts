/**
 * Auto-save engine for settings pages that save as the user edits.
 *
 * A page calls commitSetting with the section, the partial it changed and a
 * field key for the control. The engine stages the edit in the settings store,
 * sends the staged edits of the section (settingsActions.saveStagedSection), and
 * keeps one status per field for the AutoSaveStatus indicator (saving, saved or
 * an error with its reason). Timing lives here, the data safety rules (queue,
 * If-Match, taking back a refused value, the server winning a conflict) live in
 * the store. A field owns the paths it committed until a request that carried
 * those commits reports on them; overlapping requests each judge only their own
 * commits. Pending edits are also sent when the page is hidden or unloaded.
 */
import { SvelteMap, SvelteSet } from 'svelte/reactivity';
import { t } from '$lib/i18n';
import {
  SettingsChangedElsewhereError,
  settingsActions,
  type StagedSaveResult,
} from '$lib/stores/settings';
import { ApiError } from '$lib/utils/api';
import { loggers } from '$lib/utils/logger';
import type { SettingsSectionName, SettingsSectionPayloads } from '$lib/utils/settingsApi.js';
import { leafPaths } from '$lib/utils/settingsMerge';
import { withoutServerOwnedKeys } from '$lib/utils/settingsSections';

const logger = loggers.settings;

/** Quiet time after the last input of a continuous control before it is saved. */
export const AUTOSAVE_DEBOUNCE_MS = 400;

/** How long the "Saved" confirmation of a field stays visible. */
export const AUTOSAVE_SAVED_VISIBLE_MS = 2000;

/** The visible state of one field. */
export type AutoSaveFieldStatus =
  | { state: 'idle' }
  | { state: 'saving' }
  | { state: 'saved' }
  | { state: 'error'; message: string };

const IDLE: AutoSaveFieldStatus = { state: 'idle' };
/** Shared, so setting the same state again is not a change for the readers. */
const SAVING: AutoSaveFieldStatus = { state: 'saving' };
const SAVED: AutoSaveFieldStatus = { state: 'saved' };

/** Options of commitSetting. */
export interface CommitOptions {
  /** Key of the visible control, such as `audio:sources:<rowId>:gain`. */
  field: string;
  /** Save after this much quiet instead of at once (sliders, steppers, time inputs). */
  debounceMs?: number;
}

/** Bookkeeping of one backend section. */
interface SectionState {
  /** Fields with an edit staged that no request has carried yet. */
  pending: Set<string>;
  timer: ReturnType<typeof setTimeout> | null;
  /** Resolves when the running flush, including its reruns, has finished. */
  running: Promise<void> | null;
  /** A commit arrived while a request was running. */
  rerun: boolean;
  /** A commit without a delay arrived since the last request started. */
  urgent: boolean;
}

const statuses = new SvelteMap<string, AutoSaveFieldStatus>();
/**
 * Leaf path keys (JSON.stringify of the path) each field owns, each with the
 * sequence number of the field's latest commit of it. A path is owned from its
 * commit until a result that carried that commit accounts for it; a result is
 * judged only on the paths whose sequence number it saw (see judgePaths).
 */
const fieldPaths = new Map<
  string,
  {
    section: SettingsSectionName;
    keys: Map<string, number>;
    /** Paths whose edit was dropped while the field still owned others; a commit of the path clears it. */
    lost: Set<string>;
    /** Paths whose save was refused while the field still owned others, with the reason shown; a commit of the path clears it. */
    refused: Map<string, string>;
  }
>();
let commitSeq = 0;
const sections = new Map<SettingsSectionName, SectionState>();
const savedTimers = new Map<string, ReturnType<typeof setTimeout>>();
/** Mounted indicators per field (registerAutoSaveField). */
const mountedFields = new Map<string, number>();
/**
 * Fields whose indicators all unmounted. Their status stays for a remount but is
 * not counted, since nothing on screen could show or dismiss it.
 */
const detachedFields = new SvelteSet<string>();
let unsubscribeDrops: (() => void) | null = null;

function sectionState(section: SettingsSectionName): SectionState {
  let state = sections.get(section);
  if (!state) {
    state = { pending: new Set(), timer: null, running: null, rerun: false, urgent: false };
    sections.set(section, state);
  }
  return state;
}

function setStatus(field: string, status: AutoSaveFieldStatus): void {
  const timer = savedTimers.get(field);
  if (timer !== undefined) {
    clearTimeout(timer);
    savedTimers.delete(field);
  }
  if (status.state === 'idle') {
    statuses.delete(field);
  } else {
    statuses.set(field, status);
  }
  if (status.state === 'saved') {
    savedTimers.set(
      field,
      setTimeout(() => {
        savedTimers.delete(field);
        if (statuses.get(field)?.state === 'saved') statuses.delete(field);
      }, AUTOSAVE_SAVED_VISIBLE_MS)
    );
  }
}

/** The status of a field; idle when nothing was committed for it. */
export function autoSaveStatus(field: string): AutoSaveFieldStatus {
  return statuses.get(field) ?? IDLE;
}

function countStatuses(
  state: AutoSaveFieldStatus['state'],
  scope: string,
  includeDetached: boolean
): number {
  let count = 0;
  for (const [field, status] of statuses) {
    if (status.state !== state || !field.startsWith(scope)) continue;
    if (!includeDetached && detachedFields.has(field)) continue;
    count++;
  }
  return count;
}

/**
 * True while a field is waiting for a save or being saved; with a scope, only
 * fields whose key starts with it count. A request still running counts after
 * its control unmounted.
 */
export function autoSaveBusy(scope = ''): boolean {
  return countStatuses('saving', scope, true) > 0;
}

/**
 * The number of fields whose last save failed and was not dismissed; with a
 * scope, only fields whose key starts with it. A field whose indicator
 * unmounted is not counted, since nothing on screen could dismiss it.
 */
export function autoSaveFailures(scope = ''): number {
  return countStatuses('error', scope, false);
}

/**
 * Registers a mounted indicator of a field; AutoSaveStatus calls it. The
 * returned function unregisters it. When the last indicator of a field
 * unmounts, its error is cleared: the control it described is gone (a row
 * deleted, a tab switched, a page left), and "Saved" expires on its own. An
 * error that arrives later is kept for a remount but not counted.
 */
export function registerAutoSaveField(field: string): () => void {
  mountedFields.set(field, (mountedFields.get(field) ?? 0) + 1);
  detachedFields.delete(field);
  let released = false;
  return () => {
    if (released) return;
    released = true;
    const remaining = (mountedFields.get(field) ?? 1) - 1;
    if (remaining > 0) {
      mountedFields.set(field, remaining);
      return;
    }
    mountedFields.delete(field);
    detachedFields.add(field);
    if (statuses.get(field)?.state === 'error') setStatus(field, IDLE);
  };
}

/** Clears the error of a field, for the dismiss button of its indicator. */
export function dismissAutoSaveError(field: string): void {
  if (statuses.get(field)?.state === 'error') setStatus(field, IDLE);
}

/**
 * The reason to show for a failed save: the server's own message when the error
 * carried an error key, the conflict text for a settings conflict, else the
 * generic one.
 */
function failureMessage(error: unknown): string {
  if (error instanceof SettingsChangedElsewhereError) return error.message;
  if (error instanceof ApiError && error.errorKey) return error.userMessage;
  return t('settings.autoSave.failed');
}

function pathKey(path: readonly string[]): string {
  return JSON.stringify(path);
}

/** Fields of a section that own at least one of the given paths. */
function fieldsOwning(section: SettingsSectionName, paths: readonly (readonly string[])[]) {
  const keys = new Set(paths.map(pathKey));
  const owners: string[] = [];
  for (const [field, owned] of fieldPaths) {
    if (owned.section === section && [...owned.keys.keys()].some(key => keys.has(key))) {
      owners.push(field);
    }
  }
  return owners;
}

/**
 * Forgets some owned paths of a field; lostKeys are the ones whose edit was
 * dropped. Returns null while the field still owns paths (a newer commit or a
 * stalled path). Once it owns none its entry is removed and the result says
 * whether any of its edits was lost or refused, which then shows that error, not Saved.
 */
function releasePaths(
  field: string,
  keys: Iterable<string>,
  lostKeys: Iterable<string> = [],
  refusedKeys: Iterable<string> = [],
  refusedMessage = ''
): { lost: boolean; refused: string | undefined } | null {
  const owned = fieldPaths.get(field);
  if (!owned) return { lost: true, refused: undefined };
  for (const key of lostKeys) owned.lost.add(key);
  for (const key of refusedKeys) owned.refused.set(key, refusedMessage);
  for (const key of keys) owned.keys.delete(key);
  if (owned.keys.size > 0) return null;
  fieldPaths.delete(field);
  return { lost: owned.lost.size > 0, refused: owned.refused.values().next().value };
}

/** Gives a field an error and forgets its staged paths, so a later result cannot overwrite it. */
function failField(field: string, message: string): void {
  fieldPaths.delete(field);
  setStatus(field, { state: 'error', message });
}

function ensureDropSubscription(): void {
  if (unsubscribeDrops) return;
  unsubscribeDrops = settingsActions.onStagedEditsDropped(edits => {
    for (const { section, path } of edits) {
      // Only the dropped path leaves the field. A field that owns other paths keeps
      // them and a lost mark, so it shows the conflict once the last one settles.
      const key = pathKey(path);
      for (const field of fieldsOwning(section, [path])) {
        if (releasePaths(field, [key], [key])) {
          setStatus(field, { state: 'error', message: t('settings.autoSave.changedElsewhere') });
        }
      }
    }
  });
}

/** One shared function, so adding it again is a no-op. */
function flushOnPagehide(): void {
  void flushAutoSaves();
}

/** Backgrounding a tab fires visibilitychange, not pagehide. */
function flushOnHidden(): void {
  if (document.hidden) void flushAutoSaves();
}

function ensureHideFlush(): void {
  if (typeof window !== 'undefined') window.addEventListener('pagehide', flushOnPagehide);
  if (typeof document !== 'undefined') document.addEventListener('visibilitychange', flushOnHidden);
}

/** What a finished request reported, as path keys. */
interface RequestOutcome {
  saved: Set<string>;
  failed: Set<string>;
  dropped: Set<string>;
  /** The save ran into an error: paths it did not save are refused. */
  error?: unknown;
  hasError: boolean;
  /** A staged path is still waiting. */
  remaining: boolean;
}

function outcomeOf(result: StagedSaveResult): RequestOutcome {
  return {
    saved: new Set(result.saved.map(pathKey)),
    failed: new Set(result.failed.map(pathKey)),
    dropped: new Set(result.dropped.map(pathKey)),
    error: result.error,
    hasError: result.error !== undefined,
    remaining: result.remaining,
  };
}

/**
 * Gives each field of a finished request its final status, judging only the
 * paths whose commit the request carried: a path committed again since the
 * request started (a newer sequence number) is left to the request that carries
 * the newer value, with its ownership and the field's status untouched. Judged
 * paths leave the field's ownership, so a later result never judges them again.
 * A field whose judged paths a not-done result left staged is returned as stalled
 * and keeps those paths.
 */
function judgePaths(
  sent: ReadonlyMap<string, ReadonlyMap<string, number>>,
  outcome: RequestOutcome
): string[] {
  const stalled: string[] = [];
  for (const [field, sentKeys] of sent) {
    const owned = fieldPaths.get(field);
    if (!owned) continue; // already marked by the dropped-edit listener
    const judged = [...sentKeys]
      .filter(([key, seq]) => owned.keys.get(key) === seq)
      .map(([key]) => key);
    if (judged.length === 0) continue;
    const dropped = judged.some(key => outcome.dropped.has(key));
    const refused =
      judged.some(key => outcome.failed.has(key)) ||
      (outcome.hasError && !judged.every(key => outcome.saved.has(key)));
    const unsent = judged.filter(
      key => !outcome.saved.has(key) && !outcome.failed.has(key) && !outcome.dropped.has(key)
    );
    const droppedKeys = judged.filter(key => outcome.dropped.has(key));
    if (!dropped && !refused && outcome.remaining && unsent.length > 0) {
      releasePaths(
        field,
        judged.filter(key => !unsent.includes(key))
      );
      stalled.push(field);
      continue;
    }
    const refusedKeys = judged.filter(
      key => outcome.failed.has(key) || (outcome.hasError && !outcome.saved.has(key))
    );
    const message = failureMessage(outcome.error);
    const settled = releasePaths(field, judged, droppedKeys, refusedKeys, message);
    if (!settled) continue; // a newer commit decides the status
    if (dropped || settled.lost) {
      setStatus(field, { state: 'error', message: t('settings.autoSave.changedElsewhere') });
    } else {
      setStatus(
        field,
        settled.refused === undefined ? SAVED : { state: 'error', message: settled.refused }
      );
    }
  }
  return stalled;
}

/** The paths each field of a request owns, with their commit sequence numbers. */
function snapshotOwned(fields: readonly string[]): Map<string, Map<string, number>> {
  const sent = new Map<string, Map<string, number>>();
  for (const field of fields) {
    const owned = fieldPaths.get(field);
    if (owned) sent.set(field, new Map(owned.keys));
  }
  return sent;
}

/** One more request for a stalled path before it counts as not saved. */
const MAX_STALLED_RETRIES = 1;

/** Clears the rerun flags; a function so they are read afresh after the request. */
function clearRerun(state: SectionState): void {
  state.rerun = false;
  state.urgent = false;
}

/** Stops a section's debounce timer; its pending edits go out with the flush that does. */
function cancelDebounce(state: SectionState): void {
  if (state.timer !== null) {
    clearTimeout(state.timer);
    state.timer = null;
  }
}

/**
 * Sends the staged edits of a section, then again while commits keep arriving.
 * The running marker is cleared in the same step as the last check for a rerun,
 * so a commit either sets the flag before that check or starts its own flush.
 */
async function runFlush(section: SettingsSectionName): Promise<void> {
  const state = sectionState(section);
  let stalledRetries = 0;
  try {
    for (;;) {
      clearRerun(state);
      const sent = snapshotOwned([...state.pending]);
      state.pending.clear();
      let result: StagedSaveResult;
      try {
        result = await settingsActions.saveStagedSection(section);
      } catch (error) {
        logger.error('Auto save request failed:', error);
        judgePaths(sent, {
          saved: new Set(),
          failed: new Set([...sent.values()].flatMap(keys => [...keys.keys()])),
          dropped: new Set(),
          error,
          hasError: true,
          remaining: false,
        });
        if (state.urgent) {
          cancelDebounce(state);
          continue;
        }
        if (state.rerun && state.timer === null) continue;
        return;
      }
      const stalled = judgePaths(sent, outcomeOf(result));
      if (stalled.length === 0) {
        stalledRetries = 0;
      } else if (stalledRetries < MAX_STALLED_RETRIES) {
        stalledRetries++;
        for (const field of stalled) state.pending.add(field);
      } else {
        for (const field of stalled) {
          // A field committed again meanwhile is judged by the next request.
          if (!state.pending.has(field)) failField(field, failureMessage(undefined));
        }
        stalledRetries = 0;
      }
      const progressed = result.saved.length + result.failed.length + result.dropped.length > 0;
      const wantsMore = state.pending.size > 0 || (result.remaining && progressed);
      // A commit without a delay goes out now, carrying a debounced value that
      // arrived after it; a debounced commit alone keeps waiting for its quiet time.
      if (state.urgent) {
        cancelDebounce(state);
      } else if (!(state.rerun || wantsMore) || state.timer !== null) {
        return;
      }
    }
  } finally {
    state.running = null;
  }
}

/** Flushes a section at once; a flush already running picks the commit up in its rerun. */
function flushSection(section: SettingsSectionName): Promise<void> {
  const state = sectionState(section);
  cancelDebounce(state);
  if (state.running) {
    state.rerun = true;
    return state.running;
  }
  const running = runFlush(section).catch(error => {
    logger.error('Auto save flush failed:', error);
  });
  // runFlush awaits the save before it can return, so this runs before its finally clears the marker.
  state.running = running;
  return running;
}

/**
 * Stages an edit and saves it: at once, or after debounceMs of quiet. A commit
 * made without a delay flushes the section at once and carries any debounced
 * value with it; a commit made while a request runs costs one more request, not
 * one per commit. Send whole arrays and complete nested objects.
 */
export function commitSetting<S extends SettingsSectionName>(
  section: S,
  partial: SettingsSectionPayloads[S],
  options: CommitOptions
): void {
  const { field, debounceMs } = options;
  try {
    settingsActions.stageSectionEdit(section, partial);
  } catch (error) {
    logger.warn('Refused to stage a settings edit:', error);
    // The field has no sendable edit now; an earlier pending commit must not turn it into Saved.
    sectionState(section).pending.delete(field);
    failField(field, failureMessage(error));
    return;
  }
  // The store stages the same filtered paths; with none there is nothing to send or judge.
  const paths = leafPaths(withoutServerOwnedKeys(section, partial));
  if (paths.length === 0) return;
  ensureDropSubscription();
  ensureHideFlush();

  const owned = fieldPaths.get(field) ?? {
    section,
    keys: new Map<string, number>(),
    lost: new Set<string>(),
    refused: new Map<string, string>(),
  };
  for (const path of paths) {
    owned.keys.set(pathKey(path), ++commitSeq);
    owned.lost.delete(pathKey(path));
    owned.refused.delete(pathKey(path));
  }
  fieldPaths.set(field, owned);

  const state = sectionState(section);
  state.pending.add(field);
  setStatus(field, SAVING);

  if (debounceMs === undefined) {
    state.urgent = true;
    void flushSection(section);
    return;
  }
  if (state.timer !== null) clearTimeout(state.timer);
  state.timer = setTimeout(() => {
    state.timer = null;
    void flushSection(section);
  }, debounceMs);
}

/**
 * Sends every pending edit now and resolves when no request is left, for the
 * page hide event and for tests.
 */
export async function flushAutoSaves(): Promise<void> {
  for (;;) {
    const active = [...sections].filter(
      ([, state]) => state.timer !== null || state.running !== null || state.pending.size > 0
    );
    if (active.length === 0) return;
    await Promise.all(active.map(([section]) => flushSection(section)));
  }
}

/** Test-only: forget every status, timer and subscription. */
export function resetAutoSaveForTest(): void {
  for (const state of sections.values()) {
    if (state.timer !== null) clearTimeout(state.timer);
  }
  for (const timer of savedTimers.values()) clearTimeout(timer);
  sections.clear();
  savedTimers.clear();
  mountedFields.clear();
  detachedFields.clear();
  fieldPaths.clear();
  statuses.clear();
  unsubscribeDrops?.();
  unsubscribeDrops = null;
  if (typeof window !== 'undefined') window.removeEventListener('pagehide', flushOnPagehide);
  if (typeof document !== 'undefined') {
    document.removeEventListener('visibilitychange', flushOnHidden);
  }
}
