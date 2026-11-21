/**
 * timeExpandAudio.ts
 *
 * Client-side ultrasonic bat time expansion for Audible Bats playback.
 *
 * Instead of running server-side FFmpeg decoding, resampling, and caching,
 * browsers can play an ultrasonic recording time-expanded by `factor` simply by
 * dividing the WAV header's sampleRate (and byteRate) by `factor`. That avoids
 * browser playbackRate limits (16x max) and AudioContext.decodeAudioData()
 * resampling (which strips ultrasonic frequencies).
 *
 * - WAV clips (16/24/32-bit PCM, IEEE float, or WAVE_FORMAT_EXTENSIBLE):
 *   rewrites only `sampleRate` and `byteRate` in the `fmt ` chunk.
 * - FLAC clips: decoded at native sample rate (up to 384+ kHz) via WASM
 *   libflac and packaged as a 16-bit PCM WAV with the expanded sample rate.
 */

/** Minimum capture sample rate (Hz) required for ultrasonic time expansion. */
export const MIN_BAT_SAMPLE_RATE = 96_000;

export type AudioContainer = 'wav' | 'flac' | 'unknown';

const WAV_FORMAT_PCM = 1;
const WAV_FORMAT_IEEE_FLOAT = 3;
const WAV_FORMAT_EXTENSIBLE = 0xfffe;

const RIFF_HEADER_SIZE = 12;
const CHUNK_HEADER_SIZE = 8;
const MIN_FMT_CHUNK_SIZE = 16;
const EXTENSIBLE_FMT_CHUNK_SIZE = 40;
const FLAC_STREAMINFO_MIN_LENGTH = 34;
const FLAC_MIN_HEADER_SIZE = 8 + FLAC_STREAMINFO_MIN_LENGTH;
const WAV_PCM_HEADER_SIZE = 44;
const MAX_UINT32 = 0xffff_ffff;
const INT16_MIN_F = 32768;
const INT16_MAX_F = 32767;

function createAbortError(): Error {
  const err = new Error('Audible bats processing aborted');
  err.name = 'AbortError';
  return err;
}

function throwIfAborted(signal?: AbortSignal): void {
  if (signal?.aborted) {
    throw createAbortError();
  }
}

function readAscii4(bytes: Uint8Array, offset: number): string {
  return String.fromCharCode(
    bytes.at(offset) ?? 0,
    bytes.at(offset + 1) ?? 0,
    bytes.at(offset + 2) ?? 0,
    bytes.at(offset + 3) ?? 0
  );
}

function writeAscii4(view: DataView, offset: number, text: string): void {
  for (let i = 0; i < 4; i++) {
    view.setUint8(offset + i, text.charCodeAt(i));
  }
}

function isSupportedExtensibleSubFormat(view: DataView, subFormatOff: number): boolean {
  // Microsoft KSDATAFORMAT_SUBTYPE_PCM (1) / IEEE_FLOAT (3):
  // <uint32 tag>-0000-0010-8000-00aa00389b71
  const subTag = view.getUint32(subFormatOff, true);
  if (subTag !== WAV_FORMAT_PCM && subTag !== WAV_FORMAT_IEEE_FLOAT) {
    return false;
  }
  return (
    view.getUint16(subFormatOff + 4, true) === 0x0000 &&
    view.getUint16(subFormatOff + 6, true) === 0x0010 &&
    view.getUint32(subFormatOff + 8, false) === 0x800000aa &&
    view.getUint32(subFormatOff + 12, false) === 0x00389b71
  );
}

/** Identify WAV (RIFF/RF64 WAVE) or FLAC (fLaC) from container magic bytes. */
export function sniffAudioContainer(bytes: Uint8Array): AudioContainer {
  if (bytes.byteLength >= RIFF_HEADER_SIZE) {
    const rootId = readAscii4(bytes, 0);
    const waveId = readAscii4(bytes, 8);
    if ((rootId === 'RIFF' || rootId === 'RF64') && waveId === 'WAVE') {
      return 'wav';
    }
  }
  if (bytes.byteLength >= 4 && readAscii4(bytes, 0) === 'fLaC') {
    return 'flac';
  }
  return 'unknown';
}

/**
 * Read the native sample rate from a FLAC file's STREAMINFO metadata block
 * without invoking the WASM decoder, returning null if invalid.
 */
