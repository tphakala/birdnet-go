/**
 * Unit tests for client-side ultrasonic bat time expansion.
 */

import { describe, it, expect, vi, afterEach } from 'vitest';
import {
  MAX_DECODED_PCM_BYTES,
  MIN_BAT_SAMPLE_RATE,
  TimeExpandAudioError,
  encodePcm16Wav,
  readFlacStreamInfo,
  readFlacStreamInfoSampleRate,
  rewriteWavSampleRate,
  sniffAudioContainer,
  timeExpandAudio,
} from './timeExpandAudio';

// Compact valid 128-sample mono 16-bit 256 kHz FLAC sine clip generated with ffmpeg.
const VALID_FLAC_256KHZ_B64 =
  'ZkxhQ4AAACJAAEAAAACFAACFPoAA8AAAAIDdZZ8JilYEVKQStcDswHGX//huCAB/ZADgTgAADU0OxwMf9LDwUPnhCOPmHTWn8Gy7aPA5Ly2gd37cAI5Zd3LDy3208ElqOszaIcVbTSeWq+nEg5Zd3LDy3208ElqOszaIcVbTSeWq+nEg5Zd3LDy3208ElqOszaIcVbTSeWq+nEg5Zd3LDy3208ElqOszaIcVbTDNOg==';

