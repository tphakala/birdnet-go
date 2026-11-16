/**
 * Stream option helpers shared by the stream card and the add form: the type a
 * URL names, whether a URL fits a type, which types take a transport, and the
 * type a select value names.
 */
import { describe, it, expect } from 'vitest';
import {
  detectStreamType,
  isValidStreamUrl,
  streamHasTransport,
  toStreamType,
} from './streamOptions';

describe('detectStreamType', () => {
  it('reads the type from the scheme or the playlist extension, and falls back to RTSP', () => {
    expect(detectStreamType('rtsp://cam/live')).toBe('rtsp');
    expect(detectStreamType('RTSPS://cam/live')).toBe('rtsp');
    expect(detectStreamType('rtmp://cam/live')).toBe('rtmp');
    expect(detectStreamType('udp://239.0.0.1:1234')).toBe('udp');
    expect(detectStreamType('https://cam/live/index.m3u8')).toBe('hls');
    expect(detectStreamType('http://cam/live.mp3')).toBe('http');
    expect(detectStreamType('cam/live')).toBe('rtsp');
  });
});

describe('isValidStreamUrl', () => {
  it('accepts a URL whose scheme belongs to the type and refuses one that does not', () => {
    expect(isValidStreamUrl('rtsp://cam/live', 'rtsp')).toBe(true);
    expect(isValidStreamUrl('rtsp://cam/live', 'http')).toBe(false);
    expect(isValidStreamUrl('https://cam/live.m3u8', 'hls')).toBe(true);
    expect(isValidStreamUrl('not a url', 'rtsp')).toBe(false);
  });
});

describe('streamHasTransport and toStreamType', () => {
  it('gives only RTSP and RTMP a transport', () => {
    expect(streamHasTransport('rtsp')).toBe(true);
    expect(streamHasTransport('rtmp')).toBe(true);
    expect(streamHasTransport('http')).toBe(false);
    expect(streamHasTransport('hls')).toBe(false);
    expect(streamHasTransport('udp')).toBe(false);
  });

  it('names a known type and nothing for an unknown value', () => {
    expect(toStreamType('hls')).toBe('hls');
    expect(toStreamType('ftp')).toBeUndefined();
    expect(toStreamType(['hls'])).toBeUndefined();
  });
});
