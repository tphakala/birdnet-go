/**
 * Unit tests for client-side ultrasonic bat time expansion.
 */

import { describe, it, expect } from 'vitest';
import {
  MIN_BAT_SAMPLE_RATE,
  encodePcm16Wav,
  readFlacStreamInfoSampleRate,
  rewriteWavSampleRate,
  sniffAudioContainer,
  timeExpandAudio,
} from './timeExpandAudio';

// Compact valid 128-sample mono 16-bit 256 kHz FLAC sine clip generated with ffmpeg.
const VALID_FLAC_256KHZ_B64 =
  'ZkxhQ4AAACJAAEAAAACFAACFPoAA8AAAAIDdZZ8JilYEVKQStcDswHGX//huCAB/ZADgTgAADU0OxwMf9LDwUPnhCOPmHTWn8Gy7aPA5Ly2gd37cAI5Zd3LDy3208ElqOszaIcVbTSeWq+nEg5Zd3LDy3208ElqOszaIcVbTSeWq+nEg5Zd3LDy3208ElqOszaIcVbTSeWq+nEg5Zd3LDy3208ElqOszaIcVbTDNOg==';

// Compact valid 48 kHz mono 16-bit FLAC clip (below ultrasonic threshold).
const VALID_FLAC_48KHZ_B64 =
  'ZkxhQ4AAACISABIAAABAAABAC7gA8AAAABhHNYwmuOK8F3OYn6LsBhkJ//hqCAAXQk4AAPIl8iYAAA3bDdoAAPIl5hbwu3hLJ4YAKZ86wHO+tYEND7jQNJ6zOtD7jQNJ6zOtD7jQNIDUdQ==';

function base64ToArrayBuffer(b64: string): ArrayBuffer {
  const bin = globalThis.atob(b64);
  const buf = new ArrayBuffer(bin.length);
  const view = new DataView(buf);
  for (let i = 0; i < bin.length; i++) {
    view.setUint8(i, bin.charCodeAt(i));
  }
  return buf;
}

function writeAscii(view: DataView, offset: number, text: string): void {
  for (let i = 0; i < text.length; i++) {
    view.setUint8(offset + i, text.charCodeAt(i));
  }
}

function buildTestWav(opts: {
  sampleRate: number;
  channels?: number;
  bitsPerSample?: number;
  audioFormat?: number;
  extensibleSubFormatTag?: number;
  extraLeadingChunk?: boolean;
  rootTag?: 'RIFF' | 'RF64';
  sampleCount?: number;
}): ArrayBuffer {
  const channels = opts.channels ?? 1;
  const bitsPerSample = opts.bitsPerSample ?? 16;
  const audioFormat = opts.audioFormat ?? 1;
  const bytesPerSample = bitsPerSample / 8;
  const blockAlign = channels * bytesPerSample;
  const sampleCount = opts.sampleCount ?? 4;
  const dataSize = sampleCount * blockAlign;
  const fmtSize = audioFormat === 0xfffe ? 40 : 16;
  const leadingChunkSize = opts.extraLeadingChunk ? 12 : 0; // 8 hdr + 4 payload
  const totalSize = 12 + leadingChunkSize + 8 + fmtSize + 8 + dataSize;

  const buf = new ArrayBuffer(totalSize);
  const view = new DataView(buf);
  let off = 0;

  writeAscii(view, off, opts.rootTag ?? 'RIFF');
  view.setUint32(off + 4, totalSize - 8, true);
  writeAscii(view, off + 8, 'WAVE');
  off += 12;

  if (opts.extraLeadingChunk) {
    writeAscii(view, off, 'JUNK');
    view.setUint32(off + 4, 4, true);
    view.setUint32(off + 8, 0x12345678, true);
    off += 12;
  }

  writeAscii(view, off, 'fmt ');
  view.setUint32(off + 4, fmtSize, true);
  const fmtOff = off + 8;
  view.setUint16(fmtOff, audioFormat, true);
  view.setUint16(fmtOff + 2, channels, true);
  view.setUint32(fmtOff + 4, opts.sampleRate, true);
  view.setUint32(fmtOff + 8, opts.sampleRate * blockAlign, true);
  view.setUint16(fmtOff + 12, blockAlign, true);
  view.setUint16(fmtOff + 14, bitsPerSample, true);
  if (audioFormat === 0xfffe) {
    view.setUint16(fmtOff + 16, 22, true);
    view.setUint16(fmtOff + 18, bitsPerSample, true);
    view.setUint32(fmtOff + 20, 4, true);
    view.setUint32(fmtOff + 24, opts.extensibleSubFormatTag ?? 1, true);
    view.setUint16(fmtOff + 28, 0x0000, true);
    view.setUint16(fmtOff + 30, 0x0010, true);
    view.setUint32(fmtOff + 32, 0x800000aa, false);
    view.setUint32(fmtOff + 36, 0x00389b71, false);
  }
  off = fmtOff + fmtSize;

  writeAscii(view, off, 'data');
  view.setUint32(off + 4, dataSize, true);
  const dataOff = off + 8;
  for (let i = 0; i < dataSize; i++) {
    view.setUint8(dataOff + i, (i + 1) & 0xff);
  }

  return buf;
}

function readWavHeaderSampleRate(
  buf: ArrayBuffer,
  fmtPayloadOffset = 20
): { sampleRate: number; byteRate: number } {
  const view = new DataView(buf);
  return {
    sampleRate: view.getUint32(fmtPayloadOffset + 4, true),
    byteRate: view.getUint32(fmtPayloadOffset + 8, true),
  };
}