export function readFlacStreamInfoSampleRate(bytes: Uint8Array): number | null {
  if (bytes.byteLength < FLAC_MIN_HEADER_SIZE || readAscii4(bytes, 0) !== 'fLaC') {
    return null;
  }
  const blockHeader = bytes.at(4) ?? 0;
  const blockType = blockHeader & 0x7f;
  const blockLen = ((bytes.at(5) ?? 0) << 16) | ((bytes.at(6) ?? 0) << 8) | (bytes.at(7) ?? 0);
  if (blockType !== 0 || blockLen < FLAC_STREAMINFO_MIN_LENGTH) {
    return null;
  }
  // Sample rate sits 10 bytes into STREAMINFO (offset 18), packed into 20 bits big-endian.
  return (
    (((bytes.at(18) ?? 0) << 12) | ((bytes.at(19) ?? 0) << 4) | ((bytes.at(20) ?? 0) >>> 4)) >>> 0
  );
}

/**
 * Rewrite `sampleRate` and `byteRate` in a WAV container's `fmt ` chunk so the
 * browser plays ultrasonic audio time-expanded by `expansion`.
 * Mutates the passed buffer in place after all header checks succeed.
 */
export function rewriteWavSampleRate(buffer: ArrayBuffer, expansion: number): Blob {
  if (!Number.isFinite(expansion) || expansion < 1) {
    throw new RangeError(`Invalid time-expansion factor: ${expansion}`);
  }
  const bytes = new Uint8Array(buffer);
  if (sniffAudioContainer(bytes) !== 'wav') {
    throw new Error('Source audio is not a valid WAV container');
  }

  const view = new DataView(buffer);
  let offset = RIFF_HEADER_SIZE;

  while (offset + CHUNK_HEADER_SIZE <= bytes.byteLength) {
    const chunkId = readAscii4(bytes, offset);
    const chunkSize = view.getUint32(offset + 4, true);
    const payloadOffset = offset + CHUNK_HEADER_SIZE;

    if (chunkId === 'fmt ') {
      if (chunkSize < MIN_FMT_CHUNK_SIZE || payloadOffset + chunkSize > bytes.byteLength) {
        throw new Error('WAV fmt chunk is truncated');
      }

      const audioFormat = view.getUint16(payloadOffset, true);
      const channels = view.getUint16(payloadOffset + 2, true);
      const sampleRate = view.getUint32(payloadOffset + 4, true);
      const blockAlign = view.getUint16(payloadOffset + 12, true);
      const bitsPerSample = view.getUint16(payloadOffset + 14, true);

      const isSupportedFormat =
        audioFormat === WAV_FORMAT_PCM ||
        audioFormat === WAV_FORMAT_IEEE_FLOAT ||
        (audioFormat === WAV_FORMAT_EXTENSIBLE &&
          chunkSize >= EXTENSIBLE_FMT_CHUNK_SIZE &&
          isSupportedExtensibleSubFormat(view, payloadOffset + 24));
      if (!isSupportedFormat) {
        throw new Error(`Unsupported WAV audio format tag: ${audioFormat}`);
      }
      if (channels === 0 || blockAlign === 0 || bitsPerSample === 0) {
        throw new Error('Invalid WAV channel or sample geometry');
      }
      if (sampleRate < MIN_BAT_SAMPLE_RATE) {
        throw new Error(
          `Source audio must be at least ${MIN_BAT_SAMPLE_RATE} Hz for audible bats mode`
        );
      }

      const targetSampleRate = Math.max(1, Math.round(sampleRate / expansion));
      const targetByteRate = targetSampleRate * blockAlign;
      if (targetByteRate > MAX_UINT32) {
        throw new RangeError('WAV byte rate overflows 32-bit header field');
      }

      view.setUint32(payloadOffset + 4, targetSampleRate, true);
      view.setUint32(payloadOffset + 8, targetByteRate, true);
      return new Blob([buffer], { type: 'audio/wav' });
    }

    // RF64 uses 0xFFFFFFFF as a placeholder size on the data chunk; stop if out of range.
    if (chunkSize === MAX_UINT32 || payloadOffset + chunkSize > bytes.byteLength) {
      break;
    }
    const paddedSize = chunkSize + (chunkSize & 1);
    offset = payloadOffset + paddedSize;
  }

  throw new Error('WAV file is missing a valid fmt chunk');
}

