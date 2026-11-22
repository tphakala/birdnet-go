/**
 * timeExpandAudio.ts
 *
 * Client-side ultrasonic bat time expansion for Audible Bats playback.
 *
 * Instead of running server-side FFmpeg decoding, resampling, and caching,
 * browsers can play an ultrasonic recording time-expanded by `factor` simply by
 * dividing the WAV header's sampleRate (and byteRate) by `factor`. That avoids
 * browser playbackRate limits (16x max) and Web Audio decodeAudioData()
 * resampling (which strips ultrasonic frequencies).
 *
 * - WAV clips (16/24/32-bit PCM, IEEE float, or WAVE_FORMAT_EXTENSIBLE):
 *   rewrites only `sampleRate` and `byteRate` in the `fmt ` chunk.
 * - FLAC clips: decoded at native sample rate (up to 384+ kHz) via lazy-loaded
 *   WASM libflac (streams in chunks when STREAMINFO provides totalSamples to
 *   bound peak memory) and packaged as a 16-bit PCM WAV at the expanded rate.
 *   Note: `@wasm-audio-decoders/flac` uses `codec-parser` (`LGPL-3.0-or-later`)
 *   and requires `'wasm-unsafe-eval'` if a reverse proxy restricts `script-src`.
 */

/** Minimum capture sample rate (Hz) required for ultrasonic time expansion. */
export const MIN_BAT_SAMPLE_RATE = 96_000;

/**
 * Maximum decoded 16-bit PCM size (bytes) permitted during client-side FLAC
 * expansion (matches backend `maxDecodedPCMBytes` = 128 MiB) to prevent OOM
 * on mobile/Safari tabs for multi-minute ultrasonic captures.
 */
export const MAX_DECODED_PCM_BYTES = 128 * 1024 * 1024;

export type AudioContainer = 'wav' | 'flac' | 'unknown';

export type TimeExpandErrorCode =
  | 'invalid_factor'
  | 'unsupported_format'
  | 'invalid_wav'
  | 'unsupported_wav_format'
  | 'invalid_flac'
  | 'sample_rate_too_low'
  | 'pcm_too_large'
  | 'flac_decode_failed'
  | 'empty_audio';

export class TimeExpandAudioError extends Error {
  readonly code: TimeExpandErrorCode;

  constructor(code: TimeExpandErrorCode, message: string, options?: { cause?: unknown }) {
    super(message, options);
    this.name = 'TimeExpandAudioError';
    this.code = code;
  }
}

export interface FlacStreamInfo {
  sampleRate: number;
  channels: number;
  bitDepth: number;
  totalSamples: number;
}

const WAV_FORMAT_PCM = 1;
const WAV_FORMAT_IEEE_FLOAT = 3;
const WAV_FORMAT_EXTENSIBLE = 0xfffe;

const RIFF_HEADER_SIZE = 12;
const CHUNK_HEADER_SIZE = 8;
const MIN_FMT_CHUNK_SIZE = 16;
const EXTENSIBLE_FMT_CHUNK_SIZE = 40;
const EXTENSIBLE_CB_SIZE = 22;
const FLAC_STREAMINFO_MIN_LENGTH = 34;
const FLAC_MIN_HEADER_SIZE = 8 + FLAC_STREAMINFO_MIN_LENGTH;
const FLAC_STREAM_CHUNK_BYTES = 256 * 1024;
const WAV_PCM_HEADER_SIZE = 44;
const MAX_UINT32 = 0xffff_ffff;
const INT16_MIN_VAL = -32768;
const INT16_MAX_VAL = 32767;
const INT16_SCALE = 32768;

const IS_LITTLE_ENDIAN = new Uint8Array(new Uint16Array([1]).buffer).at(0) === 1;

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

function yieldToEventLoop(): Promise<void> {
  return new Promise(resolve => {
    setTimeout(resolve, 0);
  });
}

