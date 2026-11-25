import { describe, it, expect } from 'vitest';
import { splitLocationSegments } from './locationBreaks';

describe('splitLocationSegments', () => {
  it('returns no segments for an empty string', () => {
    expect(splitLocationSegments('')).toEqual([]);
  });

  it('keeps a string without separators as one segment', () => {
    expect(splitLocationSegments('localhost')).toEqual(['localhost']);
  });

  it('breaks a host, port and database after every separator but the last', () => {
    expect(splitLocationSegments('mysql.internal.example.org:3306/birdnet_production')).toEqual([
      'mysql.',
      'internal.',
      'example.',
      'org:',
      '3306/',
      'birdnet_production',
    ]);
  });

  it('breaks after hyphens', () => {
    expect(splitLocationSegments('db-primary.example.lan:3306/birdnet')).toEqual([
      'db-',
      'primary.',
      'example.',
      'lan:',
      '3306/',
      'birdnet',
    ]);
  });

  it('keeps an IPv4 address with a port and database apart from the port digits', () => {
    expect(splitLocationSegments('192.168.4.10:3306/birdnet')).toEqual([
      '192.',
      '168.',
      '4.',
      '10:',
      '3306/',
      'birdnet',
    ]);
  });

  it('keeps a trailing separator on the last segment without an empty one', () => {
    expect(splitLocationSegments('example.org:3306/')).toEqual(['example.', 'org:', '3306/']);
  });

  it('keeps consecutive separators as single-character segments', () => {
    expect(splitLocationSegments('a..b')).toEqual(['a.', '.', 'b']);
  });

  it('splits an IPv6 literal after each colon and never inside the port', () => {
    expect(splitLocationSegments('[::1]:3306/birdnet')).toEqual([
      '[:',
      ':',
      '1]:',
      '3306/',
      'birdnet',
    ]);
  });

  it('keeps unicode text intact', () => {
    expect(splitLocationSegments('päivä.äö:3306/tietokanta')).toEqual([
      'päivä.',
      'äö:',
      '3306/',
      'tietokanta',
    ]);
  });

  it('keeps a long database name with underscores as one segment', () => {
    const database = 'birdnet_production_archive_2024_q4_hours';
    expect(splitLocationSegments(`db.example.lan:3306/${database}`)).toEqual([
      'db.',
      'example.',
      'lan:',
      '3306/',
      database,
    ]);
  });

  it('keeps an 80 character string without separators as one segment', () => {
    const text = 'a'.repeat(80);
    expect(splitLocationSegments(text)).toEqual([text]);
  });
});
