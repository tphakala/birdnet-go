/**
 * Keyboard shortcut registry and dispatcher.
 *
 * A page registers its shortcut definitions as a scope (`useShortcuts`); one `keydown`
 * listener in the root layout calls `handleKeydown`, which finds the definition for the key
 * and runs it, or consumes the key and announces why it cannot run. The cheat sheet lists
 * the registered scopes, and controls ask `hint()` for the key to show. The on/off setting
 * is read on every key press, so turning shortcuts off takes effect at once.
 *
 * The dispatcher, in this order, for each key press:
 * 1. shortcuts are off: return
 * 2. the event was handled already, is part of an input method composition, or has Ctrl,
 *    Meta or Alt held: return
 * 3. the target takes typed text: return
 * 4. no definition has a matching binding (page scopes first, then global): return
 * 5. an arrow key on a widget that uses arrow keys, or Space on an interactive element:
 *    return, the element keeps the key
 * 6. a dialog is open and the definition does not allow it: return
 * 7. status `pass`: return, native behaviour stays
 * 8. consume the key (`preventDefault`); a repeat without `allowRepeat` stops here
 * 9. status `hold`: stop quietly; `{ blocked }`: announce the reason; else run
 */
import { tick } from 'svelte';
import { openDialogCount } from '$lib/desktop/components/ui/Modal.svelte';
import {
  KEYS,
  describeBinding,
  hasCommandModifier,
  isApplePlatform,
  isArrowKeyWidget,
  isImeComposition,
  isInteractiveTarget,
  isTextEntryTarget,
  matchesBinding,
  type ShortcutBinding,
  type ShortcutHint,
} from '$lib/utils/keyboard';
import { getStoredValue, setStoredValue } from '$lib/utils/storage';
import { loggers } from '$lib/utils/logger';

const logger = loggers.ui;

/** localStorage key of the on/off setting. */
export const SHORTCUTS_ENABLED_STORAGE_KEY = 'birdnet-keyboard-shortcuts';

/** Id of the scope for shortcuts that work on every page. It is listed after the page scopes. */
export const GLOBAL_SHORTCUT_SCOPE = 'global';

/** Section a shortcut is listed under in the cheat sheet. */
export type ShortcutGroup = 'navigation' | 'review' | 'audio' | 'general';

/**
 * What a definition says about its key right now: `ready` runs it, `pass` leaves the key to
 * the browser (Space scrolls), `hold` consumes it quietly (loading, nothing to act on), and
 * `{ blocked }` consumes it and announces the reason.
 */
export type ShortcutStatus = 'ready' | 'pass' | 'hold' | { blocked: string };

/** One shortcut. */
export interface ShortcutDefinition {
  /** Unique across the registered scopes; `hint()` finds a definition by it. */
  id: string;
  group: ShortcutGroup;
  binding: ShortcutBinding;
  /** A second key for the same action. */
  secondaryBinding?: ShortcutBinding;
  /** What it does, for the cheat sheet. */
  description: () => string;
  /** Run again for each key repeat. */
  allowRepeat?: boolean;
  /** True while the shortcut also works with a dialog open. */
  allowInDialog?: () => boolean;
  /** Whether the cheat sheet and the hints list it (default true); never affects matching. */
  visible?: () => boolean;
  /** Evaluated on each key press; default `ready`. */
  status?: () => ShortcutStatus;
  run: () => void;
}

/** The shortcuts of one owner: a page, or the global set. */
export interface ShortcutScope {
  id: string;
  /** Section title in the cheat sheet. */
  label: () => string;
  definitions: readonly ShortcutDefinition[];
}

/** The shortcut registry. */
export interface ShortcutStore {
  /** Whether shortcuts are on. Read on every key press. */
  readonly enabled: boolean;
  setEnabled(value: boolean): void;
  /** Whether the cheat sheet is open. */
  readonly helpOpen: boolean;
  openHelp(): void;
  closeHelp(): void;
  toggleHelp(): void;
  /** The text of the status region: why the last blocked key did nothing. */
  readonly announcement: string;
  /** Page scopes in registration order, then the global scope. */
  readonly scopes: readonly ShortcutScope[];
  /** Adds a scope; the returned function removes exactly that scope. */
  register(scope: ShortcutScope): () => void;
  /** How to show a shortcut, or null when shortcuts are off or it is unknown or not visible. */
  hint(id: string): ShortcutHint | null;
  /** The key listener; an arrow property, so it is safe to pass as a listener. */
  handleKeydown(event: KeyboardEvent): void;
}

function isBoolean(value: unknown): value is boolean {
  return typeof value === 'boolean';
}

function sameBinding(first: ShortcutBinding, second: ShortcutBinding): boolean {
  return first.key === second.key && Boolean(first.shift) === Boolean(second.shift);
}

