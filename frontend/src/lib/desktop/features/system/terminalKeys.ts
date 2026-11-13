/**
 * terminalKeys.ts - keyboard exit from the xterm terminal.
 *
 * xterm consumes Tab and Shift+Tab (the shell uses them for completion), so a
 * keyboard user who tabs into the terminal cannot leave it. The handler made
 * here implements the "Escape, then Tab" convention: Escape still reaches the
 * shell, and a Tab or Shift+Tab pressed within TAB_RELEASE_WINDOW_MS of it is
 * not handled by xterm, so the browser moves focus as it normally would.
 */

import type { Terminal } from '@xterm/xterm';

/** How long after Escape a Tab or Shift+Tab still leaves the terminal. */
export const TAB_RELEASE_WINDOW_MS = 2000;

const KEY_ESCAPE = 'Escape';
const KEY_TAB = 'Tab';
const MODIFIER_KEYS: ReadonlySet<string> = new Set(['Shift', 'Control', 'Alt', 'Meta']);

/** Key handling for one terminal. */
export interface TerminalExitKeys {
  /**
   * Handler for `Terminal.attachCustomKeyEventHandler`: `true` when xterm should
   * process the key, `false` when the key must be left to the browser (focus moves).
   */
  handleKey: (ev: KeyboardEvent) => boolean;
  /** Forget a pending Escape, for example when the terminal input loses focus. */
  disarm: () => void;
}

/**
 * Create the key handling for one terminal. Each terminal needs its own.
 *
 * @param now Clock in milliseconds, injectable for tests.
 */
export function createTerminalExitKeys(now: () => number = Date.now): TerminalExitKeys {
  let escapeAt: number | null = null;

  const handleKey = (ev: KeyboardEvent): boolean => {
    if (ev.type !== 'keydown') return true;

    // Pressing Shift for Shift+Tab is part of the sequence, not a new key.
    if (MODIFIER_KEYS.has(ev.key)) return true;

    const hasCommandModifier = ev.ctrlKey || ev.altKey || ev.metaKey;
    const armedAt = escapeAt;
    escapeAt = null;

    if (ev.key === KEY_ESCAPE && !hasCommandModifier) {
      escapeAt = now();
      return true;
    }

    if (
      ev.key === KEY_TAB &&
      !hasCommandModifier &&
      armedAt !== null &&
      now() - armedAt <= TAB_RELEASE_WINDOW_MS
    ) {
      return false;
    }

    return true;
  };

  return {
    handleKey,
    disarm: () => {
      escapeAt = null;
    },
  };
}

/**
 * Give a terminal the Escape-then-Tab exit and point its input at the hint
 * that explains it. Losing focus drops a pending Escape, so a Tab after focus
 * came back is sent to the shell. Call after `term.open()`, since the input
 * textarea is created there.
 */
export function attachTerminalExitKeys(term: Terminal, hintId: string): void {
  const keys = createTerminalExitKeys();
  term.attachCustomKeyEventHandler(keys.handleKey);
  term.textarea?.setAttribute('aria-describedby', hintId);
  term.textarea?.addEventListener('blur', keys.disarm);
}
