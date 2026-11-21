import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { tick } from 'svelte';
import {
  GLOBAL_SHORTCUT_SCOPE,
  createShortcuts,
  type ShortcutDefinition,
  type ShortcutScope,
  type ShortcutStatus,
} from './shortcuts.svelte';
import { KEYS, type ShortcutBinding } from '$lib/utils/keyboard';

const TEST_STORAGE_KEY = 'test-keyboard-shortcuts';
const BLOCKED_REASON = 'Log in to review this detection.';

interface PressOptions {
  init?: KeyboardEventInit;
  /** HTML of the element the key is pressed on; it must contain an element with id "target" */
  targetHtml?: string;
  /** Called on the target before the event bubbles to the store's listener */
  beforeBubble?: (event: Event) => void;
}

describe('shortcut store', () => {
  let dialogCount = 0;
  let run: ReturnType<typeof vi.fn<() => void>>;
  let listeners: ((event: KeyboardEvent) => void)[] = [];

  function makeStore() {
    return createShortcuts({ openDialogCount: () => dialogCount, storageKey: TEST_STORAGE_KEY });
  }

  /** A store listening on the document, like the window listener in the layout. */
  function listeningStore() {
    const store = makeStore();
    document.addEventListener('keydown', store.handleKeydown);
    listeners.push(store.handleKeydown);
    return store;
  }

  function definition(
    id: string,
    binding: ShortcutBinding,
    overrides: Partial<ShortcutDefinition> = {}
  ): ShortcutDefinition {
    return {
      id,
      group: 'review',
      binding,
      description: () => id,
      run,
      ...overrides,
    };
  }

  function scope(id: string, definitions: ShortcutDefinition[]): ShortcutScope {
    return { id, label: () => id, definitions };
  }

  /** Presses a key on a target and reports what the store did to the event. */
  function press(key: string, options: PressOptions = {}) {
    document.body.innerHTML = options.targetHtml ?? '<div id="target"></div>';
    const target = document.getElementById('target');
    if (!target) throw new Error('fixture has no #target');
    if (options.beforeBubble) target.addEventListener('keydown', options.beforeBubble);
    const event = new KeyboardEvent('keydown', {
      key,
      bubbles: true,
      cancelable: true,
      ...options.init,
    });
    target.dispatchEvent(event);
    return { prevented: event.defaultPrevented };
  }

  beforeEach(() => {
    dialogCount = 0;
    run = vi.fn<() => void>();
    localStorage.removeItem(TEST_STORAGE_KEY);
  });

  afterEach(() => {
    for (const listener of listeners) document.removeEventListener('keydown', listener);
    listeners = [];
    document.body.replaceChildren();
    localStorage.removeItem(TEST_STORAGE_KEY);
    vi.restoreAllMocks();
  });

  describe('dispatcher', () => {
    const COMMAND_KEYS: { name: string; init: KeyboardEventInit }[] = [
      { name: 'Ctrl', init: { ctrlKey: true } },
      { name: 'Meta', init: { metaKey: true } },
      { name: 'Alt', init: { altKey: true } },
      { name: 'Ctrl and Alt (AltGr)', init: { ctrlKey: true, altKey: true } },
      { name: 'an input method composition', init: { isComposing: true } },
      { name: 'keyCode 229', init: { keyCode: 229 } },
    ];

    it('runs a ready definition and consumes the key', () => {
      const store = listeningStore();
      store.register(scope('page', [definition('correct', { key: 'c' })]));

      const result = press('c');

      expect(run).toHaveBeenCalledTimes(1);
      expect(result.prevented).toBe(true);
    });

    it.each(COMMAND_KEYS)('leaves the key alone with $name held', ({ init }) => {
      const store = listeningStore();
      store.register(scope('page', [definition('correct', { key: 'c' })]));

      const result = press('c', { init });

      expect(run).not.toHaveBeenCalled();
      expect(result.prevented).toBe(false);
    });

    it('leaves a key that an element already handled', () => {
      const store = listeningStore();
      store.register(scope('page', [definition('correct', { key: 'c' })]));

      press('c', { beforeBubble: event => event.preventDefault() });

      expect(run).not.toHaveBeenCalled();
    });

    it('leaves a key typed into a text field', () => {
      const store = listeningStore();
      store.register(scope('page', [definition('correct', { key: 'c' })]));

      const result = press('c', { targetHtml: '<input id="target" type="text" />' });

      expect(run).not.toHaveBeenCalled();
      expect(result.prevented).toBe(false);
    });

    it('still runs from a focused switch, which does not take typed text', () => {
      const store = listeningStore();
      store.register(scope('page', [definition('correct', { key: 'c' })]));

      press('c', { targetHtml: '<input id="target" type="checkbox" role="switch" />' });

      expect(run).toHaveBeenCalledTimes(1);
    });

    it('leaves an arrow key to a slider', () => {
      const store = listeningStore();
      store.register(scope('page', [definition('older', { key: KEYS.arrowLeft })]));

      const result = press(KEYS.arrowLeft, { targetHtml: '<div id="target" role="slider"></div>' });

      expect(run).not.toHaveBeenCalled();
      expect(result.prevented).toBe(false);
    });

    it('leaves Space to a focused button', () => {
      const store = listeningStore();
      store.register(scope('page', [definition('play', { key: KEYS.space })]));

      const result = press(KEYS.space, { targetHtml: '<button id="target">x</button>' });

      expect(run).not.toHaveBeenCalled();
      expect(result.prevented).toBe(false);
    });

    it('matches a secondary binding too', () => {
      const store = listeningStore();
      store.register(
        scope('page', [definition('play', { key: 'p' }, { secondaryBinding: { key: KEYS.space } })])
      );

      press(KEYS.space);

      expect(run).toHaveBeenCalledTimes(1);
    });

    it('prefers a page scope over the global scope', () => {
      const store = listeningStore();
      const globalRun = vi.fn();
      store.register(
        scope(GLOBAL_SHORTCUT_SCOPE, [definition('global.c', { key: 'c' }, { run: globalRun })])
      );
      store.register(scope('page', [definition('correct', { key: 'c' })]));

      press('c');

      expect(run).toHaveBeenCalledTimes(1);
      expect(globalRun).not.toHaveBeenCalled();
    });

    it('leaves the event alone for a pass status, so native behaviour stays', () => {
      const store = listeningStore();
      store.register(
        scope('page', [definition('play', { key: KEYS.space }, { status: () => 'pass' })])
      );

      const result = press(KEYS.space);

      expect(run).not.toHaveBeenCalled();
      expect(result.prevented).toBe(false);
    });

    it('consumes a key quietly for a hold status', async () => {
      const store = listeningStore();
      store.register(
        scope('page', [definition('play', { key: KEYS.space }, { status: () => 'hold' })])
      );

      const result = press(KEYS.space);
      await tick();

      expect(run).not.toHaveBeenCalled();
      expect(result.prevented).toBe(true);
      expect(store.announcement).toBe('');
    });

    it('consumes a blocked key, announces the reason and does not run', async () => {
      const store = listeningStore();
      store.register(
        scope('page', [
          definition('correct', { key: 'c' }, { status: () => ({ blocked: BLOCKED_REASON }) }),
        ])
      );

      const result = press('c');
      await tick();

      expect(run).not.toHaveBeenCalled();
      expect(result.prevented).toBe(true);
      expect(store.announcement).toBe(BLOCKED_REASON);
    });

    it('consumes a key repeat without running it unless the definition allows repeats', () => {
      const store = listeningStore();
      store.register(
        scope('page', [
          definition('correct', { key: 'c' }),
          definition('seek', { key: KEYS.comma }, { allowRepeat: true }),
        ])
      );

      const repeated = press('c', { init: { repeat: true } });
      expect(run).not.toHaveBeenCalled();
      expect(repeated.prevented).toBe(true);

      press(KEYS.comma, { init: { repeat: true } });
      expect(run).toHaveBeenCalledTimes(1);
    });

    it('does not announce a blocked reason again for a key repeat', async () => {
      const store = listeningStore();
      store.register(
        scope('page', [
          definition('correct', { key: 'c' }, { status: () => ({ blocked: BLOCKED_REASON }) }),
        ])
      );

      press('c', { init: { repeat: true } });
      await tick();

      expect(store.announcement).toBe('');
    });

    it('stays off while a dialog is open, except for a definition that allows it', () => {
      const store = listeningStore();
      const helpRun = vi.fn();
      store.register(
        scope('page', [
          definition('correct', { key: 'c' }),
          definition(
            'help',
            { key: KEYS.questionMark },
            { run: helpRun, allowInDialog: () => true }
          ),
        ])
      );
      dialogCount = 1;

      const blocked = press('c');
      press(KEYS.questionMark);

      expect(run).not.toHaveBeenCalled();
      expect(blocked.prevented).toBe(false);
      expect(helpRun).toHaveBeenCalledTimes(1);
    });

    it('checks the status only for a key that is not left to the page', () => {
      const store = listeningStore();
      const status = vi.fn<() => ShortcutStatus>(() => 'ready');
      store.register(scope('page', [definition('correct', { key: 'c' }, { status })]));

      press('c', { init: { ctrlKey: true } });
      press('x');

      expect(status).not.toHaveBeenCalled();
    });
  });

  describe('the on and off setting', () => {
    it('does nothing while off, not even for the global scope, and hides hints', () => {
      const store = listeningStore();
      store.register(
        scope(GLOBAL_SHORTCUT_SCOPE, [definition('global.help', { key: KEYS.questionMark })])
      );
      store.register(scope('page', [definition('correct', { key: 'c' })]));
      expect(store.hint('correct')).not.toBeNull();

      store.setEnabled(false);
      const letter = press('c');
      const help = press(KEYS.questionMark);

      expect(run).not.toHaveBeenCalled();
      expect(letter.prevented).toBe(false);
      expect(help.prevented).toBe(false);
      expect(store.hint('correct')).toBeNull();
      expect(store.hint('global.help')).toBeNull();
    });

    it('applies a change at once and resumes when turned on again', () => {
      const store = listeningStore();
      store.register(scope('page', [definition('correct', { key: 'c' })]));

      store.setEnabled(false);
      press('c');
      store.setEnabled(true);
      press('c');

      expect(run).toHaveBeenCalledTimes(1);
    });

    it('starts on without a stored value', () => {
      expect(makeStore().enabled).toBe(true);
    });

    it('keeps an off setting for a store created later from the same storage', () => {
      makeStore().setEnabled(false);

      expect(makeStore().enabled).toBe(false);
    });

    it('starts on when the stored value is not a boolean', () => {
      localStorage.setItem(TEST_STORAGE_KEY, JSON.stringify('off'));

      expect(makeStore().enabled).toBe(true);
    });

    it('turns off for the session when the storage cannot be written', () => {
      vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
        throw new Error('storage is blocked');
      });
      const store = makeStore();

      store.setEnabled(false);

      expect(store.enabled).toBe(false);
    });

    it('hides a hint for an unregistered or invisible definition', () => {
      const store = makeStore();
      store.register(
        scope('page', [
          definition('visible', { key: 'c' }),
          definition('hidden', { key: 'f' }, { visible: () => false }),
        ])
      );

      expect(store.hint('visible')?.aria).toBe('C');
      expect(store.hint('hidden')).toBeNull();
      expect(store.hint('missing')).toBeNull();
    });
  });

  describe('registration', () => {
    it('lists page scopes in registration order, then the global scope', () => {
      const store = makeStore();
      store.register(scope(GLOBAL_SHORTCUT_SCOPE, []));
      store.register(scope('first', []));
      store.register(scope('second', []));

      expect(store.scopes.map(entry => entry.id)).toEqual([
        'first',
        'second',
        GLOBAL_SHORTCUT_SCOPE,
      ]);
    });

    it('removes exactly the scope that was unregistered, even with the same id', () => {
      const store = listeningStore();
      const firstRun = vi.fn();
      const secondRun = vi.fn();
      const unregisterFirst = store.register(
        scope('page', [definition('correct', { key: 'c' }, { run: firstRun })])
      );
      const unregisterSecond = store.register(
        scope('page', [definition('correct', { key: 'c' }, { run: secondRun })])
      );

      unregisterFirst();
      expect(store.scopes).toHaveLength(1);
      press('c');
      expect(firstRun).not.toHaveBeenCalled();
      expect(secondRun).toHaveBeenCalledTimes(1);

      unregisterSecond();
      expect(store.scopes).toHaveLength(0);
      press('c');
      expect(secondRun).toHaveBeenCalledTimes(1);
    });

    it('keeps the other registration when one registration of the same scope object is removed', () => {
      const store = listeningStore();
      const page = scope('page', [definition('correct', { key: 'c' })]);
      const unregisterFirst = store.register(page);
      const unregisterSecond = store.register(page);

      unregisterFirst();

      expect(store.scopes).toHaveLength(1);
      press('c');
      expect(run).toHaveBeenCalledTimes(1);
      unregisterSecond();
      expect(store.scopes).toHaveLength(0);
    });

    it('keeps the place of a scope registered twice when its later registration is removed', () => {
      const store = listeningStore();
      const shared = scope('shared', [definition('first', { key: 'a' })]);
      const other = scope('other', [definition('second', { key: 'b' })]);
      store.register(shared);
      store.register(other);
      const unregisterLater = store.register(shared);

      unregisterLater();

      expect(store.scopes.map(entry => entry.id)).toEqual(['shared', 'other']);
    });

    it('removes nothing and keeps the announcement when an unregister function is called a second time', async () => {
      const store = listeningStore();
      const shared = scope('shared', [
        definition('first', { key: 'a' }, { status: () => ({ blocked: BLOCKED_REASON }) }),
      ]);
      const unregisterFirst = store.register(shared);
      store.register(shared);
      unregisterFirst();
      press('a');
      await tick();
      expect(store.announcement).toBe(BLOCKED_REASON);

      unregisterFirst();

      expect(store.scopes).toHaveLength(1);
      expect(store.announcement).toBe(BLOCKED_REASON);
    });

    it('holds one entry after a register, unregister and register again', () => {
      const store = makeStore();
      const page = scope('page', [definition('correct', { key: 'c' })]);

      store.register(page)();
      store.register(page);

      expect(store.scopes).toHaveLength(1);
    });
  });

  describe('announcements', () => {
    it('empties the region before repeating the same reason, so it is announced again', async () => {
      const store = listeningStore();
      store.register(
        scope('page', [
          definition('correct', { key: 'c' }, { status: () => ({ blocked: BLOCKED_REASON }) }),
        ])
      );

      press('c');
      expect(store.announcement).toBe('');
      await tick();
      expect(store.announcement).toBe(BLOCKED_REASON);

      press('c');
      expect(store.announcement).toBe('');
      await tick();
      expect(store.announcement).toBe(BLOCKED_REASON);
    });
  });

  describe('definitions hidden from the sheet', () => {
    it('still match their key, so a blocked one announces its reason and a ready one runs', async () => {
      const store = listeningStore();
      store.register(
        scope('page', [
          definition(
            'blocked',
            { key: 'f' },
            { visible: () => false, status: () => ({ blocked: BLOCKED_REASON }) }
          ),
          definition('ready', { key: 'c' }, { visible: () => false }),
        ])
      );

      press('f');
      await tick();
      expect(store.announcement).toBe(BLOCKED_REASON);
      press('c');
      expect(run).toHaveBeenCalledTimes(1);
    });
  });

  describe('the platform', () => {
    afterEach(() => vi.unstubAllGlobals());

    const shiftLeft = { key: KEYS.arrowLeft, shift: true };

    it('shows Shift combinations as glyphs on an Apple platform', () => {
      vi.stubGlobal('navigator', { platform: 'MacIntel', userAgent: '' });
      const store = makeStore();
      store.register(scope('page', [definition('older', shiftLeft)]));

      expect(store.hint('older')?.display).toBe('⇧←');
    });

    it('shows Shift combinations as words elsewhere', () => {
      vi.stubGlobal('navigator', { platform: 'Linux x86_64', userAgent: 'Mozilla X11' });
      const store = makeStore();
      store.register(scope('page', [definition('older', shiftLeft)]));

      expect(store.hint('older')?.display).toBe('Shift+←');
    });
  });

  describe('clearing the announcement', () => {
    it('empties the region and cancels an announcement that is still pending', async () => {
      const store = listeningStore();
      store.register(
        scope('page', [
          definition('correct', { key: 'c' }, { status: () => ({ blocked: BLOCKED_REASON }) }),
        ])
      );

      press('c');
      store.clearAnnouncement();
      await tick();
      expect(store.announcement).toBe('');

      press('c');
      await tick();
      expect(store.announcement).toBe(BLOCKED_REASON);
      store.clearAnnouncement();
      expect(store.announcement).toBe('');
    });

    it('empties the region when the scope that spoke is unregistered', async () => {
      const store = listeningStore();
      const unregister = store.register(
        scope('page', [
          definition('correct', { key: 'c' }, { status: () => ({ blocked: BLOCKED_REASON }) }),
        ])
      );

      press('c');
      await tick();
      expect(store.announcement).toBe(BLOCKED_REASON);

      unregister();
      expect(store.announcement).toBe('');
    });
  });

  describe('the cheat sheet state', () => {
    it('opens, closes and toggles', () => {
      const store = makeStore();

      store.openHelp();
      expect(store.helpOpen).toBe(true);
      store.closeHelp();
      expect(store.helpOpen).toBe(false);
      store.toggleHelp();
      expect(store.helpOpen).toBe(true);
      store.toggleHelp();
      expect(store.helpOpen).toBe(false);
    });
  });
});
