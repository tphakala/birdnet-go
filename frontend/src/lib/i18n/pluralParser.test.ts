/**
 * Tests for ICU MessageFormat plural parsing
 * Issue #1682: Search result UI has placeholder text instead of real values
 */

import { describe, it, expect } from 'vitest';
import { parsePlural } from './pluralParser';

describe('pluralParser', () => {
  describe('parsePlural', () => {
    it('handles simple plural with count=0', () => {
      const message = '{count, plural, =0 {No results} one {# result} other {# results}}';
      const result = parsePlural(message, { count: 0 }, 'en');
      expect(result).toBe('No results');
    });

    it('handles simple plural with count=1', () => {
      const message = '{count, plural, =0 {No results} one {# result} other {# results}}';
      const result = parsePlural(message, { count: 1 }, 'en');
      expect(result).toBe('1 result');
    });

    it('handles simple plural with count > 1', () => {
      const message = '{count, plural, =0 {No results} one {# result} other {# results}}';
      const result = parsePlural(message, { count: 5 }, 'en');
      expect(result).toBe('5 results');
    });

    it('handles plural with text before and after', () => {
      const message = 'Found {count, plural, =0 {no items} one {# item} other {# items}} in search';
      const result = parsePlural(message, { count: 3 }, 'en');
      expect(result).toBe('Found 3 items in search');
    });

    it('returns original message when param is not a number', () => {
      const message = '{count, plural, =0 {No results} one {# result} other {# results}}';
      const result = parsePlural(message, { count: 'five' }, 'en');
      // Should return original since count is not a number
      expect(result).toBe(message);
    });

    it('handles missing params gracefully', () => {
      const message = '{count, plural, =0 {No results} one {# result} other {# results}}';
      const result = parsePlural(message, {}, 'en');
      expect(result).toBe(message);
    });

    it('handles messages without plural syntax', () => {
      const message = 'Hello, world!';
      const result = parsePlural(message, { count: 5 }, 'en');
      expect(result).toBe('Hello, world!');
    });

    it('handles nested braces in plural text correctly', () => {
      // This is the key test for issue #1682
      // The plural cases contain braces: {No results}, {# result}, {# results}
      const message = '{count, plural, =0 {No results} one {# result} other {# results}}';
      const result = parsePlural(message, { count: 42 }, 'en');
      // Should NOT return the raw template
      expect(result).not.toContain('{count, plural');
      expect(result).toBe('42 results');
    });
  });
});