// Compact valid 128-sample mono 24-bit 256 kHz FLAC sine clip generated with ffmpeg.
const VALID_FLAC_256KHZ_24BIT_B64 =
  'ZkxhQ4AAACJAAEAAAACGAACGPoABcAAAAIC4ebyn+xB5BCypfAfqUm0s//huDAB/ZABvTwEAAA1NDscDH/Sw8FD54Qjj5h01p/Bsu2jwOS8toHd+3ACOWXdyw8t9tPBJajrM2iHFW00nlqvpxIOWXdyw8t9tPBJajrM2iHFW00nlqvpxIOWXdyw8t9tPBJajrM2iHFW00nlqvpxIOWXdyw8t9tPBJajrM2iHFW0wid4=';

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
  extensibleCbSize?: number;
  invalidGuid?: boolean;
  extraLeadingChunkPayloadBytes?: number;
  omitDataChunk?: boolean;
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
  const extraPayload = opts.extraLeadingChunkPayloadBytes ?? 0;
  const extraPad = extraPayload & 1;
  const leadingChunkSize = extraPayload > 0 ? 8 + extraPayload + extraPad : 0;
  const dataChunkSize = opts.omitDataChunk ? 0 : 8 + dataSize;
  const totalSize = 12 + leadingChunkSize + 8 + fmtSize + dataChunkSize;

  const buf = new ArrayBuffer(totalSize);
  const view = new DataView(buf);
  let off = 0;

  writeAscii(view, off, opts.rootTag ?? 'RIFF');
  view.setUint32(off + 4, totalSize - 8, true);
  writeAscii(view, off + 8, 'WAVE');
  off += 12;

  if (extraPayload > 0) {
    writeAscii(view, off, 'JUNK');
    view.setUint32(off + 4, extraPayload, true);
    for (let i = 0; i < extraPayload; i++) {
      view.setUint8(off + 8 + i, 0xaa);
    }
    off += leadingChunkSize;
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
    view.setUint16(fmtOff + 16, opts.extensibleCbSize ?? 22, true);
    view.setUint16(fmtOff + 18, bitsPerSample, true);
    view.setUint32(fmtOff + 20, 4, true);
    view.setUint32(fmtOff + 24, opts.extensibleSubFormatTag ?? 1, true);
    view.setUint16(fmtOff + 28, 0x0000, true);
    view.setUint16(fmtOff + 30, 0x0010, true);
    view.setUint32(fmtOff + 32, opts.invalidGuid ? 0xdeadbeef : 0x800000aa, false);
    view.setUint32(fmtOff + 36, 0x00389b71, false);
  }
  off = fmtOff + fmtSize;

  if (!opts.omitDataChunk) {
    writeAscii(view, off, 'data');
    view.setUint32(off + 4, dataSize, true);
    const dataOff = off + 8;
    for (let i = 0; i < dataSize; i++) {
      view.setUint8(dataOff + i, (i + 1) & 0xff);
    }
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
  afterEach(() => {
    vi.doUnmock('@wasm-audio-decoders/flac');
    vi.resetModules();
  });

  it('sniffs RIFF WAV and FLAC while rejecting RF64 and unknown containers', () => {
    expect(sniffAudioContainer(new Uint8Array(buildTestWav({ sampleRate: 256000 })))).toBe('wav');
    expect(
      sniffAudioContainer(new Uint8Array(buildTestWav({ sampleRate: 256000, rootTag: 'RF64' })))
    ).toBe('unknown');
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

  it('supports IEEE float WAV, odd-sized preceding chunk word alignment, and WAVE_FORMAT_EXTENSIBLE', async () => {
    const floatWav = buildTestWav({ sampleRate: 192000, bitsPerSample: 32, audioFormat: 3 });
    const floatOut = await rewriteWavSampleRate(floatWav, 10).arrayBuffer();
    expect(readWavHeaderSampleRate(floatOut)).toEqual({
      sampleRate: 19200,
      byteRate: 19200 * 4,
    });

    // Odd 3-byte JUNK chunk payload is padded to 4 bytes (total leading chunk = 12 bytes).
    const extWav = buildTestWav({
      sampleRate: 256000,
      bitsPerSample: 24,
      audioFormat: 0xfffe,
      extensibleSubFormatTag: 1,
      extraLeadingChunkPayloadBytes: 3,
    });
    const extOut = await rewriteWavSampleRate(extWav, 10).arrayBuffer();
    expect(readWavHeaderSampleRate(extOut, 32)).toEqual({
      sampleRate: 25600,
      byteRate: 25600 * 3,
    });
  });

  it('rejects invalid WAV containers, RF64, bad EXTENSIBLE headers, missing data chunks, and low rates', () => {
    const birdWav = buildTestWav({ sampleRate: 48000 });
    expect(() => rewriteWavSampleRate(birdWav, 10)).toThrow(`at least ${MIN_BAT_SAMPLE_RATE} Hz`);

    const adpcmWav = buildTestWav({ sampleRate: 256000, audioFormat: 2 });
    expect(() => rewriteWavSampleRate(adpcmWav, 10)).toThrow('Unsupported WAV audio format');

    const badGuidWav = buildTestWav({
      sampleRate: 256000,
      bitsPerSample: 24,
      audioFormat: 0xfffe,
      invalidGuid: true,
    });
    expect(() => rewriteWavSampleRate(badGuidWav, 10)).toThrow('Unsupported WAV audio format');

    const smallCbWav = buildTestWav({
      sampleRate: 256000,
      bitsPerSample: 24,
      audioFormat: 0xfffe,
      extensibleCbSize: 18,
    });
    expect(() => rewriteWavSampleRate(smallCbWav, 10)).toThrow('Unsupported WAV audio format');

    const noDataWav = buildTestWav({ sampleRate: 256000, omitDataChunk: true });
    expect(() => rewriteWavSampleRate(noDataWav, 10)).toThrow('missing a valid fmt or data chunk');

    const rf64Wav = buildTestWav({ sampleRate: 256000, rootTag: 'RF64' });
    expect(() => rewriteWavSampleRate(rf64Wav, 10)).toThrow('RF64 WAV containers');
  });

  it('reads FLAC STREAMINFO metadata including rate, channels, bitDepth, and totalSamples', () => {
    const flac256 = new Uint8Array(base64ToArrayBuffer(VALID_FLAC_256KHZ_B64));
    const flac24 = new Uint8Array(base64ToArrayBuffer(VALID_FLAC_256KHZ_24BIT_B64));
    const flac48 = new Uint8Array(base64ToArrayBuffer(VALID_FLAC_48KHZ_B64));

    expect(readFlacStreamInfo(flac256)).toEqual({
      sampleRate: 256000,
      channels: 1,
      bitDepth: 16,
      totalSamples: 128,
    });
    expect(readFlacStreamInfo(flac24)).toEqual({
      sampleRate: 256000,
      channels: 1,
      bitDepth: 24,
      totalSamples: 128,
    });
    expect(readFlacStreamInfoSampleRate(flac48)).toBe(48000);
    expect(readFlacStreamInfoSampleRate(new Uint8Array([1, 2, 3]))).toBeNull();
  });

  it('decodes 16-bit and 24-bit ultrasonic FLAC at native rate into time-expanded 16-bit WAV', async () => {
    for (const b64 of [VALID_FLAC_256KHZ_B64, VALID_FLAC_256KHZ_24BIT_B64]) {
      const src = base64ToArrayBuffer(b64);
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
    }
  });

  it('rejects FLAC exceeding MAX_DECODED_PCM_BYTES before WASM decode', async () => {
    const oversized = new Uint8Array(base64ToArrayBuffer(VALID_FLAC_256KHZ_B64));
    const oversizeSamples = Math.floor(MAX_DECODED_PCM_BYTES / 2) + 1024;
    // Encode 36-bit totalSamples into STREAMINFO bytes 21..25 without touching sampleRate/channels.
    oversized[21] = (oversized[21] & 0xf0) | ((oversizeSamples / 0x1_0000_0000) & 0x0f);
    const low32 = oversizeSamples >>> 0;
    oversized[22] = (low32 >>> 24) & 0xff;
    oversized[23] = (low32 >>> 16) & 0xff;
    oversized[24] = (low32 >>> 8) & 0xff;
    oversized[25] = low32 & 0xff;

    await expect(timeExpandAudio(oversized.buffer, 10)).rejects.toMatchObject({
      code: 'pcm_too_large',
    });
  });

  it('rejects FLAC below MIN_BAT_SAMPLE_RATE and non-WAV/FLAC formats', async () => {
    const flac48 = base64ToArrayBuffer(VALID_FLAC_48KHZ_B64);
    await expect(timeExpandAudio(flac48, 10)).rejects.toBeInstanceOf(TimeExpandAudioError);

    const ogg = new Uint8Array([0x4f, 0x67, 0x67, 0x53, 0x00, 0x02]).buffer;
    await expect(timeExpandAudio(ogg, 10)).rejects.toMatchObject({
      code: 'unsupported_format',
    });
  });

  it('does not mask WASM init failures when decoder.ready rejects before decoder.free', async () => {
    const initErr = new Error('CompileError: WebAssembly.compile blocked by CSP');
    const freeSpy = vi.fn(() => {
      throw new TypeError("Cannot read properties of undefined (reading 'wasm')");
    });
    vi.doMock('@wasm-audio-decoders/flac', () => ({
      FLACDecoder: class {
        ready = Promise.reject(initErr);
        free = freeSpy;
      },
    }));

    const { timeExpandAudio: expandWithMockedFlac } = await import('./timeExpandAudio');
    const flac256 = base64ToArrayBuffer(VALID_FLAC_256KHZ_B64);

    await expect(expandWithMockedFlac(flac256, 10)).rejects.toBe(initErr);
    expect(freeSpy).not.toHaveBeenCalled();
  });

  it('encodes multi-channel PCM16 WAV with symmetric clamping', async () => {
    const ch0 = new Float32Array([-1.5, 0, 1.0, 1.5]);
    const ch1 = new Float32Array([0.5, -0.5, 0, -1.0]);
    const blob = encodePcm16Wav([ch0, ch1], 19200);
    const out = await blob.arrayBuffer();
    const view = new DataView(out);
    expect(view.getUint16(22, true)).toBe(2);
    expect(view.getUint32(24, true)).toBe(19200);
    expect(view.getUint32(40, true)).toBe(16);
    expect(view.getInt16(44, true)).toBe(-32768);
    expect(view.getInt16(46, true)).toBe(16384);
    expect(view.getInt16(52, true)).toBe(32767);
    expect(view.getInt16(58, true)).toBe(-32768);
  });

  it('falls back to streaming chunk merge when STREAMINFO totalSamples is 0', async () => {
    const standardBlob = await timeExpandAudio(base64ToArrayBuffer(VALID_FLAC_256KHZ_B64), 10);
    const standardOut = new Uint8Array(await standardBlob.arrayBuffer());

    const zeroTotal = new Uint8Array(base64ToArrayBuffer(VALID_FLAC_256KHZ_B64));
    // Zero 36-bit totalSamples in STREAMINFO (lower nibble of byte 21 + bytes 22..25).
    zeroTotal[21] &= 0xf0;
    zeroTotal[22] = 0;
    zeroTotal[23] = 0;
    zeroTotal[24] = 0;
    zeroTotal[25] = 0;

    const fallbackBlob = await timeExpandAudio(zeroTotal.buffer, 10);
    const fallbackOut = new Uint8Array(await fallbackBlob.arrayBuffer());
    expect(fallbackOut).toEqual(standardOut);
  });

  it('rejects FLAC whose decoded frames exceed STREAMINFO capacity or channel count', async () => {
    const src = base64ToArrayBuffer(VALID_FLAC_256KHZ_B64);

    vi.doMock('@wasm-audio-decoders/flac', () => ({
      FLACDecoder: class {
        ready = Promise.resolve();
        decode = vi.fn(async () => ({
          // STREAMINFO declares 128 frames; returning 256 must fail runtime capacity guard.
          channelData: [new Float32Array(256)],
          samplesDecoded: 256,
          sampleRate: 256000,
          bitDepth: 16,
          errors: [],
        }));
        flush = vi.fn();
        free = vi.fn();
      },
    }));
    const { timeExpandAudio: expandOverflow } = await import('./timeExpandAudio');
    await expect(expandOverflow(src, 10)).rejects.toMatchObject({
      code: 'pcm_too_large',
    });

    vi.resetModules();
    vi.doMock('@wasm-audio-decoders/flac', () => ({
      FLACDecoder: class {
        ready = Promise.resolve();
        decode = vi.fn(async () => ({
          // STREAMINFO declares 1 channel; returning 2 must fail channel validation.
          channelData: [new Float32Array(128), new Float32Array(128)],
          samplesDecoded: 128,
          sampleRate: 256000,
          bitDepth: 16,
          errors: [],
        }));
        flush = vi.fn();
        free = vi.fn();
      },
    }));
    const { timeExpandAudio: expandMismatch } = await import('./timeExpandAudio');
    await expect(expandMismatch(src, 10)).rejects.toMatchObject({
      code: 'flac_decode_failed',
    });
  });

  it('aborts cleanly before and during FLAC decoding', async () => {
    const controller = new AbortController();
    controller.abort();
    const src = base64ToArrayBuffer(VALID_FLAC_256KHZ_B64);
    await expect(timeExpandAudio(src, 10, controller.signal)).rejects.toMatchObject({
      name: 'AbortError',
    });

    const midController = new AbortController();
    const freeSpy = vi.fn();
    vi.doMock('@wasm-audio-decoders/flac', () => ({
      FLACDecoder: class {
        ready = Promise.resolve();
        decode = vi.fn(async () => {
          midController.abort();
          return {
            channelData: [new Float32Array([0.1, -0.1])],
            samplesDecoded: 2,
            sampleRate: 256000,
            bitDepth: 16,
            errors: [],
          };
        });
        flush = vi.fn();
        free = freeSpy;
      },
    }));

    const { timeExpandAudio: expandMidAbort } = await import('./timeExpandAudio');
    await expect(expandMidAbort(src, 10, midController.signal)).rejects.toMatchObject({
      name: 'AbortError',
    });
    expect(freeSpy).toHaveBeenCalledTimes(1);
  });
});