function bindingsOf(definition: ShortcutDefinition): ShortcutBinding[] {
  return definition.secondaryBinding
    ? [definition.binding, definition.secondaryBinding]
    : [definition.binding];
}

/** Whether the pressed key is an arrow key, which a slider, list or menu uses itself. */
function isArrowKey(key: string): boolean {
  return key === KEYS.arrowLeft || key === KEYS.arrowRight;
}

/**
 * Creates a shortcut registry. `openDialogCount` says how many dialogs are open (all
 * shortcuts stay off while one is, unless a definition allows it).
 */
export function createShortcuts(deps: {
  openDialogCount: () => number;
  storageKey?: string;
}): ShortcutStore {
  const storageKey = deps.storageKey ?? SHORTCUTS_ENABLED_STORAGE_KEY;
  const apple = isApplePlatform();

  let enabled = $state(getStoredValue(storageKey, true, isBoolean));
  let helpOpen = $state(false);
  let announcement = $state('');
  // Raw, not deep state: a scope is matched and removed by identity
  let registered = $state.raw<readonly ShortcutScope[]>([]);
  let announceGeneration = 0;

  /** Page scopes in registration order, then the global scope. */
  function orderedScopes(): readonly ShortcutScope[] {
    return [
      ...registered.filter(scope => scope.id !== GLOBAL_SHORTCUT_SCOPE),
      ...registered.filter(scope => scope.id === GLOBAL_SHORTCUT_SCOPE),
    ];
  }

  function findDefinition(id: string): ShortcutDefinition | undefined {
    for (const scope of orderedScopes()) {
      const found = scope.definitions.find(definition => definition.id === id);
      if (found) return found;
    }
    return undefined;
  }

  function findMatch(event: KeyboardEvent): ShortcutDefinition | undefined {
    for (const scope of orderedScopes()) {
      for (const definition of scope.definitions) {
        if (bindingsOf(definition).some(binding => matchesBinding(event, binding))) {
          return definition;
        }
      }
    }
    return undefined;
  }

  function warnOnDuplicateBindings(scope: ShortcutScope): void {
    for (const other of registered) {
      for (const definition of scope.definitions) {
        for (const existing of other.definitions) {
          const clash = bindingsOf(definition).some(binding =>
            bindingsOf(existing).some(taken => sameBinding(binding, taken))
          );
          if (clash) {
            logger.warn('Keyboard shortcut binding is already registered', {
              component: 'shortcuts',
              scope: scope.id,
              otherScope: other.id,
              shortcut: definition.id,
              otherShortcut: existing.id,
            });
          }
        }
      }
    }
  }

  /**
   * Announce a reason in the status region. The text is emptied first and set after a tick,
   * so an identical reason is announced again, and an overlapping call never writes stale text.
   */
  async function announce(message: string): Promise<void> {
    const generation = ++announceGeneration;
    announcement = '';
    await tick();
    if (generation === announceGeneration) announcement = message;
  }

  const handleKeydown = (event: KeyboardEvent): void => {
    if (!enabled) return;
    if (event.defaultPrevented || isImeComposition(event) || hasCommandModifier(event)) return;
    const target = event.target;
    if (isTextEntryTarget(target)) return;

    const definition = findMatch(event);
    if (!definition) return;
    if (isArrowKey(event.key) && isArrowKeyWidget(target)) return;
    if (event.key === KEYS.space && isInteractiveTarget(target)) return;
    if (deps.openDialogCount() > 0 && !definition.allowInDialog?.()) return;

    const status = definition.status?.() ?? 'ready';
    if (status === 'pass') return;
    event.preventDefault();
    if (event.repeat && !definition.allowRepeat) return;
    if (status === 'hold') return;
    if (typeof status === 'object') {
      void announce(status.blocked);
      return;
    }
    definition.run();
  };

  return {
    get enabled() {
      return enabled;
    },
    setEnabled(value: boolean) {
      enabled = value;
      setStoredValue(storageKey, value);
    },
    get helpOpen() {
      return helpOpen;
    },
    openHelp() {
      helpOpen = true;
    },
    closeHelp() {
      helpOpen = false;
    },
    toggleHelp() {
      helpOpen = !helpOpen;
    },
    get announcement() {
      return announcement;
    },
    get scopes() {
      return orderedScopes();
    },
    register(scope: ShortcutScope) {
      warnOnDuplicateBindings(scope);
      registered = [...registered, scope];
      return () => {
        registered = registered.filter(entry => entry !== scope);
      };
    },
    hint(id: string) {
      if (!enabled) return null;
      const definition = findDefinition(id);
      if (!definition || definition.visible?.() === false) return null;
      return describeBinding(definition.binding, apple, definition.secondaryBinding);
    },
    handleKeydown,
  };
}

/** The app's shortcut registry. */
export const shortcuts = createShortcuts({ openDialogCount });
