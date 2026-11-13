import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, fireEvent, waitFor } from '@testing-library/svelte';
import { tick } from 'svelte';
import { get } from 'svelte/store';

type KeyHandler = (ev: KeyboardEvent) => boolean;

// Every Terminal the page creates, so tests can reach its input and its key handler
const created = vi.hoisted(() => ({
  terminals: [] as { textarea?: HTMLTextAreaElement; handler?: KeyHandler; focusCount: number }[],
}));

vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    // Like xterm, the input only exists once the terminal is opened
    textarea?: HTMLTextAreaElement;
    handler?: (ev: KeyboardEvent) => boolean;
    focusCount = 0;
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
    focus() {
      this.focusCount++;
    }
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
import { opacityClasses } from '../../../../test/class-helpers';
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
      expect(opacityClasses(button)).toEqual([]);
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

  it('hands focus back to the terminal after Copy, Expand and Fullscreen', async () => {
    const { getByRole } = render(TerminalPage);
    await tick();
    const [terminal] = created.terminals;

    await fireEvent.click(getByRole('button', { name: 'terminal.copySelection' }));
    expect(terminal.focusCount).toBe(1);

    await fireEvent.click(getByRole('button', { name: 'terminal.expand' }));
    await waitFor(() => expect(terminal.focusCount).toBe(2));

    document.dispatchEvent(new Event('fullscreenchange'));
    await waitFor(() => expect(terminal.focusCount).toBe(3));
  });
});

describe('TerminalPage detached window', () => {
  afterEach(() => {
    document.querySelectorAll('iframe').forEach(frame => frame.remove());
    vi.restoreAllMocks();
  });

  // Foreground colours of the three terminal themes, as computed style reports them
  const DARK_FOREGROUND = 'rgb(169, 177, 214)';
  const TOOLBAR_TEXT_SELECTORS = ['#status-text', '#dimensions', '#terminal-exit-hint'];

  async function detach() {
    vi.stubGlobal(
      'WebSocket',
      class {
        close() {}
        send() {}
      }
    );
    created.terminals.length = 0;
    enableTerminal();
    const frame = document.createElement('iframe');
    document.body.appendChild(frame);
    const popup = frame.contentWindow;
    if (!popup) throw new Error('iframe has no window');
    vi.spyOn(window, 'open').mockReturnValue(popup);

    const { getByRole } = render(TerminalPage);
    await tick();
    await fireEvent.click(getByRole('button', { name: 'terminal.detach' }));
    // The popup toolbar wiring runs after the popup's DOM settle delay
    await waitFor(() => expect(popup.document.getElementById('theme-light')).not.toBeNull());
    await waitFor(() => expect(created.terminals.length).toBeGreaterThan(1));
    return popup.document;
  }

  // Computed colour and opacity of each toolbar text element
  function toolbarText(doc: Document) {
    return TOOLBAR_TEXT_SELECTORS.map(selector => {
      const el = doc.querySelector(selector);
      if (!el) throw new Error(`missing ${selector}`);
      const style = doc.defaultView?.getComputedStyle(el);
      return { selector, color: style?.color, opacity: style?.opacity };
    });
  }

  const colors = (doc: Document) => toolbarText(doc).map(t => t.color);
  const all = (color: string) => TOOLBAR_TEXT_SELECTORS.map(() => color);

  it('draws every toolbar text in the active theme foreground and follows theme switches', async () => {
    const doc = await detach();
    expect(colors(doc)).toEqual(all(DARK_FOREGROUND));

    doc.getElementById('theme-light')?.click();
    expect(colors(doc)).toEqual(all('rgb(56, 58, 66)'));

    doc.getElementById('theme-hc')?.click();
    expect(colors(doc)).toEqual(all('rgb(0, 255, 0)'));

    doc.getElementById('theme-dark')?.click();
    expect(colors(doc)).toEqual(all(DARK_FOREGROUND));
  });

  it('keeps toolbar text at full opacity so it reaches 4.5:1', async () => {
    const doc = await detach();
    expect(toolbarText(doc).map(t => t.opacity === '' || t.opacity === '1')).toEqual(
      TOOLBAR_TEXT_SELECTORS.map(() => true)
    );
  });

  it('wires the exit keys and the hint to the detached terminal', async () => {
    const doc = await detach();
    const popupTerminal = created.terminals[1];

    const hint = doc.getElementById(popupTerminal.textarea?.getAttribute('aria-describedby') ?? '');
    expect(hint?.textContent.trim()).toBe('terminal.keyboardExitHint');

    const handler: KeyHandler | undefined = popupTerminal.handler;
    expect(handler?.(new KeyboardEvent('keydown', { key: 'Escape' }))).toBe(true);
    expect(handler?.(new KeyboardEvent('keydown', { key: 'Tab' }))).toBe(false);
  });

  it('lifts a focused popup toolbar button to full opacity', async () => {
    const doc = await detach();
    // Rules come from the popup's own window, so test them by shape, not by instanceof
    const focusRule = [...doc.styleSheets]
      .flatMap(sheet => [...sheet.cssRules])
      .find(rule => 'selectorText' in rule && rule.selectorText === '.toolbar-btn:focus-visible');
    expect(focusRule?.cssText).toContain('opacity: 1');
  });
});
