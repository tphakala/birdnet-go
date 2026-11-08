import { describe, it, expect } from 'vitest';
import {
  SEEK_PAGE_STEP_SECONDS,
  SEEK_STEP_SECONDS,
  clampSeconds,
  seekKeyAction,
  seekTargetForKey,
  seekTimeFromPointer,
  usableDuration,
} from './seek';

const CLIP_SECONDS = 29;
const BAR_LEFT_PX = 100;
const BAR_WIDTH_PX = 200;

function bar(width = BAR_WIDTH_PX): DOMRect {
  return new DOMRect(BAR_LEFT_PX, 0, width, 0);
}

describe('clampSeconds', () => {
  it.each([
    [-1, 0],
    [0, 0],
    [12.5, 12.5],
    [CLIP_SECONDS, CLIP_SECONDS],
    [CLIP_SECONDS + 1, CLIP_SECONDS],
  ])('clamps %s to %s', (input, expected) => {
    expect(clampSeconds(input, CLIP_SECONDS)).toBe(expected);
  });
});

describe('seekTargetForKey', () => {
  it.each([
    ['ArrowRight', 10, 10 + SEEK_STEP_SECONDS],
    ['ArrowUp', 10, 10 + SEEK_STEP_SECONDS],
    ['ArrowLeft', 10, 10 - SEEK_STEP_SECONDS],
    ['ArrowDown', 10, 10 - SEEK_STEP_SECONDS],
    ['PageUp', 10, 10 + SEEK_PAGE_STEP_SECONDS],
    ['PageDown', 10, 10 - SEEK_PAGE_STEP_SECONDS],
    ['Home', 10, 0],
    ['End', 10, CLIP_SECONDS],
  ])('%s from %s seconds goes to %s', (key, current, expected) => {
    expect(seekTargetForKey(key, current, CLIP_SECONDS)).toBe(expected);
  });

  it.each([
    ['ArrowLeft', 0.4, 0],
    ['ArrowRight', CLIP_SECONDS - 0.4, CLIP_SECONDS],
    ['PageDown', 3, 0],
    ['PageUp', CLIP_SECONDS - 3, CLIP_SECONDS],
  ])('%s from %s seconds stops at the clip edge %s', (key, current, expected) => {
    expect(seekTargetForKey(key, current, CLIP_SECONDS)).toBe(expected);
  });

  it.each(['Enter', ' ', 'Escape', 'a', 'Tab'])('ignores %j', key => {
    expect(seekTargetForKey(key, 10, CLIP_SECONDS)).toBeNull();
  });

  it.each([0, -3, Number.NaN, Number.POSITIVE_INFINITY])(
    'does nothing for a clip length of %s',
    max => {
      expect(seekTargetForKey('End', 0, max)).toBeNull();
      expect(seekTargetForKey('ArrowRight', 0, max)).toBeNull();
    }
  );
});

describe('seekTimeFromPointer', () => {
  it.each([
    [BAR_LEFT_PX, 0],
    [BAR_LEFT_PX + BAR_WIDTH_PX / 2, CLIP_SECONDS / 2],
    [BAR_LEFT_PX + BAR_WIDTH_PX, CLIP_SECONDS],
  ])('maps clientX %s to %s seconds', (clientX, expected) => {
    expect(seekTimeFromPointer(clientX, bar(), CLIP_SECONDS)).toBeCloseTo(expected, 10);
  });

  it('clamps a pointer left of the bar to the start and right of it to the end', () => {
    expect(seekTimeFromPointer(BAR_LEFT_PX - 50, bar(), CLIP_SECONDS)).toBe(0);
    expect(seekTimeFromPointer(BAR_LEFT_PX + BAR_WIDTH_PX + 50, bar(), CLIP_SECONDS)).toBe(
      CLIP_SECONDS
    );
  });

  it('returns null for a bar with no width', () => {
    expect(seekTimeFromPointer(BAR_LEFT_PX, bar(0), CLIP_SECONDS)).toBeNull();
  });

  it.each([0, Number.NaN, Number.POSITIVE_INFINITY])(
    'returns null for a clip length of %s',
    max => {
      expect(seekTimeFromPointer(BAR_LEFT_PX + 10, bar(), max)).toBeNull();
    }
  );
});

describe('usableDuration', () => {
  it.each([
    [CLIP_SECONDS, CLIP_SECONDS],
    [0.5, 0.5],
    [0, 0],
    [-3, 0],
    [Number.NaN, 0],
    [Number.POSITIVE_INFINITY, 0],
  ])('maps a duration of %s to %s', (duration, expected) => {
    expect(usableDuration(duration)).toBe(expected);
  });
});

describe('seekKeyAction', () => {
  const key = (k: string, mods: Partial<KeyboardEvent> = {}) => ({
    key: k,
    altKey: false,
    ctrlKey: false,
    metaKey: false,
    ...mods,
  });

  it('seeks and cancels the event for a seek key', () => {
    expect(seekKeyAction(key('ArrowRight'), 10, CLIP_SECONDS)).toEqual({
      target: 10 + SEEK_STEP_SECONDS,
      preventDefault: true,
    });
  });

  it.each([0, Number.NaN])('cancels Space without seeking for a clip length of %s', max => {
    expect(seekKeyAction(key(' '), 0, max)).toEqual({ target: null, preventDefault: true });
  });

  it('cancels Space without seeking for a known clip too', () => {
    expect(seekKeyAction(key(' '), 10, CLIP_SECONDS)).toEqual({
      target: null,
      preventDefault: true,
    });
  });

  it.each(['altKey', 'ctrlKey', 'metaKey'] as const)(
    'leaves a seek key held with %s to the browser',
    modifier => {
      expect(seekKeyAction(key('ArrowLeft', { [modifier]: true }), 10, CLIP_SECONDS)).toEqual({
        target: null,
        preventDefault: false,
      });
      expect(seekKeyAction(key(' ', { [modifier]: true }), 10, CLIP_SECONDS)).toEqual({
        target: null,
        preventDefault: false,
      });
    }
  );

  it('treats Shift as no modifier, so Shift+End still seeks', () => {
    expect(seekKeyAction(key('End', { shiftKey: true }), 10, CLIP_SECONDS)).toEqual({
      target: CLIP_SECONDS,
      preventDefault: true,
    });
  });

  it.each(['Enter', 'Escape', 'Tab', 'a'])('leaves %j to the browser', k => {
    expect(seekKeyAction(key(k), 10, CLIP_SECONDS)).toEqual({
      target: null,
      preventDefault: false,
    });
  });

  it('leaves a seek key alone while the clip length is unknown', () => {
    expect(seekKeyAction(key('End'), 0, 0)).toEqual({ target: null, preventDefault: false });
  });
});
