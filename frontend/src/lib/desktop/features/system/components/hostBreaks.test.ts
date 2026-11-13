import { describe, it, expect } from 'vitest';
import { splitHostSegments } from './hostBreaks';

describe('splitHostSegments', () => {
  it('returns no segments for an empty string', () => {
    expect(splitHostSegments('')).toEqual([]);
  });

  it('keeps a string without separators as one segment', () => {
    expect(splitHostSegments('localhost')).toEqual(['localhost']);
  });

  it('breaks a host, port and database after every separator but the last', () => {
    expect(splitHostSegments('mysql.internal.example.org:3306/birdnet_production')).toEqual([
      'mysql.',
      'internal.',
      'example.',
      'org:',
      '3306/',
      'birdnet_production',
    ]);
  });

  it('breaks after hyphens', () => {
    expect(splitHostSegments('db-primary.example.lan:3306/birdnet')).toEqual([
      'db-',
      'primary.',
      'example.',
      'lan:',
      '3306/',
      'birdnet',
    ]);
  });

  it('keeps an IPv4 address with a port and database apart from the port digits', () => {
    expect(splitHostSegments('192.168.4.10:3306/birdnet')).toEqual([
      '192.',
      '168.',
      '4.',
      '10:',
      '3306/',
      'birdnet',
    ]);
  });

  it('keeps a trailing separator on the last segment without an empty one', () => {
    expect(splitHostSegments('example.org:3306/')).toEqual(['example.', 'org:', '3306/']);
  });

  it('keeps consecutive separators as single-character segments', () => {
    expect(splitHostSegments('a..b')).toEqual(['a.', '.', 'b']);
  });

  it('splits an IPv6 literal after each colon and never inside the port', () => {
    expect(splitHostSegments('[::1]:3306/birdnet')).toEqual(['[:', ':', '1]:', '3306/', 'birdnet']);
  });

  it('keeps unicode text intact', () => {
    expect(splitHostSegments('päivä.äö:3306/tietokanta')).toEqual([
      'päivä.',
      'äö:',
      '3306/',
      'tietokanta',
    ]);
  });

  it('keeps a long database name with underscores as one segment', () => {
    const database = 'birdnet_production_archive_2024_q4_detections';
    expect(database).toHaveLength(45);
    expect(splitHostSegments(`db.example.lan:3306/${database}`)).toEqual([
      'db.',
      'example.',
      'lan:',
      '3306/',
      database,
    ]);
  });

  it('keeps an 80 character string without separators as one segment', () => {
    const text = 'a'.repeat(80);
    expect(splitHostSegments(text)).toEqual([text]);
  });
});
