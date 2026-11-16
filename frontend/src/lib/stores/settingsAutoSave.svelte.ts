/**
 * Auto-save engine for settings pages that save as the user edits.
 *
 * A page calls commitSetting with the section, the partial it changed and a
 * field key for the control. The engine stages the edit in the settings store,
 * sends the staged edits of the section (settingsActions.saveStagedSection), and
 * keeps one status per field for the AutoSaveStatus indicator (saving, saved or
 * an error with its reason). Timing lives here, the data safety rules (queue,
 * If-Match, taking back a refused value, the server winning a conflict) live in
 * the store.
 */
import { SvelteMap } from 'svelte/reactivity';
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
  /** Key of the visible control, such as `audio:<rowId>:gain`. */
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
}

const statuses = new SvelteMap<string, AutoSaveFieldStatus>();
/** Leaf path keys (JSON.stringify of the path) each field has staged. */
const fieldPaths = new Map<string, { section: SettingsSectionName; keys: Set<string> }>();
const sections = new Map<SettingsSectionName, SectionState>();
const savedTimers = new Map<string, ReturnType<typeof setTimeout>>();
let unsubscribeDrops: (() => void) | null = null;
let pagehideListening = false;

function sectionState(section: SettingsSectionName): SectionState {
  let state = sections.get(section);
  if (!state) {
    state = { pending: new Set(), timer: null, running: null, rerun: false };
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

function countStatuses(state: AutoSaveFieldStatus['state']): number {
  let count = 0;
  for (const status of statuses.values()) {
    if (status.state === state) count++;
  }
  return count;
}

/** True while any field is waiting for a save or being saved. */
export function autoSaveBusy(): boolean {
  return countStatuses('saving') > 0;
}

/** The number of fields whose last save failed and was not dismissed. */
export function autoSaveFailures(): number {
  return countStatuses('error');
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
    if (owned.section === section && [...owned.keys].some(key => keys.has(key))) {
      owners.push(field);
    }
  }
  return owners;
}

function markChangedElsewhere(fields: Iterable<string>): void {
  for (const field of fields) {
    fieldPaths.delete(field);
    setStatus(field, { state: 'error', message: t('settings.autoSave.changedElsewhere') });
  }
}

function ensureDropSubscription(): void {
  if (unsubscribeDrops) return;
  unsubscribeDrops = settingsActions.onStagedEditsDropped(edits => {
    for (const { section, path } of edits) {
      markChangedElsewhere(fieldsOwning(section, [path]));
    }
  });
}

/** One shared function, so adding it again is a no-op. */
function flushOnPagehide(): void {
  void flushAutoSaves();
}

function ensurePagehideFlush(): void {
  if (pagehideListening || typeof window === 'undefined') return;
  pagehideListening = true;
  window.addEventListener('pagehide', flushOnPagehide);
}

/** Gives each field of a finished request its final status. */
function applyResult(
  section: SettingsSectionName,
  fields: readonly string[],
  result: StagedSaveResult
): void {
  const state = sectionState(section);
  const saved = new Set(result.saved.map(pathKey));
  const failed = new Set(result.failed.map(pathKey));
  const dropped = new Set(result.dropped.map(pathKey));
  for (const field of fields) {
    const owned = fieldPaths.get(field);
    if (!owned) continue; // already marked by the dropped-edit listener
    const keys = [...owned.keys];
    if (keys.some(key => dropped.has(key))) {
      markChangedElsewhere([field]);
      continue;
    }
    // A field committed again while this request ran waits for the next one.
    if (state.pending.has(field)) continue;
    fieldPaths.delete(field);
    const refused =
      keys.some(key => failed.has(key)) ||
      (result.error !== undefined && !keys.every(key => saved.has(key)));
    setStatus(field, refused ? { state: 'error', message: failureMessage(result.error) } : SAVED);
  }
}

/** Clears the rerun flag; a function so the flag is read afresh after the request. */
function clearRerun(state: SectionState): void {
  state.rerun = false;
}

/**
 * Sends the staged edits of a section, then again while commits keep arriving.
 * The running marker is cleared in the same step as the last check for a rerun,
 * so a commit either sets the flag before that check or starts its own flush.
 */
async function runFlush(section: SettingsSectionName): Promise<void> {
  const state = sectionState(section);
  try {
    for (;;) {
      clearRerun(state);
      const fields = [...state.pending];
      state.pending.clear();
      let result: StagedSaveResult;
      try {
        result = await settingsActions.saveStagedSection(section);
      } catch (error) {
        logger.error('Auto save request failed:', error);
        for (const field of fields) {
          // A field committed again during the request waits for the next one.
          if (state.pending.has(field)) continue;
          fieldPaths.delete(field);
          setStatus(field, { state: 'error', message: failureMessage(error) });
        }
        if (state.rerun && state.timer === null) continue;
        return;
      }
      applyResult(section, fields, result);
      const progressed = result.saved.length + result.failed.length + result.dropped.length > 0;
      const wantsMore = result.remaining && progressed;
      if (!(state.rerun || wantsMore) || state.timer !== null) return;
    }
  } finally {
    state.running = null;
  }
}

/** Flushes a section at once; a flush already running picks the commit up in its rerun. */
function flushSection(section: SettingsSectionName): Promise<void> {
  const state = sectionState(section);
  if (state.timer !== null) {
    clearTimeout(state.timer);
    state.timer = null;
  }
  if (state.running) {
    state.rerun = true;
    return state.running;
  }
  const running = runFlush(section).catch(error => {
    logger.error('Auto save flush failed:', error);
  });
  state.running = running;
  // A flush that finished before this assignment (it never awaited) must not stay marked.
  void running.then(() => {
    if (state.running === running) state.running = null;
  });
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
    fieldPaths.delete(field);
    setStatus(field, { state: 'error', message: failureMessage(error) });
    return;
  }
  ensureDropSubscription();
  ensurePagehideFlush();

  const owned = fieldPaths.get(field) ?? { section, keys: new Set<string>() };
  for (const path of leafPaths(withoutServerOwnedKeys(section, partial))) {
    owned.keys.add(pathKey(path));
  }
  fieldPaths.set(field, owned);

  const state = sectionState(section);
  state.pending.add(field);
  setStatus(field, SAVING);

  if (debounceMs === undefined) {
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
  fieldPaths.clear();
  statuses.clear();
  unsubscribeDrops?.();
  unsubscribeDrops = null;
  if (typeof window !== 'undefined') window.removeEventListener('pagehide', flushOnPagehide);
  pagehideListening = false;
}
