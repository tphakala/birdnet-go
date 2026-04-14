<script lang="ts">
  /* global AnalyserNode, ResizeObserver, performance, requestAnimationFrame, cancelAnimationFrame */
  /**
   * SpectrogramCanvas — Pure waterfall spectrogram renderer
   *
   * Owns the requestAnimationFrame loop. Reads frequency data from the
   * AnalyserNode each frame and renders a scrolling waterfall using the
   * Canvas 2D self-blit technique (drawImage shift + single column putImageData).
   *
   * Works entirely in device pixels (no ctx.scale) to avoid putImageData/DPR conflicts.
   */

  import {
    COLOR_MAPS,
    DEFAULT_COLOR_MAP,
    type ColorMapName,
  } from '$lib/utils/spectrogramColorMaps';
  import { t } from '$lib/i18n';
  import type { LiveSpectrogramColumn } from '$lib/types/liveSpectrogram';

  /** Interval between debug time markers on the waterfall (seconds) */
  const DEBUG_MARKER_INTERVAL_SEC = 5;

  interface Props {
    /** AnalyserNode to read frequency data from */
    analyser: AnalyserNode | null;
    /** Pre-allocated frequency data buffer */
    frequencyData: Uint8Array<ArrayBuffer>;
    /** Sample rate for bin-to-frequency mapping */
    sampleRate: number;
    /** FFT size for bin count calculation */
    fftSize: number;
    /** Display frequency range in Hz [min, max] */
    frequencyRange?: [number, number];
    /** Color map name */
    colorMap?: ColorMapName;
    /** Scroll speed in CSS pixels per second */
    scrollSpeed?: number;
    /** Whether the analyser is active (controls animation loop) */
    isActive?: boolean;
    /** Additional CSS classes for the container */
    className?: string;
    /** Detection labels to render on the spectrogram */
    overlayLabels?: Array<{
      text: string;
      birthTime: number;
      ySlot: number;
      firstDetected?: number;
      promotionDelta?: number;
    }>;
    /** Font size for overlay labels in CSS pixels (default: 11) */
    overlayFontSize?: number;
    /** Enable debug overlay: time markers + label timestamps */
    debug?: boolean;
    /** Current wall-clock time at playhead (Unix seconds) — used for debug time markers */
    wallClockAtPlayhead?: number;
    /** Rendering mode: analyser bins or server-streamed bins */
    renderMode?: 'analyser' | 'stream';
    /** Timestamped FFT columns streamed from the backend */
    streamColumns?: LiveSpectrogramColumn[];
    /** Hop size for streamed FFT columns */
    streamHopSize?: number;
    /** Whether to show a frequency axis on the left side */
    showFrequencyAxis?: boolean;
    /** Frequency axis density mode */
    frequencyAxisMode?: 'compact' | 'adaptive';
    /** Whether to show a time axis along the bottom edge */
    showTimeAxis?: boolean;
  }

  let {
    analyser,
    frequencyData,
    sampleRate,
    fftSize,
    frequencyRange = [0, 15000],
    colorMap = DEFAULT_COLOR_MAP,
    scrollSpeed = 60,
    isActive = false,
    className = '',
    overlayLabels = [],
    overlayFontSize = 11,
    debug = false,
    wallClockAtPlayhead = 0,
    renderMode = 'analyser',
    streamColumns = [],
    streamHopSize = 128,
    showFrequencyAxis = false,
    frequencyAxisMode = 'adaptive',
    showTimeAxis = false,
  }: Props = $props();

  let canvasEl: HTMLCanvasElement | undefined = $state();
  let overlayCanvasEl: HTMLCanvasElement | undefined = $state();
  let containerEl: HTMLDivElement | undefined = $state();
  // CSS pixel dimensions (from ResizeObserver)
  let cssWidth = $state(800);
  let cssHeight = $state(300);
  // Non-reactive FIFO queue of pending stream columns. Held as a plain array
  // with an integer head index so the animation loop can dequeue in O(1)
  // instead of calling queuedStreamColumns.slice(1) on every drained column.
  // Only the feed $effect and the animation loop touch these — neither path
  // needs Svelte reactivity on this buffer.
  let queuedStreamColumns: LiveSpectrogramColumn[] = [];
  let queuedStreamHead = 0;
  let lastQueuedStreamUnixMs = 0;
  const QUEUED_STREAM_COMPACT_THRESHOLD = 256;

  // DPR tracking
  let dpr = $state(globalThis.devicePixelRatio ?? 1);

  // Device pixel dimensions
  let deviceWidth = $derived(Math.round(cssWidth * dpr));
  let deviceHeight = $derived(Math.round(cssHeight * dpr));
  let frequencyAxisWidthCss = $derived.by(() => {
    if (!showFrequencyAxis) return 0;
    return frequencyAxisMode === 'compact' ? 42 : 50;
  });
  let frequencyAxisWidth = $derived(Math.round(frequencyAxisWidthCss * dpr));
  let plotOffsetX = $derived(showFrequencyAxis ? frequencyAxisWidth : 0);
  let timeAxisHeightCss = $derived(showTimeAxis ? 22 : 0);
  let timeAxisHeight = $derived(Math.round(timeAxisHeightCss * dpr));
  let plotHeight = $derived(Math.max(1, deviceHeight - timeAxisHeight));
  let plotWidth = $derived(Math.max(1, deviceWidth - plotOffsetX));

  // Precomputed bin-to-pixel mapping: maps each DEVICE pixel row to FFT bin index
  // y=0 is top of canvas = highest frequency
  let pixelToBinMap = $derived.by(() => {
    const binCount = fftSize / 2;
    const nyquist = sampleRate / 2;
    const [minFreq, maxFreq] = frequencyRange;
    const height = plotHeight;
    const map = new Uint16Array(height);

    for (let y = 0; y < height; y++) {
      // y=0 is top = highest frequency
      const freqRatio = 1 - y / (height - 1 || 1);
      const freq = minFreq + freqRatio * (maxFreq - minFreq);
      const binIndex = Math.round((freq / nyquist) * (binCount - 1));
      // eslint-disable-next-line security/detect-object-injection -- y is a loop counter bounded by canvas height
      map[y] = Math.max(0, Math.min(binCount - 1, binIndex));
    }

    return map;
  });

  // Selected color LUT
  // eslint-disable-next-line security/detect-object-injection -- colorMap is typed as ColorMapName
  let colorLUT = $derived(COLOR_MAPS[colorMap] ?? COLOR_MAPS[DEFAULT_COLOR_MAP]);
  const MAX_RENDER_READY_COLUMNS = 8192;

  function getFrequencyTickCount(): number {
    if (!showFrequencyAxis) return 0;
    if (frequencyAxisMode === 'compact') return 3;
    if (cssHeight < 260) return 3;
    if (cssHeight < 520) return 5;
    return 7;
  }

  function formatFrequencyLabel(hz: number): string {
    const khz = hz / 1000;
    const rounded = Math.round(khz * 10) / 10;
    return Number.isInteger(rounded) ? String(rounded) : rounded.toFixed(1);
  }

  function buildFrequencyTicks(count: number): Array<{ y: number; label: string }> {
    if (!showFrequencyAxis || count < 2 || plotHeight <= 0) return [];
    const [minFreq, maxFreq] = frequencyRange;
    const ticks: Array<{ y: number; label: string }> = [];
    for (let i = 0; i < count; i++) {
      const ratio = count === 1 ? 0 : i / (count - 1);
      const freq = minFreq + ratio * (maxFreq - minFreq);
      const y = plotHeight - ratio * plotHeight;
      ticks.push({
        y,
        label: formatFrequencyLabel(freq),
      });
    }
    return ticks;
  }

  function aggregateStreamColumns(
    columns: Array<number[] | Uint8Array<ArrayBuffer>>,
    binCount: number,
    start = 0,
    end: number = columns.length
  ): Uint8Array<ArrayBuffer> {
    const aggregated = new Uint8Array(binCount);
    const count = end - start;
    if (count <= 0) return aggregated;

    for (let bin = 0; bin < binCount; bin++) {
      let totalValue = 0;
      for (let i = start; i < end; i++) {
        /* eslint-disable security/detect-object-injection -- bin and i are bounded loop indices for dense FFT column arrays */
        const value = columns[i][bin] ?? 0;
        /* eslint-enable security/detect-object-injection */
        totalValue += value;
      }
      /* eslint-disable security/detect-object-injection -- bin is a bounded loop index for dense FFT column arrays */
      aggregated[bin] = Math.round(totalValue / count);
      /* eslint-enable security/detect-object-injection */
    }

    return aggregated;
  }

  $effect(() => {
    if (renderMode !== 'stream') {
      queuedStreamColumns.length = 0;
      queuedStreamHead = 0;
      lastQueuedStreamUnixMs = 0;
      return;
    }

    let appended = 0;
    for (const column of streamColumns) {
      if (column.tUnixMs > lastQueuedStreamUnixMs) {
        queuedStreamColumns.push(column);
        lastQueuedStreamUnixMs = column.tUnixMs;
        appended++;
      }
    }
    if (appended === 0) return;

    // Cap the queue to prevent unbounded growth if the animation loop
    // stops draining (e.g., tab backgrounded). Trim from the unread head.
    const live = queuedStreamColumns.length - queuedStreamHead;
    if (live > 4096) {
      queuedStreamHead += live - 4096;
    }

    // Periodically compact when the head has advanced far enough that
    // the prefix of already-consumed columns dominates the array.
    if (
      queuedStreamHead > QUEUED_STREAM_COMPACT_THRESHOLD &&
      queuedStreamHead * 2 > queuedStreamColumns.length
    ) {
      queuedStreamColumns = queuedStreamColumns.slice(queuedStreamHead);
      queuedStreamHead = 0;
    }
  });

  // ResizeObserver with debouncing (100ms)
  $effect(() => {
    if (!containerEl) return;

    let resizeTimer: ReturnType<typeof setTimeout>;
    const observer = new ResizeObserver(entries => {
      clearTimeout(resizeTimer);
      resizeTimer = setTimeout(() => {
        for (const entry of entries) {
          const { width, height } = entry.contentRect;
          if (width > 0 && height > 0) {
            cssWidth = Math.round(width);
            cssHeight = Math.round(height);
          }
        }
      }, 100);
    });

    observer.observe(containerEl);
    return () => {
      clearTimeout(resizeTimer);
      observer.disconnect();
    };
  });

  // Monitor DPR changes (e.g., dragging between displays)
  $effect(() => {
    const media = globalThis.matchMedia?.(`(resolution: ${dpr}dppx)`);
    if (!media) return;

    const handler = () => {
      dpr = globalThis.devicePixelRatio ?? 1;
    };

    media.addEventListener('change', handler);
    return () => media?.removeEventListener('change', handler);
  });

  // Update canvas buffer dimensions when size or DPR changes.
  // Snapshot existing content before resize, restore after (stretched to fit).
  // Setting canvas.width/height always clears the buffer — this preserves it.
  $effect(() => {
    if (!canvasEl) return;
    const newW = deviceWidth;
    const newH = deviceHeight;
    const oldW = canvasEl.width;
    const oldH = canvasEl.height;

    // Skip if dimensions haven't actually changed
    if (oldW === newW && oldH === newH) return;

    // Snapshot current content (only if canvas has visible content)
    if (oldW > 0 && oldH > 0) {
      try {
        // createImageBitmap is sync-ish for canvas sources in all modern browsers
        const tempCanvas = document.createElement('canvas');
        tempCanvas.width = oldW;
        tempCanvas.height = oldH;
        const tempCtx = tempCanvas.getContext('2d');
        if (tempCtx) {
          tempCtx.drawImage(canvasEl, 0, 0);
          // Resize the real canvas (clears it)
          canvasEl.width = newW;
          canvasEl.height = newH;
          // Draw snapshot back, stretched to new dimensions
          const ctx = canvasEl.getContext('2d');
          if (ctx) {
            ctx.drawImage(tempCanvas, 0, 0, oldW, oldH, 0, 0, newW, newH);
          }
          if (overlayCanvasEl) {
            overlayCanvasEl.width = newW;
            overlayCanvasEl.height = newH;
          }
          return;
        }
      } catch {
        // Fallback: just resize without preserving
      }
    }

    // Fallback: simple resize (clears canvas)
    canvasEl.width = newW;
    canvasEl.height = newH;
    if (overlayCanvasEl) {
      overlayCanvasEl.width = newW;
      overlayCanvasEl.height = newH;
    }
  });

  // Main animation loop
  $effect(() => {
    if (!isActive || !canvasEl) return;
    if (renderMode === 'analyser' && !analyser) return;

    const ctx = canvasEl.getContext('2d');
    if (!ctx) return;

    // Cache overlay canvas context outside the loop
    const olCtx = overlayCanvasEl?.getContext('2d') ?? null;
    const fontSize = overlayFontSize * dpr;

    // Ensure overlay canvas buffer dimensions match the waterfall canvas.
    // The resize $effect may not have fired yet when this effect starts
    // (overlayCanvasEl wasn't bound during the initial resize), leaving
    // the canvas buffer at 0x0 (invisible drawing).
    if (
      overlayCanvasEl &&
      (overlayCanvasEl.width !== canvasEl.width || overlayCanvasEl.height !== canvasEl.height)
    ) {
      overlayCanvasEl.width = canvasEl.width;
      overlayCanvasEl.height = canvasEl.height;
    }

    // Overlay font/style is set per-frame in the render loop (canvas state
    // can be lost on resize), so no initial setup needed here.

    let lastFrameTime = performance.now();
    let scrollAccumulator = 0;
    let frameId: number;
    // Track whether overlay was drawn last frame to avoid clearing an already-empty canvas
    let overlayHadContent = false;
    let lastStreamBins: number[] | Uint8Array<ArrayBuffer> | null = null;
    // Head-indexed FIFO of FFT columns ready to paint. Append via push(),
    // consume via head++ to avoid O(N) slice() calls per dequeue.
    let renderReadyColumns: Array<number[] | Uint8Array<ArrayBuffer>> = [];
    let renderReadyHead = 0;
    const RENDER_READY_COMPACT_THRESHOLD = 256;
    let streamColumnAccumulator = 0;
    let hasRenderedStreamColumns = false;
    let clearedStreamPrimingCanvas = false;
    let wasStreamPriming = false;
    const streamColumnsPerSecond =
      renderMode === 'stream' ? sampleRate / Math.max(1, streamHopSize) : 0;

    // Convert CSS px/s to device px/s
    const deviceScrollSpeed = scrollSpeed * dpr;

    // Debug overlay: cache formatted time strings to avoid toLocaleTimeString() per frame.
    // toLocaleTimeString invokes the Intl API which is expensive at 60fps.
    const timeFormatCache = new Map<number, string>();
    let lastCacheClearSec = 0;
    function formatTimeCached(unixSeconds: number): string {
      const intSec = Math.floor(unixSeconds);
      let str = timeFormatCache.get(intSec);
      if (str === undefined) {
        str = new Date(intSec * 1000).toLocaleTimeString('en-GB', {
          hour: '2-digit',
          minute: '2-digit',
          second: '2-digit',
        });
        timeFormatCache.set(intSec, str);
      }
      // Evict old entries every 30 seconds to prevent unbounded growth
      if (intSec - lastCacheClearSec > 30) {
        for (const key of timeFormatCache.keys()) {
          if (key < intSec - 120) timeFormatCache.delete(key);
        }
        lastCacheClearSec = intSec;
      }
      return str;
    }

    const loop = () => {
      const now = performance.now();
      const deltaTime = (now - lastFrameTime) / 1000;
      lastFrameTime = now;
      let dueStreamColumns: LiveSpectrogramColumn[] = [];
      let streamPriming = false;

      if (renderMode === 'analyser') {
        analyser?.getByteFrequencyData(frequencyData);
      } else if (queuedStreamColumns.length - queuedStreamHead > 0) {
        const playheadUnixMs = wallClockAtPlayhead > 0 ? Math.floor(wallClockAtPlayhead * 1000) : 0;

        // O(1) head-index dequeue — no slice() copies per column.
        /* eslint-disable security/detect-object-injection -- queuedStreamHead is a monotonic index into an internal FIFO */
        while (
          queuedStreamHead < queuedStreamColumns.length &&
          playheadUnixMs > 0 &&
          queuedStreamColumns[queuedStreamHead].tUnixMs <= playheadUnixMs
        ) {
          dueStreamColumns.push(queuedStreamColumns[queuedStreamHead]);
          queuedStreamHead++;
        }
        /* eslint-enable security/detect-object-injection */
        // Compact opportunistically once the consumed prefix is large
        // enough to make the one-shot copy worth it.
        if (
          queuedStreamHead > QUEUED_STREAM_COMPACT_THRESHOLD &&
          queuedStreamHead * 2 > queuedStreamColumns.length
        ) {
          queuedStreamColumns = queuedStreamColumns.slice(queuedStreamHead);
          queuedStreamHead = 0;
        }
        if (dueStreamColumns.length > 0) {
          const dueBins = dueStreamColumns.map(column => column.bins);
          renderReadyColumns.push(...dueBins);
          // Trim from the unread head instead of slicing — matches the
          // queuedStreamColumns head-index pattern below.
          const renderLive = renderReadyColumns.length - renderReadyHead;
          if (renderLive > MAX_RENDER_READY_COLUMNS) {
            renderReadyHead += renderLive - MAX_RENDER_READY_COLUMNS;
          }
          lastStreamBins = dueBins[dueBins.length - 1];
        }
      } else if (renderMode === 'stream' && wallClockAtPlayhead > 0) {
        streamPriming = true;
      }

      if (
        renderMode === 'stream' &&
        !hasRenderedStreamColumns &&
        dueStreamColumns.length === 0 &&
        renderReadyColumns.length === 0
      ) {
        streamPriming = true;
      }

      if (streamPriming && !clearedStreamPrimingCanvas) {
        ctx.clearRect(0, 0, deviceWidth, deviceHeight);
        clearedStreamPrimingCanvas = true;
      }

      if (showFrequencyAxis && plotOffsetX > 0) {
        ctx.fillStyle = '#000000';
        ctx.fillRect(0, 0, plotOffsetX, plotHeight);
      }
      if (showTimeAxis && timeAxisHeight > 0) {
        ctx.fillStyle = '#000000';
        ctx.fillRect(plotOffsetX, plotHeight, plotWidth, timeAxisHeight);
      }

      // Compute device pixels to scroll
      scrollAccumulator += deviceScrollSpeed * deltaTime;
      let pixelsToScroll = Math.floor(scrollAccumulator);
      if (streamPriming) {
        scrollAccumulator = 0;
        pixelsToScroll = 0;
      } else {
        if (wasStreamPriming) {
          scrollAccumulator = 0;
          streamColumnAccumulator = 0;
          pixelsToScroll = 0;
          if (!hasRenderedStreamColumns) {
            ctx.clearRect(0, 0, deviceWidth, deviceHeight);
          }
        }
        scrollAccumulator -= pixelsToScroll;
      }
      wasStreamPriming = streamPriming;

      if (pixelsToScroll > 0) {
        const w = plotWidth;
        const h = plotHeight;

        // Self-blit: shift existing content left (GPU-composited)
        if (hasRenderedStreamColumns || renderMode === 'analyser') {
          ctx.drawImage(
            canvasEl!,
            plotOffsetX + pixelsToScroll,
            0,
            Math.max(0, w - pixelsToScroll),
            h,
            plotOffsetX,
            0,
            Math.max(0, w - pixelsToScroll),
            h
          );
        }

        // Draw new column(s) at right edge using device pixel dimensions
        const imgData = ctx.createImageData(pixelsToScroll, h);
        const data = imgData.data;
        const currentBinMap = pixelToBinMap;
        const currentLUT = colorLUT;
        let streamPixels: Array<number[] | Uint8Array<ArrayBuffer>> = [];

        if (renderMode === 'stream') {
          streamColumnAccumulator += streamColumnsPerSecond * deltaTime;
          let columnsToConsume = Math.floor(streamColumnAccumulator);
          if (columnsToConsume > 0) {
            streamColumnAccumulator -= columnsToConsume;
          }

          const renderReadyAvailable = renderReadyColumns.length - renderReadyHead;
          if (renderReadyAvailable > 0 && columnsToConsume === 0) {
            columnsToConsume = 1;
          }

          if (columnsToConsume > renderReadyAvailable) {
            columnsToConsume = renderReadyAvailable;
          }

          if (columnsToConsume > 0) {
            if (!hasRenderedStreamColumns) {
              pixelsToScroll = Math.min(pixelsToScroll, Math.max(1, columnsToConsume));
            }
            // View slice against the live head; advance head without copying.
            const consumeStart = renderReadyHead;
            const consumeEnd = renderReadyHead + columnsToConsume;
            renderReadyHead = consumeEnd;
            hasRenderedStreamColumns = true;
            clearedStreamPrimingCanvas = false;
            const columnsPerPixel = columnsToConsume / pixelsToScroll;

            // In stream mode the bin count is dictated by the server's FFT
            // size (streamed via the spectrogram-meta event and forwarded
            // into the fftSize prop), not by frequencyData.length — which
            // is fixed at the client's AnalyserNode fftSize/2 and exists
            // only for the analyser-mode render path. If the server is
            // configured with fftSize != 1024 the two numbers differ and
            // aggregation would truncate the output, leaving the extra
            // bins undefined and producing black unrendered bands in the
            // waterfall (sentry-io flagged this on PR #2745).
            const streamBinCount = fftSize / 2;
            for (let col = 0; col < pixelsToScroll; col++) {
              const offsetStart = Math.floor(col * columnsPerPixel);
              const offsetEnd = Math.max(offsetStart + 1, Math.floor((col + 1) * columnsPerPixel));
              const sliceStart = consumeStart + offsetStart;
              const sliceEnd = Math.min(consumeStart + offsetEnd, consumeEnd);
              streamPixels.push(
                sliceEnd > sliceStart
                  ? aggregateStreamColumns(renderReadyColumns, streamBinCount, sliceStart, sliceEnd)
                  : (lastStreamBins ?? [])
              );
            }
          } else if (lastStreamBins) {
            for (let col = 0; col < pixelsToScroll; col++) {
              streamPixels.push(lastStreamBins);
            }
          }

          // Compact the ready-column buffer once the consumed prefix
          // dominates the backing array. Amortised O(1) per column since
          // compaction only runs at threshold * 2 intervals.
          if (
            renderReadyHead > RENDER_READY_COMPACT_THRESHOLD &&
            renderReadyHead * 2 > renderReadyColumns.length
          ) {
            renderReadyColumns = renderReadyColumns.slice(renderReadyHead);
            renderReadyHead = 0;
          }
        }

        for (let col = 0; col < pixelsToScroll; col++) {
          let columnBins: number[] | Uint8Array<ArrayBuffer> = frequencyData;
          if (renderMode === 'stream') {
            /* eslint-disable security/detect-object-injection -- col is a bounded loop index for the scroll buffer */
            columnBins = streamPixels[col] ?? lastStreamBins ?? [];
            /* eslint-enable security/detect-object-injection */
          }
          for (let y = 0; y < h; y++) {
            /* eslint-disable security/detect-object-injection -- loop indices and typed array lookups */
            const binIndex = currentBinMap[y];
            const magnitude = columnBins[binIndex] ?? 0;
            const rgba = currentLUT[magnitude];
            const offset = (y * pixelsToScroll + col) * 4;
            data[offset] = rgba & 0xff;
            data[offset + 1] = (rgba >>> 8) & 0xff;
            data[offset + 2] = (rgba >>> 16) & 0xff;
            data[offset + 3] = (rgba >>> 24) & 0xff;
            /* eslint-enable security/detect-object-injection */
          }
        }

        // putImageData works in raw device pixel coordinates (no transform needed)
        ctx.putImageData(imgData, plotOffsetX + w - pixelsToScroll, 0);
      }

      // --- Overlay: detection labels + debug time markers ---
      const hasOverlayContent =
        showFrequencyAxis || showTimeAxis || overlayLabels.length > 0 || debug || streamPriming;

      if (olCtx && hasOverlayContent) {
        olCtx.clearRect(0, 0, deviceWidth, deviceHeight);

        if (showFrequencyAxis && plotOffsetX > 0) {
          const axisFontSize = Math.max(
            9,
            Math.round((frequencyAxisMode === 'compact' ? 9 : 10) * dpr)
          );
          const ticks = buildFrequencyTicks(getFrequencyTickCount());

          olCtx.save();
          olCtx.shadowColor = 'transparent';
          olCtx.shadowBlur = 0;
          olCtx.strokeStyle = 'rgba(255, 255, 255, 0.26)';
          olCtx.fillStyle = 'rgba(255, 255, 255, 0.78)';
          olCtx.lineWidth = 1 * dpr;
          olCtx.font = `${axisFontSize}px sans-serif`;
          olCtx.textBaseline = 'middle';
          olCtx.textAlign = 'right';

          olCtx.beginPath();
          olCtx.moveTo(plotOffsetX - 0.5 * dpr, 0);
          olCtx.lineTo(plotOffsetX - 0.5 * dpr, plotHeight);
          olCtx.stroke();

          const topAxisPadding =
            frequencyAxisMode === 'compact' ? axisFontSize * 1.1 : axisFontSize;
          for (const tick of ticks) {
            const y = Math.max(topAxisPadding, Math.min(plotHeight - axisFontSize, tick.y));
            olCtx.beginPath();
            olCtx.moveTo(plotOffsetX - 6 * dpr, y);
            olCtx.lineTo(plotOffsetX - 1.5 * dpr, y);
            olCtx.stroke();
            olCtx.fillText(tick.label, plotOffsetX - 8 * dpr, y);
          }

          olCtx.textBaseline = 'top';
          olCtx.textAlign = 'left';
          olCtx.fillText('kHz', 4 * dpr, 4 * dpr);
          olCtx.restore();
        }

        // --- Debug time markers: vertical lines every 5s with HH:MM:SS ---
        if (debug && wallClockAtPlayhead > 0) {
          const debugFontSize = Math.round(9 * dpr);
          const visibleSeconds = plotWidth / deviceScrollSpeed;

          // Draw markers from right edge backwards
          // Right edge = wallClockAtPlayhead, left edge = wallClockAtPlayhead - visibleSeconds
          const rightEdgeTime = wallClockAtPlayhead;
          // Find the nearest 5-second boundary at or before the right edge
          const firstMarker =
            Math.floor(rightEdgeTime / DEBUG_MARKER_INTERVAL_SEC) * DEBUG_MARKER_INTERVAL_SEC;

          olCtx.save();
          olCtx.shadowColor = 'transparent';
          olCtx.shadowBlur = 0;
          olCtx.shadowOffsetX = 0;
          olCtx.shadowOffsetY = 0;

          for (
            let t = firstMarker;
            t > rightEdgeTime - visibleSeconds - DEBUG_MARKER_INTERVAL_SEC;
            t -= DEBUG_MARKER_INTERVAL_SEC
          ) {
            const ageSeconds = rightEdgeTime - t;
            const x = plotOffsetX + plotWidth - ageSeconds * deviceScrollSpeed;
            if (x < plotOffsetX || x > deviceWidth) continue;

            // Vertical dashed line
            olCtx.strokeStyle = 'rgba(255, 255, 0, 0.4)';
            olCtx.lineWidth = 1 * dpr;
            olCtx.setLineDash([4 * dpr, 4 * dpr]);
            olCtx.beginPath();
            olCtx.moveTo(x, 0);
            olCtx.lineTo(x, plotHeight);
            olCtx.stroke();
            olCtx.setLineDash([]);

            // Time label at bottom
            const timeStr = formatTimeCached(t);
            olCtx.font = `${debugFontSize}px monospace`;
            olCtx.fillStyle = 'rgba(255, 255, 0, 0.8)';
            olCtx.textBaseline = 'bottom';
            olCtx.fillText(timeStr, x + 2 * dpr, plotHeight - 2 * dpr);
          }

          // Debug info panel (top-left corner)
          olCtx.font = `${debugFontSize}px monospace`;
          olCtx.fillStyle = 'rgba(255, 255, 0, 0.9)';
          olCtx.textBaseline = 'top';
          const playheadStr = formatTimeCached(wallClockAtPlayhead);
          const nowStr = formatTimeCached(Date.now() / 1000);
          const hlsDelay = (Date.now() / 1000 - wallClockAtPlayhead).toFixed(1);
          olCtx.fillText(`playhead: ${playheadStr}`, plotOffsetX + 4 * dpr, 4 * dpr);
          olCtx.fillText(
            `wall: ${nowStr}  HLS lag: ${hlsDelay}s`,
            plotOffsetX + 4 * dpr,
            4 * dpr + debugFontSize + 2 * dpr
          );
          olCtx.fillText(
            `queue: ${overlayLabels.length} labels`,
            plotOffsetX + 4 * dpr,
            4 * dpr + (debugFontSize + 2 * dpr) * 2
          );

          olCtx.restore();
        }

        if (streamPriming) {
          const primingFontSize = Math.max(10, Math.round(10 * dpr));
          olCtx.save();
          olCtx.shadowColor = 'transparent';
          olCtx.shadowBlur = 0;
          olCtx.fillStyle = 'rgba(255, 255, 255, 0.72)';
          olCtx.font = `${primingFontSize}px sans-serif`;
          olCtx.textBaseline = 'middle';
          olCtx.textAlign = 'center';
          olCtx.fillText(
            t('spectrogram.page.syncingAudio'),
            plotOffsetX + plotWidth / 2,
            plotHeight / 2
          );
          olCtx.restore();
        }

        if (showTimeAxis && wallClockAtPlayhead > 0) {
          const axisFontSize = Math.max(9, Math.round(10 * dpr));
          const visibleSeconds = plotWidth / deviceScrollSpeed;
          const leftEdgeTime = wallClockAtPlayhead - visibleSeconds;
          const midpointTime = leftEdgeTime + visibleSeconds / 2;
          const timeLabels = [
            { x: plotOffsetX + 8 * dpr, time: leftEdgeTime, align: 'left' as const },
            { x: plotOffsetX + plotWidth / 2, time: midpointTime, align: 'center' as const },
            {
              x: plotOffsetX + plotWidth - 8 * dpr,
              time: wallClockAtPlayhead,
              align: 'right' as const,
            },
          ];

          olCtx.save();
          olCtx.shadowColor = 'transparent';
          olCtx.shadowBlur = 0;
          olCtx.strokeStyle = 'rgba(255, 255, 255, 0.22)';
          olCtx.fillStyle = 'rgba(255, 255, 255, 0.78)';
          olCtx.lineWidth = 1 * dpr;
          olCtx.font = `${axisFontSize}px sans-serif`;
          olCtx.textBaseline = 'middle';
          olCtx.textAlign = 'center';

          olCtx.beginPath();
          olCtx.moveTo(plotOffsetX, plotHeight + 0.5 * dpr);
          olCtx.lineTo(plotOffsetX + plotWidth, plotHeight + 0.5 * dpr);
          olCtx.stroke();

          for (const label of timeLabels) {
            olCtx.textAlign = label.align;
            olCtx.beginPath();
            olCtx.moveTo(label.x, plotHeight + 1.5 * dpr);
            olCtx.lineTo(label.x, plotHeight + 6 * dpr);
            olCtx.stroke();
            olCtx.fillText(
              formatTimeCached(label.time),
              label.x,
              plotHeight + timeAxisHeight / 2 + 1 * dpr
            );
          }

          olCtx.restore();
        }

        // --- Detection labels ---
        if (overlayLabels.length > 0) {
          // Re-apply styles every frame (canvas state can be lost on resize)
          olCtx.font = `bold ${fontSize}px sans-serif`;
          olCtx.fillStyle = '#ffffff';
          olCtx.shadowColor = 'rgba(0, 0, 0, 0.8)';
          olCtx.shadowBlur = 3 * dpr;
          olCtx.shadowOffsetX = 1 * dpr;
          olCtx.shadowOffsetY = 1 * dpr;
          olCtx.textBaseline = 'middle';

          const maxSlots = Math.max(2, Math.floor(plotHeight / (fontSize * 2.5)));

          for (const label of overlayLabels) {
            const labelAge = (now - label.birthTime) / 1000;
            const x = plotOffsetX + plotWidth - labelAge * deviceScrollSpeed;

            if (x < plotOffsetX - 200 * dpr || x > deviceWidth) continue;

            const slotHeight = plotHeight / (maxSlots + 1);
            const y = slotHeight * (1 + (label.ySlot % maxSlots));

            if (debug && label.firstDetected) {
              // Debug: show species name + firstDetected time + frozen promotion offset
              const detStr = formatTimeCached(label.firstDetected);
              const delta = label.promotionDelta?.toFixed(1) ?? '?';
              olCtx.fillText(`${label.text} [${detStr} \u0394${delta}s]`, x, y);
            } else {
              olCtx.fillText(label.text, x, y);
            }
          }
        }
        overlayHadContent = true;
      } else if (olCtx && overlayHadContent) {
        // Only clear overlay when transitioning from content to empty
        olCtx.clearRect(0, 0, deviceWidth, deviceHeight);
        overlayHadContent = false;
      }

      frameId = requestAnimationFrame(loop);
    };

    frameId = requestAnimationFrame(loop);
    return () => cancelAnimationFrame(frameId);
  });
</script>

<div bind:this={containerEl} class="relative overflow-hidden bg-black {className}">
  <canvas bind:this={canvasEl} style:width="100%" style:height="100%"></canvas>
  <canvas
    bind:this={overlayCanvasEl}
    style:width="100%"
    style:height="100%"
    class="pointer-events-none absolute inset-0 z-10"
  ></canvas>
</div>