describe('timeExpandAudio', () => {
  it('sniffs WAV (RIFF and RF64), FLAC, and unknown containers', () => {
    expect(sniffAudioContainer(new Uint8Array(buildTestWav({ sampleRate: 256000 })))).toBe('wav');
    expect(
      sniffAudioContainer(new Uint8Array(buildTestWav({ sampleRate: 256000, rootTag: 'RF64' })))
    ).toBe('wav');
    expect(sniffAudioContainer(new Uint8Array(base64ToArrayBuffer(VALID_FLAC_256KHZ_B64)))).toBe(
      'flac'
    );
    expect(sniffAudioContainer(new Uint8Array([0x4f, 0x67, 0x67, 0x53, 0x00]))).toBe('unknown');
  });

  it('rewrites sampleRate and byteRate in place for ultrasonic WAVs across rates and factors', async () => {
    for (const srcRate of [96000, 192000, 256000, 384000]) {
      for (const factor of [5, 10, 16, 20]) {
        const src = buildTestWav({ sampleRate: srcRate, channels: 2, bitsPerSample: 16 });
        const origTail = new Uint8Array(src.slice(44));
        const blob = await timeExpandAudio(src, factor);
        expect(blob.type).toBe('audio/wav');

        const out = await blob.arrayBuffer();
        const wantRate = Math.round(srcRate / factor);
        expect(readWavHeaderSampleRate(out)).toEqual({
          sampleRate: wantRate,
          byteRate: wantRate * 4,
        });
        expect(new Uint8Array(out.slice(44))).toEqual(origTail);
      }
    }
  });

  it('handles WAVE_FORMAT_EXTENSIBLE and preceding non-fmt chunks in WAV', async () => {
    const src = buildTestWav({
      sampleRate: 256000,
      bitsPerSample: 24,
      audioFormat: 0xfffe,
      extensibleSubFormatTag: 1,
      extraLeadingChunk: true,
    });
    const blob = rewriteWavSampleRate(src, 10);
    const out = await blob.arrayBuffer();
    // 12 RIFF + 12 JUNK + 8 fmt hdr = offset 32 for fmt payload
    expect(readWavHeaderSampleRate(out, 32)).toEqual({
      sampleRate: 25600,
      byteRate: 25600 * 3,
    });
  });

  it('rejects WAV files below MIN_BAT_SAMPLE_RATE and unsupported WAV tags', () => {
    const birdWav = buildTestWav({ sampleRate: 48000 });
    expect(() => rewriteWavSampleRate(birdWav, 10)).toThrow(`at least ${MIN_BAT_SAMPLE_RATE} Hz`);

    const adpcmWav = buildTestWav({ sampleRate: 256000, audioFormat: 2 });
    expect(() => rewriteWavSampleRate(adpcmWav, 10)).toThrow('Unsupported WAV audio format');
  });

  it('reads FLAC STREAMINFO sample rate from header bytes', () => {
    const flac256 = new Uint8Array(base64ToArrayBuffer(VALID_FLAC_256KHZ_B64));
    const flac48 = new Uint8Array(base64ToArrayBuffer(VALID_FLAC_48KHZ_B64));
    expect(readFlacStreamInfoSampleRate(flac256)).toBe(256000);
    expect(readFlacStreamInfoSampleRate(flac48)).toBe(48000);
    expect(readFlacStreamInfoSampleRate(new Uint8Array([1, 2, 3]))).toBeNull();
  });

  it('decodes ultrasonic FLAC at native sample rate and builds time-expanded 16-bit WAV', async () => {
    const src = base64ToArrayBuffer(VALID_FLAC_256KHZ_B64);
    const blob = await timeExpandAudio(src, 10);
    expect(blob.type).toBe('audio/wav');

    const out = await blob.arrayBuffer();
    const view = new DataView(out);
    expect(readWavHeaderSampleRate(out)).toEqual({
      sampleRate: 25600,
      byteRate: 25600 * 2,
    });
    // 128 decoded samples * 2 bytes/sample = 256 PCM bytes
    expect(view.getUint32(40, true)).toBe(256);
    expect(out.byteLength).toBe(44 + 256);
  });

  it('rejects FLAC below MIN_BAT_SAMPLE_RATE and non-WAV/FLAC formats', async () => {
    const flac48 = base64ToArrayBuffer(VALID_FLAC_48KHZ_B64);
    await expect(timeExpandAudio(flac48, 10)).rejects.toThrow(`at least ${MIN_BAT_SAMPLE_RATE} Hz`);

    const ogg = new Uint8Array([0x4f, 0x67, 0x67, 0x53, 0x00, 0x02]).buffer;
    await expect(timeExpandAudio(ogg, 10)).rejects.toThrow('format is not supported');
  });

  it('encodes multi-channel PCM16 WAV with proper clamping', async () => {
    const ch0 = new Float32Array([-1.5, 0, 1.5]);
    const ch1 = new Float32Array([0.5, -0.5, 0]);
    const blob = encodePcm16Wav([ch0, ch1], 19200);
    const out = await blob.arrayBuffer();
    const view = new DataView(out);
    expect(view.getUint16(22, true)).toBe(2);
    expect(view.getUint32(24, true)).toBe(19200);
    expect(view.getUint32(40, true)).toBe(12);
    expect(view.getInt16(44, true)).toBe(-32768);
    expect(view.getInt16(46, true)).toBe(16384);
    expect(view.getInt16(52, true)).toBe(32767);
  });

  it('aborts cleanly when signal is already aborted', async () => {
    const controller = new AbortController();
    controller.abort();
    const src = buildTestWav({ sampleRate: 256000 });
    await expect(timeExpandAudio(src, 10, controller.signal)).rejects.toMatchObject({
      name: 'AbortError',
    });
  });
});