function readAscii4(bytes: Uint8Array, offset: number): string {
  return String.fromCharCode(
    bytes.at(offset) ?? 0,
    bytes.at(offset + 1) ?? 0,
    bytes.at(offset + 2) ?? 0,
    bytes.at(offset + 3) ?? 0
  );
}

function isPrintableAscii4(bytes: Uint8Array, offset: number): boolean {
  for (let i = 0; i < 4; i++) {
    const ch = bytes.at(offset + i) ?? 0;
    if (ch < 0x20 || ch > 0x7e) return false;
  }
  return true;
}

function hasTrailingRiffChunks(bytes: Uint8Array, view: DataView, startOffset: number): boolean {
  let off = startOffset;
  let count = 0;
  while (off + CHUNK_HEADER_SIZE <= bytes.byteLength) {
    if (!isPrintableAscii4(bytes, off)) return false;
    const size = view.getUint32(off + 4, true);
    const paddedEnd = off + CHUNK_HEADER_SIZE + size + (size & 1);
    if (paddedEnd > bytes.byteLength) return false;
    off = paddedEnd;
    count++;
  }
  return count > 0 && off === bytes.byteLength;
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

/**
 * Identify WAV (RIFF WAVE) or FLAC (fLaC) from container magic bytes.
 * RF64 (>4 GiB WAV) is rejected as `'unknown'` because browsers cannot play it.
 */
export function sniffAudioContainer(bytes: Uint8Array): AudioContainer {
  if (bytes.byteLength >= RIFF_HEADER_SIZE) {
    if (readAscii4(bytes, 0) === 'RIFF' && readAscii4(bytes, 8) === 'WAVE') {
      return 'wav';
    }
  }
  if (bytes.byteLength >= 4 && readAscii4(bytes, 0) === 'fLaC') {
    return 'flac';
  }
  return 'unknown';
}

/**
 * Parse STREAMINFO metadata from a FLAC header without initializing WASM,
 * returning null when invalid.
 */
export function readFlacStreamInfo(bytes: Uint8Array): FlacStreamInfo | null {
  if (bytes.byteLength < FLAC_MIN_HEADER_SIZE || readAscii4(bytes, 0) !== 'fLaC') {
    return null;
  }
  const blockHeader = bytes.at(4) ?? 0;
  const blockType = blockHeader & 0x7f;
  const blockLen = ((bytes.at(5) ?? 0) << 16) | ((bytes.at(6) ?? 0) << 8) | (bytes.at(7) ?? 0);
  if (blockType !== 0 || blockLen < FLAC_STREAMINFO_MIN_LENGTH) {
    return null;
  }

  const b18 = bytes.at(18) ?? 0;
  const b19 = bytes.at(19) ?? 0;
  const b20 = bytes.at(20) ?? 0;
  const b21 = bytes.at(21) ?? 0;
  const b22 = bytes.at(22) ?? 0;
  const b23 = bytes.at(23) ?? 0;
  const b24 = bytes.at(24) ?? 0;
  const b25 = bytes.at(25) ?? 0;

  const sampleRate = ((b18 << 12) | (b19 << 4) | (b20 >>> 4)) >>> 0;
  const channels = ((b20 >>> 1) & 0x07) + 1;
  const bitDepth = (((b20 & 0x01) << 4) | (b21 >>> 4)) + 1;
  const totalSamples =
    (b21 & 0x0f) * 0x1_0000_0000 + (((b22 << 24) | (b23 << 16) | (b24 << 8) | b25) >>> 0);

  return { sampleRate, channels, bitDepth, totalSamples };
}

/** Read only the native sample rate from a FLAC STREAMINFO header. */
export function readFlacStreamInfoSampleRate(bytes: Uint8Array): number | null {
  return readFlacStreamInfo(bytes)?.sampleRate ?? null;
}

/**
 * Rewrite `sampleRate` and `byteRate` in a RIFF WAV container's `fmt ` chunk so
 * the browser plays ultrasonic audio time-expanded by `expansion`.
 * Mutates the passed buffer in place only after validating both `fmt ` and `data`.
 */
export function rewriteWavSampleRate(buffer: ArrayBuffer, expansion: number): Blob {
  if (!Number.isFinite(expansion) || expansion < 1) {
    throw new TimeExpandAudioError('invalid_factor', `Invalid time-expansion factor: ${expansion}`);
  }
  const bytes = new Uint8Array(buffer);
  if (sniffAudioContainer(bytes) !== 'wav') {
    const isRf64 =
      bytes.byteLength >= RIFF_HEADER_SIZE &&
      readAscii4(bytes, 0) === 'RF64' &&
      readAscii4(bytes, 8) === 'WAVE';
    throw new TimeExpandAudioError(
      isRf64 ? 'unsupported_format' : 'invalid_wav',
      isRf64
        ? 'RF64 WAV containers are not supported for browser playback'
        : 'Source audio is not a valid WAV container'
    );
  }

  const view = new DataView(buffer);
  let offset = RIFF_HEADER_SIZE;
  let fmtPayloadOffset = -1;
  let targetSampleRate = 0;
  let targetByteRate = 0;
  let dataSizeOffset = -1;
  let resolvedDataSize = 0;

  while (offset + CHUNK_HEADER_SIZE <= bytes.byteLength) {
    const chunkId = readAscii4(bytes, offset);
    const chunkSize = view.getUint32(offset + 4, true);
    const payloadOffset = offset + CHUNK_HEADER_SIZE;

    if (chunkId === 'data') {
      const hasZeroPayload =
        payloadOffset >= bytes.byteLength ||
        (chunkSize === 0 && hasTrailingRiffChunks(bytes, view, payloadOffset));
      if (hasZeroPayload) {
        throw new TimeExpandAudioError('empty_audio', 'WAV file contains zero audio bytes');
      }
      dataSizeOffset = offset + 4;
      const actualRemaining = bytes.byteLength - payloadOffset;
      resolvedDataSize =
        chunkSize === 0 || chunkSize > actualRemaining ? actualRemaining : chunkSize;
      if (fmtPayloadOffset !== -1) {
        break;
      }
    }

    if (payloadOffset + chunkSize > bytes.byteLength) {
      break;
    }

    if (chunkId === 'fmt ') {
      if (chunkSize < MIN_FMT_CHUNK_SIZE) {
        throw new TimeExpandAudioError('invalid_wav', 'WAV fmt chunk is truncated');
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
          view.getUint16(payloadOffset + 16, true) >= EXTENSIBLE_CB_SIZE &&
          isSupportedExtensibleSubFormat(view, payloadOffset + 24));
      if (!isSupportedFormat) {
        throw new TimeExpandAudioError(
          'unsupported_wav_format',
          `Unsupported WAV audio format tag: ${audioFormat}`
        );
      }
      if (channels === 0 || blockAlign === 0 || bitsPerSample === 0) {
        throw new TimeExpandAudioError('invalid_wav', 'Invalid WAV channel or sample geometry');
      }
      if (sampleRate < MIN_BAT_SAMPLE_RATE) {
        throw new TimeExpandAudioError(
          'sample_rate_too_low',
          `Source audio must be at least ${MIN_BAT_SAMPLE_RATE} Hz for audible bats mode`
        );
      }

      targetSampleRate = Math.max(1, Math.round(sampleRate / expansion));
      targetByteRate = targetSampleRate * blockAlign;
      if (targetByteRate > MAX_UINT32) {
        throw new TimeExpandAudioError(
          'invalid_wav',
          'WAV byte rate overflows 32-bit header field'
        );
      }
      fmtPayloadOffset = payloadOffset;
    }

    if (fmtPayloadOffset !== -1 && dataSizeOffset !== -1) {
      break;
    }

    const paddedSize = chunkSize + (chunkSize & 1);
    if (payloadOffset + paddedSize > bytes.byteLength) {
      break;
    }
    offset = payloadOffset + paddedSize;
  }

  if (fmtPayloadOffset === -1 || dataSizeOffset === -1) {
    throw new TimeExpandAudioError('invalid_wav', 'WAV file is missing a valid fmt or data chunk');
  }

  // Finalize zero/truncated/streaming RIFF and data chunk sizes so browsers such as Firefox report non-zero duration.
  const declaredRiffSize = view.getUint32(4, true);
  const maxRiffSize = bytes.byteLength - 8;
  if (declaredRiffSize === 0 || declaredRiffSize > maxRiffSize) {
    view.setUint32(4, maxRiffSize, true);
  }
  if (view.getUint32(dataSizeOffset, true) !== resolvedDataSize) {
    view.setUint32(dataSizeOffset, resolvedDataSize, true);
  }
  view.setUint32(fmtPayloadOffset + 4, targetSampleRate, true);
  view.setUint32(fmtPayloadOffset + 8, targetByteRate, true);
  return new Blob([buffer], { type: 'audio/wav' });
}

function writeWavHeader(
  view: DataView,
  numChannels: number,
  sampleRate: number,
  dataByteLength: number
): void {
  const bytesPerSample = 2;
  const blockAlign = numChannels * bytesPerSample;
  const totalByteLength = WAV_PCM_HEADER_SIZE + dataByteLength;

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
}

function clampFloatToInt16(sample: number): number {
  const scaled = Math.round(sample * INT16_SCALE);
  if (scaled < INT16_MIN_VAL) return INT16_MIN_VAL;
  if (scaled > INT16_MAX_VAL) return INT16_MAX_VAL;
  return scaled;
}

function writeFramesPcm16(
  outBuffer: ArrayBuffer,
  frameOffset: number,
  channelData: Float32Array[],
  numFrames: number
): void {
  const numChannels = channelData.length;
  const firstChannel = channelData.at(0);
  if (!firstChannel || numFrames === 0) return;

  if (IS_LITTLE_ENDIAN) {
    const pcmOut = new Int16Array(
      outBuffer,
      WAV_PCM_HEADER_SIZE + frameOffset * numChannels * 2,
      numFrames * numChannels
    );
    if (numChannels === 1) {
      for (let i = 0; i < numFrames; i++) {
        // TypedArray indexed access stays inside bounds and avoids slow DataView calls.
        // eslint-disable-next-line security/detect-object-injection
        pcmOut[i] = clampFloatToInt16(firstChannel[i]);
      }
      return;
    }
    let outIdx = 0;
    for (let frame = 0; frame < numFrames; frame++) {
      for (let ch = 0; ch < numChannels; ch++) {
        // eslint-disable-next-line security/detect-object-injection
        pcmOut[outIdx++] = clampFloatToInt16(channelData[ch][frame]);
      }
    }
    return;
  }

  const view = new DataView(outBuffer);
  let writeOffset = WAV_PCM_HEADER_SIZE + frameOffset * numChannels * 2;
  for (let frame = 0; frame < numFrames; frame++) {
    for (const channel of channelData) {
      view.setInt16(writeOffset, clampFloatToInt16(channel.at(frame) ?? 0), true);
      writeOffset += 2;
    }
  }
}

/** Package planar float32 PCM channels into a standard 16-bit PCM WAV Blob. */
export function encodePcm16Wav(channelData: Float32Array[], sampleRate: number): Blob {
  if (channelData.length === 0) {
    throw new TimeExpandAudioError('empty_audio', 'No audio channels decoded');
  }
  const firstChannel = channelData.at(0);
  if (!firstChannel || firstChannel.length === 0) {
    throw new TimeExpandAudioError('empty_audio', 'Decoded audio contains zero samples');
  }
  const numChannels = channelData.length;
  if (!Number.isFinite(sampleRate) || sampleRate < 1 || sampleRate * numChannels * 2 > MAX_UINT32) {
    throw new TimeExpandAudioError('invalid_wav', `Invalid output WAV sample rate: ${sampleRate}`);
  }
  const numFrames = firstChannel.length;
  const dataByteLength = numFrames * numChannels * 2;
  const totalByteLength = WAV_PCM_HEADER_SIZE + dataByteLength;

  if (dataByteLength > MAX_DECODED_PCM_BYTES || totalByteLength > MAX_UINT32) {
    throw new TimeExpandAudioError(
      'pcm_too_large',
      `Decoded audio exceeds ${MAX_DECODED_PCM_BYTES} byte limit`
    );
  }

  const outBuffer = new ArrayBuffer(totalByteLength);
  writeWavHeader(new DataView(outBuffer), numChannels, sampleRate, dataByteLength);
  writeFramesPcm16(outBuffer, 0, channelData, numFrames);
  return new Blob([outBuffer], { type: 'audio/wav' });
}

interface DecodedFlacChunk {
  channelData: Float32Array[];
  samplesDecoded: number;
  sampleRate: number;
  errors: Array<{ message: string }>;
}

function appendDecodedChunk(
  outBuffer: ArrayBuffer | null,
  chunks: Float32Array[][],
  framesWritten: number,
  expectedChannels: number,
  decoded: DecodedFlacChunk
): number {
  if (decoded.samplesDecoded === 0 || decoded.channelData.length === 0) {
    return 0;
  }
  if (decoded.channelData.length !== expectedChannels) {
    throw new TimeExpandAudioError(
      'flac_decode_failed',
      `Decoded FLAC channel count (${decoded.channelData.length}) does not match STREAMINFO (${expectedChannels})`
    );
  }
  const frames = decoded.samplesDecoded;
  const newTotalBytes = (framesWritten + frames) * expectedChannels * 2;
  if (newTotalBytes > MAX_DECODED_PCM_BYTES) {
    throw new TimeExpandAudioError(
      'pcm_too_large',
      `Decoded FLAC exceeds ${MAX_DECODED_PCM_BYTES} byte limit`
    );
  }

  if (outBuffer !== null) {
    const capacityFrames = (outBuffer.byteLength - WAV_PCM_HEADER_SIZE) / (expectedChannels * 2);
    if (framesWritten + frames <= capacityFrames) {
      writeFramesPcm16(outBuffer, framesWritten, decoded.channelData, frames);
      return frames;
    }
    throw new TimeExpandAudioError(
      'pcm_too_large',
      'Decoded FLAC frames exceeded STREAMINFO sample count'
    );
  }

  // Fallback for STREAMINFO.totalSamples === 0: retain per-chunk copies up to MAX_DECODED_PCM_BYTES.
  chunks.push(decoded.channelData.map(ch => ch.slice(0, frames)));
  return frames;
}

async function expandFlacAudio(
  bytes: Uint8Array,
  expansion: number,
  signal?: AbortSignal
): Promise<Blob> {
  const headerInfo = readFlacStreamInfo(bytes);
  if (headerInfo === null) {
    throw new TimeExpandAudioError(
      'invalid_flac',
      'Source FLAC is missing a valid STREAMINFO header'
    );
  }
  if (headerInfo.sampleRate < MIN_BAT_SAMPLE_RATE) {
    throw new TimeExpandAudioError(
      'sample_rate_too_low',
      `Source audio must be at least ${MIN_BAT_SAMPLE_RATE} Hz for audible bats mode`
    );
  }
  if (headerInfo.totalSamples > 0) {
    const estimatedPcmBytes = headerInfo.totalSamples * headerInfo.channels * 2;
    if (estimatedPcmBytes > MAX_DECODED_PCM_BYTES) {
      throw new TimeExpandAudioError(
        'pcm_too_large',
        `Estimated decoded FLAC size (${estimatedPcmBytes} bytes) exceeds ${MAX_DECODED_PCM_BYTES} byte limit`
      );
    }
  }

  throwIfAborted(signal);
  const { FLACDecoder } = await import('@wasm-audio-decoders/flac');
  throwIfAborted(signal);

  const preAllocatedBuffer =
    headerInfo.totalSamples > 0
      ? new ArrayBuffer(WAV_PCM_HEADER_SIZE + headerInfo.totalSamples * headerInfo.channels * 2)
      : null;
  const fallbackChunks: Float32Array[][] = [];

  let isReady = false;
  let framesWritten = 0;
  let detectedSampleRate = headerInfo.sampleRate;
  let firstErrorMsg: string | undefined;

  const decoder = new FLACDecoder();
  try {
    await decoder.ready;
    isReady = true;
    throwIfAborted(signal);

    for (let off = 0; off < bytes.byteLength; off += FLAC_STREAM_CHUNK_BYTES) {
      if (off > 0) {
        // Yield a macrotask between synchronous WASM decode chunks so queued UI input/cancel handlers can run.
        await yieldToEventLoop();
      }
      throwIfAborted(signal);
      const chunk = bytes.subarray(off, Math.min(bytes.byteLength, off + FLAC_STREAM_CHUNK_BYTES));
      const decoded = await decoder.decode(chunk);
      if (decoded.sampleRate > 0) detectedSampleRate = decoded.sampleRate;
      firstErrorMsg ??= decoded.errors.at(0)?.message;
      framesWritten += appendDecodedChunk(
        preAllocatedBuffer,
        fallbackChunks,
        framesWritten,
        headerInfo.channels,
        decoded
      );
    }

    if (bytes.byteLength > FLAC_STREAM_CHUNK_BYTES) {
      await yieldToEventLoop();
    }
    throwIfAborted(signal);
    const flushed = await decoder.flush();
    if (flushed.sampleRate > 0) detectedSampleRate = flushed.sampleRate;
    firstErrorMsg ??= flushed.errors.at(0)?.message;
    framesWritten += appendDecodedChunk(
      preAllocatedBuffer,
      fallbackChunks,
      framesWritten,
      headerInfo.channels,
      flushed
    );
  } finally {
    if (isReady) {
      try {
        decoder.free();
      } catch {
        // Ignore cleanup errors if decoder state was already torn down.
      }
    }
  }

  throwIfAborted(signal);
  if (framesWritten === 0) {
    throw new TimeExpandAudioError('flac_decode_failed', firstErrorMsg ?? 'Failed to decode FLAC');
  }
  if (detectedSampleRate < MIN_BAT_SAMPLE_RATE) {
    throw new TimeExpandAudioError(
      'sample_rate_too_low',
      `Source audio must be at least ${MIN_BAT_SAMPLE_RATE} Hz for audible bats mode`
    );
  }

  const targetSampleRate = Math.max(1, Math.round(detectedSampleRate / expansion));
  const actualDataBytes = framesWritten * headerInfo.channels * 2;

  if (preAllocatedBuffer !== null) {
    const finalBuffer =
      actualDataBytes === preAllocatedBuffer.byteLength - WAV_PCM_HEADER_SIZE
        ? preAllocatedBuffer
        : preAllocatedBuffer.slice(0, WAV_PCM_HEADER_SIZE + actualDataBytes);
    writeWavHeader(
      new DataView(finalBuffer),
      headerInfo.channels,
      targetSampleRate,
      actualDataBytes
    );
    return new Blob([finalBuffer], { type: 'audio/wav' });
  }

  // Merge streaming chunks when STREAMINFO.totalSamples was 0.
  const merged = new ArrayBuffer(WAV_PCM_HEADER_SIZE + actualDataBytes);
  writeWavHeader(new DataView(merged), headerInfo.channels, targetSampleRate, actualDataBytes);
  let mergedFrameOffset = 0;
  for (const chunkChannels of fallbackChunks) {
    const chunkFrames = chunkChannels.at(0)?.length ?? 0;
    writeFramesPcm16(merged, mergedFrameOffset, chunkChannels, chunkFrames);
    mergedFrameOffset += chunkFrames;
  }
  return new Blob([merged], { type: 'audio/wav' });
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
    throw new TimeExpandAudioError('invalid_factor', `Invalid time-expansion factor: ${expansion}`);
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
      throw new TimeExpandAudioError(
        'unsupported_format',
        'Source audio format is not supported for audible bats mode'
      );
  }
}
