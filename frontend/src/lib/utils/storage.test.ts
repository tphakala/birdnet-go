import { describe, it, expect, beforeEach, vi } from 'vitest';
import { getStoredValue, setStoredValue, removeStoredValue } from './storage';

describe('storage', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  describe('getStoredValue', () => {
    it('returns default when key does not exist', () => {
      expect(getStoredValue('missing', 'fallback')).toBe('fallback');
    });

    it('returns stored string value', () => {
      localStorage.setItem('test-key', JSON.stringify('hello'));
      expect(getStoredValue('test-key', 'default')).toBe('hello');
    });

    it('returns stored object value', () => {
      const obj = { primary: '#fff', accent: '#000' };
      localStorage.setItem('colors', JSON.stringify(obj));
      expect(getStoredValue('colors', { primary: '', accent: '' })).toEqual(obj);
    });

    it('returns stored boolean value', () => {
      localStorage.setItem('collapsed', JSON.stringify(true));
      expect(getStoredValue('collapsed', false)).toBe(true);
    });

    it('returns default on malformed JSON', () => {
      localStorage.setItem('bad', '{not valid json');
      expect(getStoredValue('bad', 'safe')).toBe('safe');
    });

    it('returns default when validate rejects stored value', () => {
      localStorage.setItem('scheme', JSON.stringify('invalid-scheme'));
      const isValidScheme = (v: unknown): v is string =>
        typeof v === 'string' && ['blue', 'forest', 'amber'].includes(v);
      expect(getStoredValue('scheme', 'blue', isValidScheme)).toBe('blue');
    });

    it('returns stored value when validate accepts it', () => {
      localStorage.setItem('scheme', JSON.stringify('forest'));
      const isValidScheme = (v: unknown): v is string =>
        typeof v === 'string' && ['blue', 'forest', 'amber'].includes(v);
      expect(getStoredValue('scheme', 'blue', isValidScheme)).toBe('forest');
    });

    it('returns default when localStorage throws', () => {
      vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
        throw new Error('storage full');
      });
      expect(getStoredValue('key', 42)).toBe(42);
    });
  });

  describe('setStoredValue', () => {
    it('stores string value as JSON', () => {
      setStoredValue('key', 'value');
      expect(localStorage.getItem('key')).toBe(JSON.stringify('value'));
    });

    it('stores object value as JSON', () => {
      const obj = { a: 1 };
      setStoredValue('key', obj);
      expect(localStorage.getItem('key')).toBe(JSON.stringify(obj));
    });

    it('does not throw when localStorage is unavailable', () => {
      vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
        throw new Error('quota exceeded');
      });
      expect(() => setStoredValue('key', 'val')).not.toThrow();
    });
  });

  describe('removeStoredValue', () => {
    it('removes stored key', () => {
      localStorage.setItem('key', 'val');
      removeStoredValue('key');
      expect(localStorage.getItem('key')).toBeNull();
    });

    it('does not throw when localStorage is unavailable', () => {
      vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
        throw new Error('not allowed');
      });
      expect(() => removeStoredValue('key')).not.toThrow();
    });
  });
});
