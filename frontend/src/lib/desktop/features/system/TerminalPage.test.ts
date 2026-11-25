import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, fireEvent, waitFor, screen } from '@testing-library/svelte';
import { tick } from 'svelte';
import { get } from 'svelte/store';

type KeyHandler = (ev: KeyboardEvent) => boolean;

interface MockTerminal {
  textarea?: HTMLTextAreaElement;
  handler?: KeyHandler;
  focusCount: number;
  selection: string;
}

// Every Terminal the page creates, so tests can reach its input, key handler and focus calls
const created = vi.hoisted(() => {
  const terminals: MockTerminal[] = [];
  return { terminals };
});

vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    // Like xterm, the input only exists once the terminal is opened
    textarea?: HTMLTextAreaElement;
    handler?: (ev: KeyboardEvent) => boolean;
    focusCount = 0;
    selection = '';
    constructor() {
      created.terminals.push(this);
    }
    cols = 80;
    rows = 24;
    options = {};
    loadAddon() {}
    open(container: HTMLElement) {
      // Like xterm, the input lives in the document of the container it is opened in
      this.textarea = container.ownerDocument.createElement('textarea');
      container.appendChild(this.textarea);
    }
    focus() {
      this.focusCount++;
    }
    write() {}
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
      return this.selection;
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
vi.mock('$lib/utils/clipboard', async importOriginal => ({
  ...(await importOriginal<typeof import('$lib/utils/clipboard')>()),
  copyToClipboard: vi.fn(() => Promise.resolve(true)),
}));

import { settingsStore } from '$lib/stores/settings';
import { copyToClipboard } from '$lib/utils/clipboard';
import { opacityClasses } from '../../../../test/class-helpers';
import TerminalPage from './TerminalPage.svelte';

// The test setup returns translation keys as text
function key(k: string) {
  return new KeyboardEvent('keydown', { key: k });
}

const TOOLBAR_BUTTON_LABELS = [
  'terminal.colorTheme',
  'terminal.copySelection',
  'terminal.detach',
  'terminal.expand',
  'terminal.fullscreen',
];

const THEME_STORAGE_KEY = 'birdnet-terminal-theme';

// Undo what the page tests change outside their own DOM: settings, globals and the saved theme
function restoreGlobalsAfterEach() {
  let settingsSnapshot = get(settingsStore);
  beforeEach(() => {
    settingsSnapshot = get(settingsStore);
  });
  afterEach(() => {
    settingsStore.set(settingsSnapshot);
    vi.unstubAllGlobals();
    localStorage.removeItem(THEME_STORAGE_KEY);
  });
}

function setTerminalEnabled(enableTerminal: boolean) {
  const state = get(settingsStore);
  settingsStore.set({
    ...state,
    originalData: {
      ...state.originalData,
      webServer: { ...state.originalData.webServer, enableTerminal },
    },
  });
}

function enableTerminal() {
  setTerminalEnabled(true);
}

describe('TerminalPage toolbar', () => {
  restoreGlobalsAfterEach();

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
    expect(handler?.(key('Escape'))).toBe(true);
    expect(handler?.(key('Tab'))).toBe(false);
  });

  it('forgets an armed Escape when the inline terminal input loses focus', async () => {
    render(TerminalPage);
    await tick();
    const [terminal] = created.terminals;

    terminal.handler?.(key('Escape'));
    terminal.textarea?.dispatchEvent(new FocusEvent('blur'));

    expect(terminal.handler?.(key('Tab'))).toBe(true);
  });

  it('hands focus back to the terminal after Copy, Expand and Fullscreen', async () => {
    const { getByRole } = render(TerminalPage);
    await tick();
    const [terminal] = created.terminals;
    // The first connect on page load leaves focus where it is
    expect(terminal.focusCount).toBe(0);

    await fireEvent.click(getByRole('button', { name: 'terminal.copySelection' }));
    expect(terminal.focusCount).toBe(1);

    await fireEvent.click(getByRole('button', { name: 'terminal.expand' }));
    await waitFor(() => expect(terminal.focusCount).toBe(2));

    // jsdom has no Fullscreen API: stand in for the browser entering fullscreen
    Object.defineProperty(HTMLElement.prototype, 'requestFullscreen', {
      configurable: true,
      value: () => {
        document.dispatchEvent(new Event('fullscreenchange'));
        return Promise.resolve();
      },
    });
    try {
      await fireEvent.click(getByRole('button', { name: 'terminal.fullscreen' }));
      await waitFor(() => expect(terminal.focusCount).toBe(3));
    } finally {
      Reflect.deleteProperty(HTMLElement.prototype, 'requestFullscreen');
    }
  });

  it('copies a non-empty selection and then hands focus back to the terminal', async () => {
    const { getByRole } = render(TerminalPage);
    await tick();
    const [terminal] = created.terminals;
    terminal.selection = 'ls -la';

    await fireEvent.click(getByRole('button', { name: 'terminal.copySelection' }));

    expect(copyToClipboard).toHaveBeenCalledWith('ls -la');
    await waitFor(() => expect(terminal.focusCount).toBe(1));
  });
});

