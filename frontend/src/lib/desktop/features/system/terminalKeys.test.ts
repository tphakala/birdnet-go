import { describe, it, expect } from 'vitest';
import type { Terminal } from '@xterm/xterm';
import {
  attachTerminalExitKeys,
  createTerminalExitKeys,
  TAB_RELEASE_WINDOW_MS,
} from './terminalKeys';

type KeyInit = Partial<
  Pick<KeyboardEvent, 'ctrlKey' | 'altKey' | 'metaKey' | 'shiftKey' | 'isComposing' | 'repeat'>
> & { keyCode?: number };

function key(type: string, k: string, init: KeyInit = {}): KeyboardEvent {
  return new KeyboardEvent(type, { key: k, ...init });
}

function setup() {
  let clock = 1000;
  const { handleKey: handler } = createTerminalExitKeys(() => clock);
  return {
    handler,
    advance: (ms: number) => {
      clock += ms;
    },
  };
}

describe('createTerminalExitKeys', () => {
  it('passes Escape to the terminal', () => {
    const { handler } = setup();
    expect(handler(key('keydown', 'Escape'))).toBe(true);
  });

  it('releases Tab pressed right after Escape', () => {
    const { handler, advance } = setup();
    handler(key('keydown', 'Escape'));
    advance(300);
    expect(handler(key('keydown', 'Tab'))).toBe(false);
  });

  it('releases Shift+Tab after Escape, including the bare Shift keydown in between', () => {
    const { handler, advance } = setup();
    handler(key('keydown', 'Escape'));
    advance(100);
    expect(handler(key('keydown', 'Shift', { shiftKey: true }))).toBe(true);
    advance(100);
    expect(handler(key('keydown', 'Tab', { shiftKey: true }))).toBe(false);
  });

  it('keeps sending a plain Tab to the terminal', () => {
    const { handler } = setup();
    expect(handler(key('keydown', 'Tab'))).toBe(true);
    expect(handler(key('keydown', 'Tab', { shiftKey: true }))).toBe(true);
  });

  it('releases Tab at exactly the window edge and passes it one millisecond later', () => {
    const { handler, advance } = setup();
    handler(key('keydown', 'Escape'));
    advance(TAB_RELEASE_WINDOW_MS);
    expect(handler(key('keydown', 'Tab'))).toBe(false);

    handler(key('keydown', 'Escape'));
    advance(TAB_RELEASE_WINDOW_MS + 1);
    expect(handler(key('keydown', 'Tab'))).toBe(true);
  });

  it('forgets the Escape after another key is typed', () => {
    const { handler } = setup();
    handler(key('keydown', 'Escape'));
    expect(handler(key('keydown', 'a'))).toBe(true);
    expect(handler(key('keydown', 'Tab'))).toBe(true);
  });

  it('releases only one Tab per Escape', () => {
    const { handler } = setup();
    handler(key('keydown', 'Escape'));
    expect(handler(key('keydown', 'Tab'))).toBe(false);
    expect(handler(key('keydown', 'Tab'))).toBe(true);
  });

  it('does not treat Escape with a modifier as the exit prefix', () => {
    const { handler } = setup();
    handler(key('keydown', 'Escape', { ctrlKey: true }));
    expect(handler(key('keydown', 'Tab'))).toBe(true);
  });

  it.each(['ctrlKey', 'altKey', 'metaKey'] as const)(
    'does not release Tab combined with %s',
    modifier => {
      const { handler } = setup();
      handler(key('keydown', 'Escape'));
      expect(handler(key('keydown', 'Tab', { [modifier]: true }))).toBe(true);
    }
  );

  it.each([
    ['isComposing', 'Escape', { isComposing: true }],
    ['the Process key', 'Process', {}],
    ['keyCode 229', 'Escape', { keyCode: 229 }],
  ] as const)('does not arm the exit on %s', (_name, k, init) => {
    const { handler } = setup();
    expect(handler(key('keydown', k, init))).toBe(true);
    expect(handler(key('keydown', 'Tab'))).toBe(true);
  });

  it.each([
    ['isComposing', 'Tab', { isComposing: true }],
    ['the Process key', 'Process', {}],
    ['keyCode 229', 'Tab', { keyCode: 229 }],
  ] as const)('does not release Tab and drops the armed Escape on %s', (_name, k, init) => {
    const { handler } = setup();
    handler(key('keydown', 'Escape'));
    expect(handler(key('keydown', k, init))).toBe(true);
    expect(handler(key('keydown', 'Tab'))).toBe(true);
  });

  it('never releases on a repeating Tab and keeps the Escape armed', () => {
    const { handler } = setup();
    handler(key('keydown', 'Escape'));
    expect(handler(key('keydown', 'Tab', { repeat: true }))).toBe(true);
    expect(handler(key('keydown', 'Tab'))).toBe(false);
  });

  it('does not refresh the window on a repeating Escape', () => {
    const { handler, advance } = setup();
    handler(key('keydown', 'Escape'));
    advance(TAB_RELEASE_WINDOW_MS - 100);
    expect(handler(key('keydown', 'Escape', { repeat: true }))).toBe(true);
    advance(200);
    expect(handler(key('keydown', 'Tab'))).toBe(true);
  });

  it('does not arm on a repeating Escape alone', () => {
    const { handler } = setup();
    handler(key('keydown', 'Escape', { repeat: true }));
    expect(handler(key('keydown', 'Tab'))).toBe(true);
  });

  it('does not release when the clock stepped backward after Escape', () => {
    const { handler, advance } = setup();
    handler(key('keydown', 'Escape'));
    advance(-500);
    expect(handler(key('keydown', 'Tab'))).toBe(true);
  });

  it('always passes keyup and keypress events', () => {
    const { handler } = setup();
    handler(key('keydown', 'Escape'));
    expect(handler(key('keyup', 'Tab'))).toBe(true);
    expect(handler(key('keypress', 'Tab'))).toBe(true);
    expect(handler(key('keydown', 'Tab'))).toBe(false);
  });
});

describe('attachTerminalExitKeys', () => {
  type KeyHandler = (ev: KeyboardEvent) => boolean;

  function stubTerminal(textarea: HTMLTextAreaElement | undefined) {
    const captured: { handler?: KeyHandler } = {};
    const term = {
      textarea,
      attachCustomKeyEventHandler(handler: KeyHandler) {
        captured.handler = handler;
      },
    };
    // Partial stub: xterm needs a real layout engine, only the two members used here exist.
    return { captured, term: term as unknown as Terminal };
  }

  it('registers the exit handler and describes the input by the hint', () => {
    const textarea = document.createElement('textarea');
    const { captured, term } = stubTerminal(textarea);

    attachTerminalExitKeys(term, 'hint-id');

    expect(textarea.getAttribute('aria-describedby')).toBe('hint-id');
    expect(captured.handler?.(key('keydown', 'Escape'))).toBe(true);
    expect(captured.handler?.(key('keydown', 'Tab'))).toBe(false);
  });

  it('forgets an armed Escape when the terminal input loses focus', () => {
    const textarea = document.createElement('textarea');
    const { captured, term } = stubTerminal(textarea);
    attachTerminalExitKeys(term, 'hint-id');

    captured.handler?.(key('keydown', 'Escape'));
    textarea.dispatchEvent(new FocusEvent('blur'));

    expect(captured.handler?.(key('keydown', 'Tab'))).toBe(true);
  });

  it('still registers the handler when the terminal has no input element', () => {
    const { captured, term } = stubTerminal(undefined);

    expect(() => attachTerminalExitKeys(term, 'hint-id')).not.toThrow();
    expect(captured.handler).toBeDefined();
  });
});
