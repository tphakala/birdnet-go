import { describe, it, expect } from 'vitest';
import { isBirdweatherToken } from './birdweather';

const TOKEN = 'aB3dEf6hIj9lMn2pQr5tUv8x';

describe('isBirdweatherToken', () => {
  it.each([
    ['24 mixed-case letters and digits', TOKEN, true],
    ['a token padded with spaces', `  ${TOKEN}  `, true],
    ['a token padded with a tab and newline', `\t${TOKEN}\n`, true],
    ['23 characters', TOKEN.slice(1), false],
    ['25 characters', `${TOKEN}a`, false],
    ['a hyphen', `${TOKEN.slice(1)}-`, false],
    ['an underscore', `${TOKEN.slice(1)}_`, false],
    ['an inner space', `${TOKEN.slice(0, 11)} ${TOKEN.slice(12)}`, false],
    ['a non-ASCII letter', `${TOKEN.slice(1)}ä`, false],
    ['a full-width digit', `${TOKEN.slice(1)}０`, false],
    ['an empty string', '', false],
    ['whitespace only', '   ', false],
    ['the value from the report', 'TESTID123', false],
  ])('%s', (_name, value, valid) => {
    expect(isBirdweatherToken(value)).toBe(valid);
  });
});