describe('TerminalPage detached window', () => {
  restoreGlobalsAfterEach();

  afterEach(() => {
    document.querySelectorAll('iframe').forEach(frame => frame.remove());
    vi.restoreAllMocks();
  });

  // Foreground colours of the three terminal themes, as computed style reports them
  const DARK_FOREGROUND = 'rgb(169, 177, 214)';
  // Every popup element drawn in the theme foreground: status texts and the theme swatch buttons
  const TOOLBAR_TEXT_SELECTORS = [
    '#status-text',
    '#dimensions',
    '#terminal-exit-hint',
    '#theme-dark',
    '#theme-light',
    '#theme-hc',
  ];

  // Every socket the page opens; the last one belongs to the popup
  const sockets: { onclose?: () => void }[] = [];

  // Renders the page with a live inline terminal and an iframe standing in for the popup, and
  // clicks Detach. The popup toolbar wiring still waits for the popup's DOM settle delay.
  async function startDetach({ withoutTerminalElement = false } = {}) {
    sockets.length = 0;
    vi.stubGlobal(
      'WebSocket',
      class {
        onclose?: () => void;
        constructor() {
          sockets.push(this);
        }
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
    if (withoutTerminalElement) {
      // The popup page failed to build: the page looks for its terminal element and finds none
      const lookUp = popup.document.getElementById.bind(popup.document);
      vi.spyOn(popup.document, 'getElementById').mockImplementation(id =>
        id === 'terminal' ? null : lookUp(id)
      );
    }

    const { getByRole } = render(TerminalPage);
    await tick();
    await fireEvent.click(getByRole('button', { name: 'terminal.detach' }));
    return popup;
  }

  async function detach() {
    const popup = await startDetach();
    // The popup toolbar wiring runs after the popup's DOM settle delay
    await waitFor(() => expect(popup.document.getElementById('theme-light')).not.toBeNull());
    await waitFor(() => expect(created.terminals.length).toBeGreaterThan(1));
    return popup.document;
  }

  // Focus calls on the terminals rebuilt in the main window after the first one was torn down
  function rebuiltInlineFocusCount() {
    return created.terminals
      .slice(1)
      .filter(terminal => terminal.textarea?.ownerDocument === document)
      .reduce((sum, terminal) => sum + terminal.focusCount, 0);
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
    expect(handler?.(key('Escape'))).toBe(true);
    expect(handler?.(key('Tab'))).toBe(false);
  });

  it('lets the popup toolbar grow and wrap instead of clipping a long hint', async () => {
    const doc = await detach();
    const toolbarRule = [...doc.styleSheets]
      .flatMap(sheet => [...sheet.cssRules])
      .find(rule => 'selectorText' in rule && rule.selectorText === '#toolbar');
    expect(toolbarRule?.cssText).toContain('min-height: 40px');
    expect(toolbarRule?.cssText).toContain('flex-wrap: wrap');
    expect(toolbarRule?.cssText).not.toMatch(/(^|[\s{;])height:/);
  });

  it('keeps the status text class when the popup connection closes', async () => {
    const doc = await detach();
    sockets.at(-1)?.onclose?.();

    const status = doc.getElementById('status-text');
    expect(status?.textContent).toBe('terminal.disconnected');
    expect(status?.className).toBe('status-text');
  });

  it('draws a full-strength outline on a focused popup toolbar or swatch button', async () => {
    const doc = await detach();
    // Rules come from the popup's own window, so test them by shape, not by instanceof
    const focusRule = [...doc.styleSheets]
      .flatMap(sheet => [...sheet.cssRules])
      .find(
        rule =>
          'selectorText' in rule &&
          rule.selectorText === '.toolbar-btn:focus-visible, .swatch-btn:focus-visible'
      );
    expect(focusRule?.cssText).toContain('opacity: 1');
    // A real outline, so forced colors still draws the ring
    expect(focusRule?.cssText).toContain('outline: 2px solid currentColor');
  });

  it('moves focus to the detached notice', async () => {
    await detach();

    const notice = screen.getByText('terminal.detached').closest<HTMLElement>('[tabindex="-1"]');
    expect(notice).not.toBeNull();
    await waitFor(() => expect(document.activeElement).toBe(notice));
  });

  it('hands focus back to the terminal after the popup closes', async () => {
    const doc = await detach();
    const [inlineTerminal] = created.terminals;
    expect(inlineTerminal.focusCount).toBe(0);

    doc.defaultView?.dispatchEvent(new Event('beforeunload'));

    // The inline terminal is rebuilt in the main window once the popup has gone
    await waitFor(() => expect(created.terminals.length).toBe(3));
    await waitFor(() => expect(created.terminals[2].focusCount).toBe(1));
  });

  // A cancelled leave prompt must not close the popout of a page that stays.
  it('keeps the popup open when the page only asks to unload', async () => {
    const doc = await detach();
    const popup = doc.defaultView;
    if (!popup) throw new Error('popup has no window');
    const close = vi.spyOn(popup, 'close').mockImplementation(() => {});

    window.dispatchEvent(new Event('beforeunload', { cancelable: true }));

    expect(close).not.toHaveBeenCalled();
  });

  it('closes the popup when the page goes away', async () => {
    const doc = await detach();
    const popup = doc.defaultView;
    if (!popup) throw new Error('popup has no window');
    const close = vi.spyOn(popup, 'close').mockImplementation(() => {});

    window.dispatchEvent(new Event('pagehide'));

    expect(close).toHaveBeenCalledTimes(1);
  });

  it('hands focus back to the terminal after the popup closes before it has settled', async () => {
    const popup = await startDetach();

    popup.dispatchEvent(new Event('beforeunload'));

    await waitFor(() => expect(rebuiltInlineFocusCount()).toBe(1));
  });

  it('hands focus back to the terminal when the popup page cannot be built', async () => {
    await startDetach({ withoutTerminalElement: true });

    await waitFor(() => expect(rebuiltInlineFocusCount()).toBe(1));
  });

  it('does not take focus on a later reconnect when the popup closed while the terminal was disabled', async () => {
    const doc = await detach();
    setTerminalEnabled(false);
    await tick();

    doc.defaultView?.dispatchEvent(new Event('beforeunload'));
    await tick();
    setTerminalEnabled(true);

    // The terminal is rebuilt in the background, unprompted by the user
    await waitFor(() =>
      expect(
        created.terminals.some(
          t => t.textarea?.ownerDocument === document && t !== created.terminals[0]
        )
      ).toBe(true)
    );
    expect(rebuiltInlineFocusCount()).toBe(0);
  });

  it('does not take focus on a later reconnect when the terminal was disabled right after the popup closed', async () => {
    const doc = await detach();

    // Both happen before the page has reconnected the terminal
    doc.defaultView?.dispatchEvent(new Event('beforeunload'));
    setTerminalEnabled(false);
    await tick();
    setTerminalEnabled(true);

    await waitFor(() =>
      expect(
        created.terminals.some(
          terminal =>
            terminal.textarea?.ownerDocument === document && terminal !== created.terminals[0]
        )
      ).toBe(true)
    );
    expect(rebuiltInlineFocusCount()).toBe(0);
  });

  it('draws the detached notice focus ring inside the notice, which fills a clipping wrapper', async () => {
    await detach();

    const notice = screen.getByText('terminal.detached').closest<HTMLElement>('[tabindex="-1"]');
    expect(notice).toHaveClass('focus-ring-inset');
    expect(notice).not.toHaveClass('focus-ring');
  });

  it('forgets an armed Escape when the detached terminal input loses focus', async () => {
    await detach();
    const popupTerminal = created.terminals[1];

    popupTerminal.handler?.(key('Escape'));
    popupTerminal.textarea?.dispatchEvent(new FocusEvent('blur'));

    expect(popupTerminal.handler?.(key('Tab'))).toBe(true);
  });
});
