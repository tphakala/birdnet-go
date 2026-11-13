import { describe, it, expect } from 'vitest';
import { createTerminalExitKeys, TAB_RELEASE_WINDOW_MS } from './terminalKeys';

type KeyInit = Partial<Pick<KeyboardEvent, 'ctrlKey' | 'altKey' | 'metaKey' | 'shiftKey'>>;

function key(type: string, k: string, init: KeyInit = {}): KeyboardEvent {
  return new KeyboardEvent(type, { key: k, ...init });
}

function setup() {
  let clock = 1000;
  const handler = createTerminalExitKeys(() => clock);
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

  it('passes Tab once the window after Escape has elapsed, and not before', () => {
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

  it('does not release Tab combined with Ctrl, Alt or Meta', () => {
    const { handler } = setup();
    handler(key('keydown', 'Escape'));
    expect(handler(key('keydown', 'Tab', { altKey: true }))).toBe(true);
  });

  it('always passes keyup and keypress events', () => {
    const { handler } = setup();
    handler(key('keydown', 'Escape'));
    expect(handler(key('keyup', 'Tab'))).toBe(true);
    expect(handler(key('keypress', 'Tab'))).toBe(true);
    expect(handler(key('keydown', 'Tab'))).toBe(false);
  });
});
