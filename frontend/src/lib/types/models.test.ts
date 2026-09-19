import { describe, it, expect } from 'vitest';
import { isNoAcousticModelState, NO_ACOUSTIC_MODEL_STATES } from './models';

describe('isNoAcousticModelState', () => {
  it.each(NO_ACOUSTIC_MODEL_STATES)('accepts the %s verdict', state => {
    expect(isNoAcousticModelState(state)).toBe(true);
  });

  // Allow-list: "ok", the "" sentinel, null, unknown strings and non-strings
  // must never trigger a no-model UI.
  it.each([
    ['ok', 'ok'],
    ['the empty sentinel', ''],
    ['null', null],
    ['undefined', undefined],
    ['a differently cased verdict', 'NONE_INSTALLED'],
    ['an unknown future verdict', 'degraded'],
    ['a number', 0],
    ['an object', {}],
  ])('rejects %s', (_label, value) => {
    expect(isNoAcousticModelState(value)).toBe(false);
  });
});
