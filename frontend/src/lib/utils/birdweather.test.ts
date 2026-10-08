import { describe, it, expect } from 'vitest';
import { birdweatherTokenProblem, isBirdweatherToken } from './birdweather';

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

describe('birdweatherTokenProblem', () => {
  it.each([
    ['off with an empty token', false, '', null],
    ['off with a malformed token', false, 'abc', null],
    ['off with a well-formed token', false, TOKEN, null],
    ['off with a missing token', false, undefined, null],
    ['on with an empty token', true, '', 'required'],
    ['on with a missing token', true, undefined, 'required'],
    ['on with a null token', true, null, 'required'],
    ['on with spaces only', true, '   ', 'required'],
    ['on with 23 characters', true, TOKEN.slice(1), 'format'],
    ['on with 25 characters', true, `${TOKEN}a`, 'format'],
    ['on with a non-ASCII letter', true, `${TOKEN.slice(1)}ä`, 'format'],
    ['on with 24 characters', true, TOKEN, null],
    ['on with a padded 24 character token', true, `  ${TOKEN} `, 'format'],
  ] as const)('%s', (_name, enabled, token, expected) => {
    expect(birdweatherTokenProblem(enabled, token)).toBe(expected);
  });
});
