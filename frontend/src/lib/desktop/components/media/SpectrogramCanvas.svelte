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
  import { loggers } from '$lib/utils/logger';

  const logger = loggers.audio;

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
    overlayLabels?: Array<{ text: string; birthTime: number; ySlot: number }>;
    /** Font size for overlay labels in CSS pixels (default: 11) */
    overlayFontSize?: number;
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
  }: Props = $props();

  let canvasEl: HTMLCanvasElement | undefined = $state();
  let overlayCanvasEl: HTMLCanvasElement | undefined = $state();
  let containerEl: HTMLDivElement | undefined = $state();
  // CSS pixel dimensions (from ResizeObserver)
  let cssWidth = $state(800);
  let cssHeight = $state(300);

  // DPR tracking
  let dpr = $state(globalThis.devicePixelRatio ?? 1);

  // Device pixel dimensions
  let deviceWidth = $derived(Math.round(cssWidth * dpr));
  let deviceHeight = $derived(Math.round(cssHeight * dpr));

  // Precomputed bin-to-pixel mapping: maps each DEVICE pixel row to FFT bin index
  // y=0 is top of canvas = highest frequency
  let pixelToBinMap = $derived.by(() => {
    const binCount = fftSize / 2;
    const nyquist = sampleRate / 2;
    const [minFreq, maxFreq] = frequencyRange;
    const height = deviceHeight;
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
    return () => media.removeEventListener('change', handler);
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
          logger.debug('Canvas resized with content preserved', { oldW, oldH, newW, newH });
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
    if (!analyser || !isActive || !canvasEl) return;

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

    // Convert CSS px/s to device px/s
    const deviceScrollSpeed = scrollSpeed * dpr;

    const loop = () => {
      const now = performance.now();
      const deltaTime = (now - lastFrameTime) / 1000;
      lastFrameTime = now;

      // Read frequency data from analyser
      analyser.getByteFrequencyData(frequencyData);

      // Compute device pixels to scroll
      scrollAccumulator += deviceScrollSpeed * deltaTime;
      const pixelsToScroll = Math.floor(scrollAccumulator);
      scrollAccumulator -= pixelsToScroll;

      if (pixelsToScroll > 0) {
        const w = deviceWidth;
        const h = deviceHeight;

        // Self-blit: shift existing content left (GPU-composited)
        ctx.drawImage(canvasEl!, -pixelsToScroll, 0);

        // Draw new column(s) at right edge using device pixel dimensions
        const imgData = ctx.createImageData(pixelsToScroll, h);
        const data = new Uint32Array(imgData.data.buffer);
        const currentBinMap = pixelToBinMap;
        const currentLUT = colorLUT;

        for (let col = 0; col < pixelsToScroll; col++) {
          for (let y = 0; y < h; y++) {
            /* eslint-disable security/detect-object-injection -- loop indices and typed array lookups */
            const binIndex = currentBinMap[y];
            const magnitude = frequencyData[binIndex];
            data[y * pixelsToScroll + col] = currentLUT[magnitude];
            /* eslint-enable security/detect-object-injection */
          }
        }

        // putImageData works in raw device pixel coordinates (no transform needed)
        ctx.putImageData(imgData, w - pixelsToScroll, 0);
      }

      // --- Overlay detection labels on separate canvas (avoids self-blit smearing) ---
      if (olCtx && overlayLabels.length > 0) {
        olCtx.clearRect(0, 0, deviceWidth, deviceHeight);

        // Re-apply styles every frame (canvas state can be lost on resize)
        olCtx.font = `bold ${fontSize}px sans-serif`;
        olCtx.fillStyle = '#ffffff';
        olCtx.shadowColor = 'rgba(0, 0, 0, 0.8)';
        olCtx.shadowBlur = 3 * dpr;
        olCtx.shadowOffsetX = 1 * dpr;
        olCtx.shadowOffsetY = 1 * dpr;
        olCtx.textBaseline = 'middle';

        const maxSlots = Math.max(2, Math.floor(deviceHeight / (fontSize * 2.5)));

        for (const label of overlayLabels) {
          const labelAge = (now - label.birthTime) / 1000;
          const x = deviceWidth - labelAge * deviceScrollSpeed;

          if (x < -200 * dpr || x > deviceWidth) continue;

          const slotHeight = deviceHeight / (maxSlots + 1);
          const y = slotHeight * (1 + (label.ySlot % maxSlots));

          olCtx.fillText(label.text, x, y);
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
