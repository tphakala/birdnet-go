import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render } from '@testing-library/svelte';
import { tick } from 'svelte';
import { get } from 'svelte/store';

type KeyHandler = (ev: KeyboardEvent) => boolean;

// Every Terminal the page creates, so tests can reach its input and its key handler
const created = vi.hoisted(() => ({
  terminals: [] as { textarea?: HTMLTextAreaElement; handler?: (ev: KeyboardEvent) => boolean }[],
}));

vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    // Like xterm, the input only exists once the terminal is opened
    textarea?: HTMLTextAreaElement;
    handler?: (ev: KeyboardEvent) => boolean;
    constructor() {
      created.terminals.push(this);
    }
    cols = 80;
    rows = 24;
    options = {};
    loadAddon() {}
    open() {
      this.textarea = document.createElement('textarea');
    }
    focus() {}
    dispose() {}
    onData() {
      return { dispose() {} };
    }
    onResize() {
      return { dispose() {} };
    }
    attachCustomKeyEventHandler(handler: (ev: KeyboardEvent) => boolean) {
      this.handler = handler;
    }
    getSelection() {
      return '';
    }
  },
}));
vi.mock('@xterm/addon-fit', () => ({
  FitAddon: class {
    fit() {}
  },
}));
vi.mock('@xterm/xterm/css/xterm.css', () => ({}));
vi.mock('@xterm/xterm/css/xterm.css?raw', () => ({ default: '' }));

import { settingsStore } from '$lib/stores/settings';
import TerminalPage from './TerminalPage.svelte';

// The test setup returns translation keys as text
const TOOLBAR_BUTTON_LABELS = [
  'terminal.colorTheme',
  'terminal.copySelection',
  'terminal.detach',
  'terminal.expand',
  'terminal.fullscreen',
];

function enableTerminal() {
  const state = get(settingsStore);
  settingsStore.set({
    ...state,
    originalData: {
      ...state.originalData,
      webServer: { ...state.originalData.webServer, enableTerminal: true },
    },
  });
}

describe('TerminalPage toolbar', () => {
  beforeEach(() => {
    vi.stubGlobal(
      'WebSocket',
      class {
        close() {}
      }
    );
    created.terminals.length = 0;
    enableTerminal();
  });

  it('keeps every toolbar button in the Tab order', async () => {
    const { getByRole } = render(TerminalPage);
    await tick();
    for (const label of TOOLBAR_BUTTON_LABELS) {
      expect(getByRole('button', { name: label }).getAttribute('tabindex')).toBeNull();
    }
  });

  it('dims toolbar icons through colour, so the focus ring stays at full strength', async () => {
    const { getByRole } = render(TerminalPage);
    await tick();
    for (const label of TOOLBAR_BUTTON_LABELS) {
      const button = getByRole('button', { name: label });
      expect(button.style.opacity).toBe('');
      expect(button.className).not.toMatch(/(^|\s)(hover:)?opacity-/);
    }
  });

  it('keeps a real outline on keyboard focus so forced colors still shows one', async () => {
    const { getByRole } = render(TerminalPage);
    await tick();
    for (const label of TOOLBAR_BUTTON_LABELS) {
      const classes = getByRole('button', { name: label }).className;
      expect(classes).toContain('focus-visible:outline-2');
      expect(classes).not.toContain('focus-visible:outline-none');
    }
  });

  it('wires the exit keys and the hint of the inline terminal', async () => {
    const { container } = render(TerminalPage);
    await tick();

    const [terminal] = created.terminals;
    const hint = container.querySelector(`#${terminal.textarea?.getAttribute('aria-describedby')}`);
    expect(hint?.textContent.trim()).toBe('terminal.keyboardExitHint');

    const handler: KeyHandler | undefined = terminal.handler;
    const key = (k: string) => new KeyboardEvent('keydown', { key: k });
    expect(handler?.(key('Escape'))).toBe(true);
    expect(handler?.(key('Tab'))).toBe(false);
  });
});
