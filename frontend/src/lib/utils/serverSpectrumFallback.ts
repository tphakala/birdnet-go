/**
 * Server-spectrum fallback controller for the live spectrogram.
 *
 * WebKit cannot route an HLS-backed media element into the Web Audio graph
 * (https://bugs.webkit.org/show_bug.cgi?id=180696), so on Safari the live
 * spectrogram's AnalyserNode reports nothing and the waterfall stays blank.
 * This owns the whole workaround: watch the local analyser, and if it turns out
 * to be blind, subscribe to magnitude columns computed on the server and
 * delivered over the audio-level SSE stream.
 *
 * It is deliberately free of Svelte runes and of any Web Audio state, so it can
 * be unit tested with plain fakes and so `useSpectrogramAnalyser` keeps only a
 * handful of lines of wiring. Everything it needs from the host arrives through
 * `ServerSpectrumHooks`; everything it produces leaves through `onColumn` and
 * `onAdopt`.
 */

import { buildAppUrl } from './urlHelpers';
import { loggers } from './logger';
import { ReconnectingEventSource } from './ReconnectingEventSource';
import {
  decodeSpectrumColumn,
  hasSpectrumEnergy,
  nextSpectrumRender,
  trimSpectrumQueue,
  type SpectrumColumn,
  type SpectrumRenderState,
} from './serverSpectrum';

const logger = loggers.audio;

/** How often the local analyser is checked (ms) */
const PROBE_INTERVAL = 250;
/** Consecutive silent analyser reads, while playing, before asking the server */
const PROBE_THRESHOLD = 4;
/** Bin value above which a server column counts as carrying energy (0-255) */
const ENERGY_THRESHOLD = 8;
/** How often a queued column is promoted to the render buffer (ms) */
const PUBLISH_INTERVAL = 50;
/**
 * Longest playback lag the column queue can cover (seconds). HLS live latency
 * is a few seconds; holding more would only grow memory to redraw audio nobody
 * is listening to.
 */
const QUEUE_SECONDS = 30;
/** Hard cap on queued columns, in case a server clock jumps backwards */
const QUEUE_MAX_COLUMNS = 800;
/** Blank the waterfall when no new column has been published for this long (ms) */
const STALL_TIMEOUT = 1500;

/**
 * What the host's analyser looks like right now.
 *
 * - `working` — it produced data, so this browser needs no fallback at all.
 * - `silent`  — audio is playing through a running context but every bin is 0.
 * - `absent`  — no analyser could be built; there is nothing to compare against.
 * - `idle`    — paused, suspended, or not started: proves nothing either way.
 */
export type AnalyserProbe = 'working' | 'silent' | 'absent' | 'idle';

export interface ServerSpectrumHooks {
  /** Inspect the local analyser. Called every PROBE_INTERVAL until decided. */
  probeAnalyser: () => AnalyserProbe;
  /**
   * Wall-clock time (Unix seconds) of the audio currently being heard, or 0 if
   * unknown. Columns are held back until the playhead reaches them so the
   * waterfall matches the buffered audio rather than live capture.
   */
  playheadWallClock: () => number;
  /** Render these bins. Called with an all-zero column to blank the waterfall. */
  onColumn: (bins: Uint8Array<ArrayBuffer>) => void;
  /** The fallback has taken over; the host should stop polling its analyser. */
  onAdopt: (info: { sampleRate: number; binCount: number; hadAnalyser: boolean }) => void;
}

export interface ServerSpectrumFallback {
  /** Begin watching the analyser for this audio source. */
  start: (sourceID: string) => void;
  /** Close the stream, clear the timers, and forget everything. */
  stop: () => void;
}

interface SpectrumEntry {
  spectrum?: string;
  spectrumSampleRate?: number;
  spectrumTime?: number;
}

