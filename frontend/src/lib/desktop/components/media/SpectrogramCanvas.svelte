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
  }: Props = $props();

  let canvasEl: HTMLCanvasElement | undefined = $state();
  let containerEl: HTMLDivElement | undefined = $state();
  // CSS pixel dimensions (from ResizeObserver)
  let cssWidth = $state(800);
  let cssHeight = $state(300);

  // Timing exposure for future DetectionOverlay
  let startTime = $state(0);

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
      map[y] = Math.max(0, Math.min(binCount - 1, binIndex));
    }

    return map;
  });

  // Selected color LUT
  let colorLUT = $derived(COLOR_MAPS[colorMap] ?? COLOR_MAPS[DEFAULT_COLOR_MAP]);

  // Internal timestampToX for future use (not exported — Svelte 5 limitation)
  function timestampToX(eventTime: number): number {
    const elapsed = (performance.now() - startTime) / 1000;
    const eventAge = elapsed - (eventTime - startTime / 1000);
    const x = cssWidth - eventAge * scrollSpeed;
    return x >= 0 ? x : -1;
  }

  // Suppress unused function warning — timestampToX will be used by DetectionOverlay
  void timestampToX;

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

  // Update canvas buffer dimensions when size or DPR changes
  $effect(() => {
    if (!canvasEl) return;
    // Size canvas buffer to device pixels
    canvasEl.width = deviceWidth;
    canvasEl.height = deviceHeight;
    // NO ctx.scale() — we work entirely in device pixels
  });

  // Main animation loop
  $effect(() => {
    if (!analyser || !isActive || !canvasEl) return;

    const ctx = canvasEl.getContext('2d');
    if (!ctx) return;

    startTime = performance.now();
    let lastFrameTime = performance.now();
    let scrollAccumulator = 0;
    let frameId: number;

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
            const binIndex = currentBinMap[y];
            const magnitude = frequencyData[binIndex];
            data[y * pixelsToScroll + col] = currentLUT[magnitude];
          }
        }

        // putImageData works in raw device pixel coordinates (no transform needed)
        ctx.putImageData(imgData, w - pixelsToScroll, 0);
      }

      frameId = requestAnimationFrame(loop);
    };

    frameId = requestAnimationFrame(loop);
    return () => cancelAnimationFrame(frameId);
  });
</script>

<div bind:this={containerEl} class="relative overflow-hidden bg-black {className}">
  <canvas bind:this={canvasEl} style:width="100%" style:height="100%"></canvas>
</div>
