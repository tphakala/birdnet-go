import { describe, it, expect } from 'vitest';
import { getFriendlyAudioSourceName, getAudioSourceDisplayFallback } from './audioSourceLabel';
import type { AudioSourceConfig, StreamConfig } from '$lib/stores/settings';
import type { SourceInfo } from '$lib/types/detection.types';

const soundCards: AudioSourceConfig[] = [
  { name: 'Front Yard Mic', device: 'hw:CARD=USB,DEV=0', gain: 0, models: ['birdnet'] },
  { name: '', device: 'hw:CARD=Unnamed,DEV=0', gain: 0, models: ['birdnet'] },
];

const rtspStreams: StreamConfig[] = [
  { name: 'Back Garden Cam', url: 'rtsp://cam.local/stream1', enabled: true, type: 'rtsp' },
  { name: '', url: 'rtsp://unnamed.local/stream2', enabled: true, type: 'rtsp' },
];

describe('getFriendlyAudioSourceName', () => {
  it('returns null when the source is null or undefined', () => {
    expect(getFriendlyAudioSourceName(null, soundCards, rtspStreams)).toBeNull();
    expect(getFriendlyAudioSourceName(undefined, soundCards, rtspStreams)).toBeNull();
  });

  it('returns displayName verbatim when it is present and distinct from the id', () => {
    const source: SourceInfo = {
      id: 'hw:CARD=USB,DEV=0',
      displayName: 'Server-Resolved Name',
    };
    expect(getFriendlyAudioSourceName(source, soundCards, rtspStreams)).toBe(
      'Server-Resolved Name'
    );
  });

  it('falls back to sound card name when displayName equals the id and a device matches', () => {
    const source: SourceInfo = {
      id: 'hw:CARD=USB,DEV=0',
      displayName: 'hw:CARD=USB,DEV=0',
    };
    expect(getFriendlyAudioSourceName(source, soundCards, rtspStreams)).toBe('Front Yard Mic');
  });

  it('falls back to RTSP stream name when displayName is empty and id matches a stream url', () => {
    const source: SourceInfo = {
      id: 'rtsp://cam.local/stream1',
      displayName: '',
    };
    expect(getFriendlyAudioSourceName(source, soundCards, rtspStreams)).toBe('Back Garden Cam');
  });

  it('returns the id as last resort when displayName equals id and nothing matches in config', () => {
    const source: SourceInfo = {
      id: 'hw:CARD=Mystery,DEV=0',
      displayName: 'hw:CARD=Mystery,DEV=0',
    };
    expect(getFriendlyAudioSourceName(source, soundCards, rtspStreams)).toBe(
      'hw:CARD=Mystery,DEV=0'
    );
  });

  it('falls back to sound card name when displayName is an empty string but the id matches a device', () => {
    const source: SourceInfo = {
      id: 'hw:CARD=USB,DEV=0',
      displayName: '',
    };
    expect(getFriendlyAudioSourceName(source, soundCards, rtspStreams)).toBe('Front Yard Mic');
  });

  it('returns the id when audioSources and rtspStreams are empty and displayName is missing', () => {
    const source: SourceInfo = { id: 'hw:CARD=USB,DEV=0' };
    expect(getFriendlyAudioSourceName(source, [], [])).toBe('hw:CARD=USB,DEV=0');
  });

  it('returns null when the source has no displayName and no id', () => {
    const source: SourceInfo = { id: '' };
    expect(getFriendlyAudioSourceName(source, [], [])).toBeNull();
  });

  it('ignores sound card entries with an empty name and continues searching', () => {
    const source: SourceInfo = { id: 'hw:CARD=Unnamed,DEV=0', displayName: '' };
    // The only matching device has an empty name, so the id should be returned.
    expect(getFriendlyAudioSourceName(source, soundCards, rtspStreams)).toBe(
      'hw:CARD=Unnamed,DEV=0'
    );
  });

  it('ignores stream entries with an empty name and falls back to the id', () => {
    const source: SourceInfo = { id: 'rtsp://unnamed.local/stream2', displayName: '' };
    expect(getFriendlyAudioSourceName(source, soundCards, rtspStreams)).toBe(
      'rtsp://unnamed.local/stream2'
    );
  });

  it('handles undefined audioSources and rtspStreams arguments', () => {
    const source: SourceInfo = {
      id: 'hw:CARD=USB,DEV=0',
      displayName: 'Front Yard Mic',
    };
    expect(getFriendlyAudioSourceName(source, undefined, undefined)).toBe('Front Yard Mic');
  });
});

describe('getAudioSourceDisplayFallback', () => {
  it('returns the friendly name when resolved from config', () => {
    const source: SourceInfo = {
      id: 'hw:CARD=USB,DEV=0',
      displayName: 'hw:CARD=USB,DEV=0',
    };
    expect(getAudioSourceDisplayFallback(source, soundCards, rtspStreams)).toBe('Front Yard Mic');
  });

  it('returns the raw id when no friendly name is available', () => {
    const source: SourceInfo = {
      id: 'hw:CARD=Mystery,DEV=0',
      displayName: '',
    };
    expect(getAudioSourceDisplayFallback(source, [], [])).toBe('hw:CARD=Mystery,DEV=0');
  });

  it('returns an empty string when source is null', () => {
    expect(getAudioSourceDisplayFallback(null, soundCards, rtspStreams)).toBe('');
  });
});
