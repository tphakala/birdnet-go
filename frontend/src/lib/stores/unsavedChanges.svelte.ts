/**
 * Unsaved-changes guard registry.
 *
 * Pages that hold drafts register an `isDirty` getter (see useUnsavedChangesGuard). The router
 * asks `request` before it changes the route, a single app-level dialog shows the prompt while
 * `prompting` is true, and one `beforeunload` listener covers tab close and reload while any
 * registration is dirty.
 *
 * This module imports only from svelte so the navigation store can use it without a cycle.
 */

/** A navigation endpoint: the canonical app path (navigation.currentPath form) and the query string with its '?', or ''. */
export interface NavigationTarget {
  path: string;
  search: string;
}

/** What a page tells the registry about its drafts. */
export interface UnsavedChangesGuard {
  /** Read at every navigation and unload decision; never cached. */
  isDirty: () => boolean;
  /** True when moving from `from` to `to` keeps the guarded component mounted with its edits. Default: samePath. */
  allows?: (to: NavigationTarget, from: NavigationTarget) => boolean;
}

/** Default predicate: the guarded component stays mounted while the path is unchanged. */
const samePath = (to: NavigationTarget, from: NavigationTarget): boolean => to.path === from.path;

/** For drafts inside a SettingsTabs tab: a query change switches the tab and unmounts them. */
export const samePathAndQuery = (to: NavigationTarget, from: NavigationTarget): boolean =>
  to.path === from.path && to.search === from.search;

/** The registry API. */
export interface UnsavedChanges {
  /** True while the leave prompt is open. */
  readonly prompting: boolean;
  /** Adds a guard; the returned function removes only that registration. */
  register(guard: UnsavedChangesGuard): () => void;
  /** Pure query (it does not commit a field being typed in): whether some dirty, unreleased guard does not allow moving from `from` to `to`. */
  blocks(to: NavigationTarget, from: NavigationTarget): boolean;
  /**
   * Asks before a route change. Null when nothing blocks (proceed synchronously). While a prompt
   * is open every move resolves false. Otherwise opens the prompt; resolves true on Leave
   * (the blocking guards then stand down until the user returns to `from.path`) and false on Stay.
   */
  request(to: NavigationTarget, from: NavigationTarget): Promise<boolean> | null;
  /**
   * For in-app actions that end in a hard navigation (login, logout): resolves true at once when
   * nothing is dirty, otherwise prompts; releases no guard.
   */
  confirmDiscard(): Promise<boolean>;
  /**
   * Makes the beforeunload handler stand down for the rest of this page's life. Call only
   * immediately before a navigation that must unload the page.
   */
  allowUnload(): void;
  /** Answers the open prompt with Leave. */
  leave(): void;
  /** Answers the open prompt with Stay. */
  stay(): void;
  /** Test helper: answers an open prompt with Stay and clears allowUnload. Leaves registrations alone. */
  resetForTesting(): void;
}

interface GuardRecord {
  guard: UnsavedChangesGuard;
  /** Path the user left from; while the current path differs, the guard is on its way out. */
  leftAt: string | null;
}

/** Creates a registry; the app uses the `unsavedChanges` singleton, tests create their own. */
export function createUnsavedChanges(): UnsavedChanges {
  const records = new Set<GuardRecord>();
  let prompting = $state(false);
  let unloadAllowed = false;
  let listening = false;
  let answer: ((leave: boolean) => void) | null = null;
  let blockers: GuardRecord[] = [];
  let leavingFrom = '';

  function isBlocking(record: GuardRecord, to: NavigationTarget, from: NavigationTarget): boolean {
    // Cheap checks first: isDirty can stringify a whole form.
    if ((record.guard.allows ?? samePath)(to, from)) return false;
    if (record.leftAt !== null && from.path !== record.leftAt) return false;
    return record.guard.isDirty();
  }

  /**
   * Fields such as TextInput commit on change, so text still being typed is not in the draft yet.
   * Leaving the focused field fires its change event synchronously and commits it; the field then
   * gets its focus back, so the user keeps typing if the move is held or never blocked.
   */
  function commitPendingEdit(): void {
    if (typeof document === 'undefined') return;
    const field = document.activeElement;
    if (
      field instanceof HTMLInputElement ||
      field instanceof HTMLTextAreaElement ||
      field instanceof HTMLSelectElement
    ) {
      field.blur();
      field.focus();
    }
  }

  function handleBeforeUnload(event: BeforeUnloadEvent): void {
    if (unloadAllowed) return;
    commitPendingEdit();
    for (const record of records) {
      if (record.guard.isDirty()) {
        event.preventDefault();
        // Legacy recipe, still needed by some browsers.
        event.returnValue = true;
        return;
      }
    }
  }

  function openPrompt(leavers: GuardRecord[], fromPath: string): Promise<boolean> {
    blockers = leavers;
    leavingFrom = fromPath;
    prompting = true;
    return new Promise<boolean>(resolve => {
      answer = resolve;
    });
  }

  function settle(leave: boolean): void {
    const resolve = answer;
    if (!resolve) return;
    answer = null;
    if (leave) {
      for (const record of blockers) record.leftAt = leavingFrom;
    }
    blockers = [];
    prompting = false;
    resolve(leave);
  }

  return {
    get prompting() {
      return prompting;
    },

    register(guard) {
      const record: GuardRecord = { guard, leftAt: null };
      records.add(record);
      if (!listening && typeof window !== 'undefined') {
        window.addEventListener('beforeunload', handleBeforeUnload);
        listening = true;
      }
      return () => {
        records.delete(record);
        if (records.size === 0 && listening) {
          window.removeEventListener('beforeunload', handleBeforeUnload);
          listening = false;
        }
      };
    },

    blocks(to, from) {
      for (const record of records) {
        if (isBlocking(record, to, from)) return true;
      }
      return false;
    },

    request(to, from) {
      if (prompting) return Promise.resolve(false);
      // Only a guard that does not admit the move can be affected by a pending edit.
      if ([...records].some(record => !(record.guard.allows ?? samePath)(to, from))) {
        commitPendingEdit();
      }
      const leavers = [...records].filter(record => isBlocking(record, to, from));
      if (leavers.length === 0) return null;
      return openPrompt(leavers, from.path);
    },

    confirmDiscard() {
      if (prompting) return Promise.resolve(false);
      if (records.size > 0) commitPendingEdit();
      for (const record of records) {
        if (record.guard.isDirty()) return openPrompt([], '');
      }
      return Promise.resolve(true);
    },

    allowUnload() {
      unloadAllowed = true;
    },

    leave() {
      settle(true);
    },

    stay() {
      settle(false);
    },

    resetForTesting() {
      settle(false);
      unloadAllowed = false;
    },
  };
}

/** The app-wide registry. */
export const unsavedChanges = createUnsavedChanges();
