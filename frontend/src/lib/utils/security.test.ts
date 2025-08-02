import { describe, it, expect } from 'vitest';
import {
  safeGet,
  safeLookup,
  createSafeMap,
  safeArrayAccess,
  validateInput,
  safeRegexTest,
  validateCIDR,
  validateProtocolURL,
  sanitizeFilename,
  createEnumLookup,
  safeSwitch,
} from './security';

describe('Security Utilities', () => {
  describe('safeGet', () => {
    it('should safely access existing properties', () => {
      const obj = { foo: 'bar', baz: 42 };
      expect(safeGet(obj, 'foo')).toBe('bar');
      expect(safeGet(obj, 'baz')).toBe(42);
    });

    it('should return undefined for non-existent properties', () => {
      const obj = { foo: 'bar' };
      expect(safeGet(obj, 'nonexistent')).toBeUndefined();
    });

    it('should return default value for non-existent properties', () => {
      const obj = { foo: 'bar' };
      expect(safeGet(obj, 'nonexistent', 'default')).toBe('default');
    });

    it('should not access prototype properties', () => {
      const obj = Object.create({ proto: 'dangerous' });
      obj.own = 'safe';
      expect(safeGet(obj, 'own')).toBe('safe');
      expect(safeGet(obj, 'proto')).toBeUndefined();
    });
  });

  describe('safeLookup', () => {
    const lookup = { foo: 'bar', baz: 'qux' };
    const allowed = ['foo', 'baz'];

    it('should return value for allowed keys', () => {
      expect(safeLookup(lookup, 'foo', allowed)).toBe('bar');
    });

    it('should return undefined for disallowed keys', () => {
      expect(safeLookup(lookup, 'notallowed', allowed)).toBeUndefined();
    });

    it('should return default for disallowed keys', () => {
      expect(safeLookup(lookup, 'notallowed', allowed, 'default')).toBe('default');
    });
  });

  describe('createSafeMap', () => {
    it('should create a Map from an object', () => {
      const obj = { foo: 'bar', baz: 'qux' };
      const map = createSafeMap(obj);
      expect(map).toBeInstanceOf(Map);
      expect(map.get('foo')).toBe('bar');
      expect(map.get('baz')).toBe('qux');
    });

    it('should not include prototype properties', () => {
      const obj = Object.create({ proto: 'dangerous' });
      obj.own = 'safe';
      const map = createSafeMap(obj);
      expect(map.get('own')).toBe('safe');
      expect(map.get('proto')).toBeUndefined();
    });
  });

  describe('safeArrayAccess', () => {
    const arr = ['a', 'b', 'c'];

    it('should access valid indices', () => {
      expect(safeArrayAccess(arr, 0)).toBe('a');
      expect(safeArrayAccess(arr, 2)).toBe('c');
    });

    it('should return undefined for invalid indices', () => {
      expect(safeArrayAccess(arr, -1)).toBeUndefined();
      expect(safeArrayAccess(arr, 3)).toBeUndefined();
    });

    it('should return default for invalid indices', () => {
      expect(safeArrayAccess(arr, 10, 'default')).toBe('default');
    });
  });

  describe('validateInput', () => {
    it('should validate input length', () => {
      expect(validateInput('hello', 10)).toBe(true);
      expect(validateInput('hello', 3)).toBe(false);
    });

    it('should validate with pattern', () => {
      const pattern = /^[a-z]+$/;
      expect(validateInput('hello', 10, pattern)).toBe(true);
      expect(validateInput('hello123', 10, pattern)).toBe(false);
    });

    it('should reject empty input', () => {
      expect(validateInput('', 10)).toBe(false);
    });
  });

  describe('safeRegexTest', () => {
    it('should test regex safely', () => {
      const pattern = /^test$/;
      expect(safeRegexTest(pattern, 'test')).toBe(true);
      expect(safeRegexTest(pattern, 'notest')).toBe(false);
    });

    it('should reject overly long input', () => {
      const pattern = /./;
      const longString = 'a'.repeat(2000);
      expect(safeRegexTest(pattern, longString)).toBe(false);
    });
  });

  describe('validateCIDR', () => {
    it('should validate correct CIDR notation', () => {
      expect(validateCIDR('192.168.1.0/24')).toBe(true);
      expect(validateCIDR('10.0.0.0/8')).toBe(true);
      expect(validateCIDR('172.16.0.0/12')).toBe(true);
    });

    it('should reject invalid CIDR notation', () => {
      expect(validateCIDR('192.168.1.0')).toBe(false); // No mask
      expect(validateCIDR('192.168.1.0/33')).toBe(false); // Invalid mask
      expect(validateCIDR('256.168.1.0/24')).toBe(false); // Invalid octet
      expect(validateCIDR('192.168.1/24')).toBe(false); // Missing octet
      expect(validateCIDR('')).toBe(false);
    });

    it('should reject overly long input', () => {
      expect(validateCIDR('192.168.1.0/24' + 'x'.repeat(100))).toBe(false);
    });
  });

  describe('validateProtocolURL', () => {
    it('should validate URLs with allowed protocols', () => {
      expect(validateProtocolURL('http://example.com', ['http', 'https'])).toBe(true);
      expect(validateProtocolURL('https://example.com', ['http', 'https'])).toBe(true);
      expect(validateProtocolURL('rtsp://192.168.1.1:554/stream', ['rtsp'])).toBe(true);
    });

    it('should reject URLs with disallowed protocols', () => {
      expect(validateProtocolURL('ftp://example.com', ['http', 'https'])).toBe(false);
      expect(validateProtocolURL('file:///etc/passwd', ['http', 'https'])).toBe(false);
    });

    it('should reject invalid URLs', () => {
      expect(validateProtocolURL('not a url', ['http'])).toBe(false);
      expect(validateProtocolURL('', ['http'])).toBe(false);
    });

    it('should reject overly long URLs', () => {
      const longUrl = 'http://example.com/' + 'x'.repeat(3000);
      expect(validateProtocolURL(longUrl, ['http'])).toBe(false);
    });
  });

  describe('sanitizeFilename', () => {
    it('should sanitize filenames', () => {
      expect(sanitizeFilename('normal.txt')).toBe('normal.txt');
      expect(sanitizeFilename('file-name_123.png')).toBe('file-name_123.png');
    });

    it('should remove path components', () => {
      expect(sanitizeFilename('/etc/passwd')).toBe('passwd');
      expect(sanitizeFilename('../../secret.txt')).toBe('secret.txt');
      expect(sanitizeFilename('C:\\Windows\\System32\\config.sys')).toBe('config.sys');
    });

    it('should replace unsafe characters', () => {
      expect(sanitizeFilename('file<>:|?*.txt')).toBe('file______.txt');
      expect(sanitizeFilename('file name.txt')).toBe('file_name.txt');
    });
  });

  describe('createEnumLookup', () => {
    it('should create a lookup function', () => {
      const enumObj = { FOO: 'bar', BAZ: 'qux' };
      const lookup = createEnumLookup(enumObj);

      expect(lookup('FOO')).toBe('bar');
      expect(lookup('BAZ')).toBe('qux');
      expect(lookup('INVALID')).toBeUndefined();
    });
  });

  describe('safeSwitch', () => {
    const cases = {
      small: 'sm',
      medium: 'md',
      large: 'lg',
    };

    it('should return value for known keys', () => {
      expect(safeSwitch('small', cases, 'default')).toBe('sm');
      expect(safeSwitch('large', cases, 'default')).toBe('lg');
    });

    it('should return default for unknown keys', () => {
      expect(safeSwitch('unknown', cases, 'default')).toBe('default');
    });
  });
});