export function createServerSpectrumFallback(hooks: ServerSpectrumHooks): ServerSpectrumFallback {
  let sourceID: string | null = null;
  let stream: ReconnectingEventSource | null = null;
  let probeTimer: ReturnType<typeof globalThis.setInterval> | null = null;
  let publishTimer: ReturnType<typeof globalThis.setInterval> | null = null;
  let columns: SpectrumColumn[] = [];
  let render: SpectrumRenderState = { renderedTime: 0, advancedAt: 0, unaligned: false };
  let sampleRate = 0;
  let binCount = 0;
  let silentProbes = 0;
  let adopted = false;
  let hadAnalyser = true;
  /**
   * Bumped by every start/stop. Timers and SSE callbacks check it, so a frame
   * already queued when the stream was replaced cannot resurrect a fallback for
   * a source nobody is listening to.
   */
  let epoch = 0;

  function start(id: string): void {
    stop();
    sourceID = id;
    const mine = epoch;
    probeTimer = globalThis.setInterval(() => probe(mine), PROBE_INTERVAL);
  }

  function stop(): void {
    epoch++;
    clearTimer('probe');
    clearTimer('publish');
    stream?.close();
    stream = null;
    columns = [];
    render = { renderedTime: 0, advancedAt: 0, unaligned: false };
    sourceID = null;
    sampleRate = 0;
    binCount = 0;
    silentProbes = 0;
    adopted = false;
    hadAnalyser = true;
  }

  function clearTimer(which: 'probe' | 'publish'): void {
    const timer = which === 'probe' ? probeTimer : publishTimer;
    if (timer === null) return;
    globalThis.clearInterval(timer);
    if (which === 'probe') probeTimer = null;
    else publishTimer = null;
  }

  /**
   * Decide whether the local analyser can see this stream.
   *
   * All-zero bins alone prove nothing — silence, a paused element and a
   * suspended context look identical. So this waits for the analyser to stay
   * empty while audio is playing, and only then opens the server stream to ask
   * whether there was anything to see. One non-empty read ends the whole thing:
   * that browser is fine, and never pays for the extra connection.
   */
  function probe(mine: number): void {
    if (mine !== epoch || adopted) return;

    switch (hooks.probeAnalyser()) {
      case 'working':
        stop();
        return;
      case 'absent':
        hadAnalyser = false;
        openStream(mine);
        return;
      case 'silent':
        if (++silentProbes >= PROBE_THRESHOLD) openStream(mine);
        return;
      case 'idle':
        return;
    }
  }

  /** Subscribe to server-computed columns for this source. No-op if already open. */
  function openStream(mine: number): void {
    const id = sourceID;
    if (stream || !id) return;

    const url = buildAppUrl(`/api/v2/streams/audio-level?spectrum=${encodeURIComponent(id)}`);
    const sse = new ReconnectingEventSource(url, { max_retry_time: 30000, withCredentials: false });
    stream = sse;

    sse.onmessage = (event: globalThis.MessageEvent) => {
      if (mine !== epoch || stream !== sse) return;

      try {
        const data = JSON.parse(event.data) as {
          type?: string;
          levels?: Record<string, SpectrumEntry>;
        };
        if (data.type !== 'audio-level') return;
        // eslint-disable-next-line security/detect-object-injection -- id is this controller's own audio source
        const entry = data.levels?.[id];
        if (!entry?.spectrum) return;

        const bins = decodeSpectrumColumn(entry.spectrum);
        if (!bins) return;

        if (entry.spectrumSampleRate) sampleRate = entry.spectrumSampleRate;
        columns.push({ bins, time: entry.spectrumTime ?? 0 });
        columns = trimSpectrumQueue(columns, QUEUE_SECONDS, QUEUE_MAX_COLUMNS);

        // Either there is no analyser to compare against, or the server can see
        // energy on this source that the analyser cannot: the local Web Audio
        // path is confirmed dead.
        if (!adopted && (!hadAnalyser || hasSpectrumEnergy(bins, ENERGY_THRESHOLD))) {
          adopt(mine);
        }
      } catch {
        /* ignore malformed frames; the next one is 50ms away */
      }
    };

    sse.onerror = () => {
      /* ReconnectingEventSource handles reconnection */
    };

    logger.debug('Live spectrogram opened server spectrum stream', { sourceID: id });
  }

  /** Hand rendering over to the server columns for the rest of this session. */
  function adopt(mine: number): void {
    adopted = true;
    binCount = columns.at(-1)?.bins.length ?? 0;
    render = { renderedTime: 0, advancedAt: Date.now(), unaligned: false };

    clearTimer('probe');
    publishTimer = globalThis.setInterval(() => pump(mine), PUBLISH_INTERVAL);

    logger.info('Live spectrogram falling back to server-computed bins', {
      reason: hadAnalyser ? 'analyser returned no data' : 'analyser unavailable',
      sampleRate,
      bins: binCount,
    });

    hooks.onAdopt({ sampleRate, binCount, hadAnalyser });
    pump(mine);
  }

  /** Apply one render decision. All the timing logic lives in nextSpectrumRender. */
  function pump(mine: number): void {
    if (mine !== epoch || !adopted) return;

    const wasAligned = !render.unaligned;
    const step = nextSpectrumRender(
      columns,
      hooks.playheadWallClock(),
      render,
      Date.now(),
      STALL_TIMEOUT
    );
    render = step.state;

    if (wasAligned && render.unaligned) {
      logger.warn(
        'Live spectrogram could not align server columns to the playhead; rendering live columns instead',
        { hint: 'check that the browser and server clocks agree' }
      );
    }

    if (step.blank) {
      hooks.onColumn(new Uint8Array(binCount));
      return;
    }
    if (step.index < 0) return;

    hooks.onColumn(columns[step.index].bins);
  }

  return { start, stop };
}
