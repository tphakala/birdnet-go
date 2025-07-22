import { describe, it, expect } from 'vitest';
import { cn } from './cn';

describe('cn', () => {
  it('joins strings with spaces', () => {
    expect(cn('foo', 'bar')).toBe('foo bar');
    expect(cn('foo', 'bar', 'baz')).toBe('foo bar baz');
  });

  it('handles null and undefined values', () => {
    expect(cn('foo', null, 'bar')).toBe('foo bar');
    expect(cn('foo', undefined, 'bar')).toBe('foo bar');
    expect(cn(null, undefined)).toBe('');
  });

  it('handles boolean values', () => {
    expect(cn('foo', false, 'bar')).toBe('foo bar');
    expect(cn('foo', true, 'bar')).toBe('foo bar');
    expect(cn(false)).toBe('');
  });

  it('handles object with boolean values', () => {
    expect(cn({ foo: true, bar: false })).toBe('foo');
    expect(cn({ foo: true, bar: true })).toBe('foo bar');
    expect(cn({ foo: false, bar: false })).toBe('');
  });

  it('handles arrays', () => {
    expect(cn(['foo', 'bar'])).toBe('foo bar');
    expect(cn(['foo', null, 'bar'])).toBe('foo bar');
    expect(cn(['foo', ['bar', 'baz']])).toBe('foo bar baz');
  });

  it('handles mixed types', () => {
    expect(cn('foo', { bar: true, baz: false }, ['qux', 'quux'])).toBe('foo bar qux quux');
  });

  it('handles numbers', () => {
    expect(cn('foo', 123, 'bar')).toBe('foo 123 bar');
    expect(cn(0)).toBe('0');
  });

  it('handles empty inputs', () => {
    expect(cn()).toBe('');
    expect(cn('')).toBe(''); // empty strings are filtered out
    expect(cn([])).toBe('');
    expect(cn({})).toBe('');
  });

  it('handles complex nested structures', () => {
    expect(cn('foo', ['bar', { baz: true, qux: false }, ['quux', null, { corge: true }]])).toBe(
      'foo bar baz quux corge'
    );
  });

  it('filters out falsy values from arrays', () => {
    expect(cn(['foo', 0, false, '', null, undefined, 'bar'])).toBe('foo 0 bar');
  });

  it('handles object with null and undefined values', () => {
    expect(cn({ foo: true, bar: null, baz: undefined, qux: false })).toBe('foo');
  });
});