/** Package planar float32 PCM channels into a standard 16-bit PCM WAV Blob. */
export function encodePcm16Wav(channelData: Float32Array[], sampleRate: number): Blob {
  if (channelData.length === 0) {
    throw new Error('No audio channels decoded');
  }
  const firstChannel = channelData.at(0);
  if (!firstChannel || firstChannel.length === 0) {
    throw new Error('Decoded audio contains zero samples');
  }
  if (!Number.isFinite(sampleRate) || sampleRate < 1 || sampleRate > MAX_UINT32) {
    throw new RangeError(`Invalid output WAV sample rate: ${sampleRate}`);
  }

  const numChannels = channelData.length;
  const numFrames = firstChannel.length;
  const bytesPerSample = 2;
  const blockAlign = numChannels * bytesPerSample;
  const dataByteLength = numFrames * blockAlign;
  const totalByteLength = WAV_PCM_HEADER_SIZE + dataByteLength;

  if (totalByteLength > MAX_UINT32) {
    throw new RangeError('Expanded WAV exceeds 4 GiB RIFF size limit');
  }

  const outBuffer = new ArrayBuffer(totalByteLength);
  const view = new DataView(outBuffer);

  writeAscii4(view, 0, 'RIFF');
  view.setUint32(4, totalByteLength - 8, true);
  writeAscii4(view, 8, 'WAVE');
  writeAscii4(view, 12, 'fmt ');
  view.setUint32(16, MIN_FMT_CHUNK_SIZE, true);
  view.setUint16(20, WAV_FORMAT_PCM, true);
  view.setUint16(22, numChannels, true);
  view.setUint32(24, sampleRate, true);
  view.setUint32(28, sampleRate * blockAlign, true);
  view.setUint16(32, blockAlign, true);
  view.setUint16(34, 16, true);
  writeAscii4(view, 36, 'data');
  view.setUint32(40, dataByteLength, true);

  let writeOffset = WAV_PCM_HEADER_SIZE;
  for (let frame = 0; frame < numFrames; frame++) {
    for (const channel of channelData) {
      const sample = channel.at(frame) ?? 0;
      const clamped = Math.max(-1, Math.min(1, sample));
      const intSample =
        clamped < 0 ? Math.round(clamped * INT16_MIN_F) : Math.round(clamped * INT16_MAX_F);
      view.setInt16(writeOffset, intSample, true);
      writeOffset += bytesPerSample;
    }
  }

  return new Blob([outBuffer], { type: 'audio/wav' });
}

async function expandFlacAudio(
  bytes: Uint8Array,
  expansion: number,
  signal?: AbortSignal
): Promise<Blob> {
  const headerSampleRate = readFlacStreamInfoSampleRate(bytes);
  if (headerSampleRate === null) {
    throw new Error('Source FLAC is missing a valid STREAMINFO header');
  }
  if (headerSampleRate < MIN_BAT_SAMPLE_RATE) {
    throw new Error(
      `Source audio must be at least ${MIN_BAT_SAMPLE_RATE} Hz for audible bats mode`
    );
  }

  throwIfAborted(signal);
  const { FLACDecoder } = await import('@wasm-audio-decoders/flac');
  throwIfAborted(signal);

  const decoder = new FLACDecoder();
  try {
    await decoder.ready;
    throwIfAborted(signal);

    const decoded = await decoder.decodeFile(bytes);
    throwIfAborted(signal);

    if (decoded.samplesDecoded === 0 || decoded.channelData.length === 0) {
      const firstErr = decoded.errors.at(0)?.message;
      throw new Error(firstErr ?? 'Failed to decode FLAC audio');
    }
    if (decoded.sampleRate < MIN_BAT_SAMPLE_RATE) {
      throw new Error(
        `Source audio must be at least ${MIN_BAT_SAMPLE_RATE} Hz for audible bats mode`
      );
    }

    const trimmedChannels = decoded.channelData.map(ch =>
      ch.length === decoded.samplesDecoded ? ch : ch.subarray(0, decoded.samplesDecoded)
    );
    const targetSampleRate = Math.max(1, Math.round(decoded.sampleRate / expansion));
    return encodePcm16Wav(trimmedChannels, targetSampleRate);
  } finally {
    decoder.free();
  }
}

/**
 * Convert an ultrasonic WAV or FLAC clip buffer into a playable WAV Blob
 * time-expanded by `expansion`.
 */
export async function timeExpandAudio(
  buffer: ArrayBuffer,
  expansion: number,
  signal?: AbortSignal
): Promise<Blob> {
  if (!Number.isFinite(expansion) || expansion < 1) {
    throw new RangeError(`Invalid time-expansion factor: ${expansion}`);
  }
  throwIfAborted(signal);

  const bytes = new Uint8Array(buffer);
  const container = sniffAudioContainer(bytes);

  switch (container) {
    case 'wav':
      return rewriteWavSampleRate(buffer, expansion);
    case 'flac':
      return expandFlacAudio(bytes, expansion, signal);
    case 'unknown':
      throw new Error('Source audio format is not supported for audible bats mode');
  }
}
