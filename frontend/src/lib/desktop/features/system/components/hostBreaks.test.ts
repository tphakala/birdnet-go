import { describe, it, expect } from 'vitest';
import { splitHostSegments } from './hostBreaks';

describe('splitHostSegments', () => {
  it('returns no segments for an empty string', () => {
    expect(splitHostSegments('')).toEqual([]);
  });

  it('keeps a host without separators as one segment', () => {
    expect(splitHostSegments('localhost')).toEqual(['localhost']);
  });

  it('ends each segment with its separator so no character is lost', () => {
    expect(splitHostSegments('mysql.internal.example.org:3306')).toEqual([
      'mysql.',
      'internal.',
      'example.',
      'org:',
      '3306',
    ]);
  });

  it('breaks after hyphens', () => {
    expect(splitHostSegments('db-primary.example.lan:3306')).toEqual([
      'db-',
      'primary.',
      'example.',
      'lan:',
      '3306',
    ]);
  });

  it('keeps a trailing separator on the last segment without an empty one', () => {
    expect(splitHostSegments('example.org:')).toEqual(['example.', 'org:']);
  });

  it('keeps consecutive separators as single-character segments', () => {
    expect(splitHostSegments('a..b')).toEqual(['a.', '.', 'b']);
  });

  it('splits an IPv6 literal after each colon and never inside the port', () => {
    expect(splitHostSegments('[::1]:3306')).toEqual(['[:', ':', '1]:', '3306']);
  });

  it('keeps unicode hosts intact', () => {
    expect(splitHostSegments('päivä.äö')).toEqual(['päivä.', 'äö']);
  });

  it('keeps an 80 character host without separators as one segment', () => {
    const host = 'a'.repeat(80);
    expect(splitHostSegments(host)).toEqual([host]);
  });
});
