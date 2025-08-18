<!--
  Filter Response Graph Component
  
  Purpose: Visualizes frequency response curves for audio filters
  
  Features:
  - Real-time frequency response visualization
  - Modified logarithmic frequency scale optimized for high frequencies (20Hz - 20kHz)
  - Gain display (-48dB to +12dB)
  - Interactive tooltips on hover
  - Combined response curve (single line representing filter chain)
  - Shows flat 0dB response when no filters are applied
  - Responsive width with professional margins
  
  Props:
  - filters: Array of filter configurations (can be empty)
  - height: Canvas height (optional, defaults to 400px)
  
  Width is auto-calculated based on container size for responsive behavior.
  
  @component
-->
<script lang="ts">
  import { onMount } from 'svelte';
  interface Filter {
    type: string;
    frequency: number;
    q?: number;
    width?: number;
    passes?: number;
    [key: string]: any;
  }

  interface Props {
    filters: Filter[];
    height?: number;
  }

  // Make responsive to container width, default to reasonable size
  let { filters = [], height = 400 }: Props = $props();

  // Container element for measuring width
  let containerElement: HTMLDivElement;

  // Canvas element reference
  let canvas: HTMLCanvasElement;
  let tooltip = $state({ visible: false, x: 0, y: 0, freq: 0, gain: 0 });

  // Performance optimization: Cache filter coefficients
  let filterCoefficientsCache = new Map<
    string,
    { b0: number; b1: number; b2: number; a1: number; a2: number }
  >();
  let lastFilterState = '';

  // Performance optimization: Removed sinh cache (no longer needed with standard Q calculation)

  // Performance optimization: Cache entire frequency response curves
  let responseCache = new Map<string, { response: Float32Array; xPositions: Float32Array }>();
  let lastResponseCacheKey = '';

  // Performance optimization: Cache individual filter responses per frequency
  let filterResponseCache = new Map<string, Map<number, number>>();

  // Performance optimization: Precompute trig values for common frequencies
  let trigCache = new Map<number, { cos: number; sin: number; cos2: number; sin2: number }>();

  // Performance optimization: Use requestAnimationFrame
  let animationFrameId: number | null = null;
  let resizeDebounceTimer: number | null = null;
  let qualityUpgradeTimer: number | null = null;

  // PERFORMANCE: Track if we should use reduced quality for real-time updates
  let useReducedQuality = $state(false);
  let lastInteractionTime = 0;
  let isResizing = $state(false);

  // Professional margins for proper label spacing
  const margins = {
    top: 40,
    right: 60,
    bottom: 100, // More space for frequency labels and axis title
    left: 100, // Much more space for dB labels
  };

  // Responsive canvas dimensions
  let canvasWidth = $state(800); // Default fallback
  let canvasHeight = $state(height); // Make reactive since we update it

  // Plot area dimensions (excluding margins) - now reactive
  let plotWidth = $derived(canvasWidth - margins.left - margins.right);
  let plotHeight = $derived(canvasHeight - margins.top - margins.bottom);

  // Frequency range - standard audio range up to 20kHz (human hearing limit)
  const MIN_FREQ = 20;
  const MAX_FREQ = 20000;
  const MIN_DB = -48;
  const MAX_DB = 12;

  // Grid lines optimized for audio engineering work - more detail in bird frequency range
  const freqGridLines = [
    20, 50, 100, 200, 500, 1000, 2000, 3000, 4000, 5000, 6000, 8000, 10000, 12000, 15000, 20000,
  ];
  const dbGridLines = [-48, -36, -24, -12, 0, 12];

  // Color scheme - reactive state for proper Svelte 5 updates
  let colors = $state({
    background: 'hsl(222, 30%, 15%)', //
    grid: 'hsl(215, 16%, 35%)', // Light blue-grey grid lines
    text: 'hsl(218, 11%, 87%)', // White/light text
    reference: 'hsl(0, 0%, 55%)', // Light reference line
    primary: 'hsl(204, 70%, 63%)', // Bright blue frequency curve
  });

  // Initialize colors - no need to update since we use same colors for both themes
  function updateColors() {
    // Colors are already set in $state above and don't change based on theme
    // This function kept for compatibility with existing effect calls
  }

  // Get cached filter coefficients or calculate them
  function getFilterCoefficients(filter: Filter) {
    const cacheKey = `${filter.type}-${filter.frequency}-${filter.q ?? 0}-${filter.width ?? 0}-${filter.passes ?? 0}`;

    // Check cache first
    if (filterCoefficientsCache.has(cacheKey)) {
      return filterCoefficientsCache.get(cacheKey)!;
    }

    const sampleRate = 48000; // 48kHz sample rate

    // For HP/LP filters, always use Butterworth response (Q=0.707)
    // For band-pass/band-stop, use the specified Q factor or width
    let q = 0.707; // Default to Butterworth
    let alpha = 0;

    if (filter.type === 'BandReject' && filter.width) {
      // For BandReject, use standard Q = fc / bandwidth formula
      // This gives correct attenuation matching the passes setting
      const centerFreq = filter.frequency;
      const bandwidth = Math.max(1, Math.min(filter.width, centerFreq * 1.9)); // Ensure reasonable bandwidth

      // Standard bandreject Q calculation: Q = center_frequency / bandwidth_in_Hz
      q = centerFreq / bandwidth;

      // Clamp Q to reasonable range to avoid instability
      q = Math.max(0.5, Math.min(50, q));
    } else if (
      filter.type === 'BandPass' ||
      filter.type === 'BandStop' ||
      filter.type === 'Notch'
    ) {
      q = Math.max(0.1, Math.min(10, filter.q || 0.707));
    } else if (filter.type === 'HighPass' || filter.type === 'LowPass') {
      // Force Butterworth response for HP/LP filters
      q = 0.707;
    }

    // Calculate filter coefficients using Robert Bristow-Johnson's cookbook formulas
    const fc = filter.frequency;
    const omega = (2 * Math.PI * fc) / sampleRate;
    const sin_omega = Math.sin(omega);
    const cos_omega = Math.cos(omega);

    // Calculate alpha from Q for all filter types (BandReject now uses standard Q)
    alpha = sin_omega / (2 * q);

    let b0 = 0,
      b1 = 0,
      b2 = 0;
    let a0 = 1,
      a1 = 0,
      a2 = 0;

    if (filter.type === 'LowPass') {
      // Low-pass filter coefficients
      b0 = (1 - cos_omega) / 2;
      b1 = 1 - cos_omega;
      b2 = (1 - cos_omega) / 2;
      a0 = 1 + alpha;
      a1 = -2 * cos_omega;
      a2 = 1 - alpha;
    } else if (filter.type === 'HighPass') {
      // High-pass filter coefficients
      b0 = (1 + cos_omega) / 2;
      b1 = -(1 + cos_omega);
      b2 = (1 + cos_omega) / 2;
      a0 = 1 + alpha;
      a1 = -2 * cos_omega;
      a2 = 1 - alpha;
    } else if (filter.type === 'BandPass') {
      // Band-pass filter coefficients (constant 0 dB peak gain)
      b0 = alpha;
      b1 = 0;
      b2 = -alpha;
      a0 = 1 + alpha;
      a1 = -2 * cos_omega;
      a2 = 1 - alpha;
    } else if (
      filter.type === 'BandStop' ||
      filter.type === 'Notch' ||
      filter.type === 'BandReject'
    ) {
      // Band-stop/notch/band-reject filter coefficients
      b0 = 1;
      b1 = -2 * cos_omega;
      b2 = 1;
      a0 = 1 + alpha;
      a1 = -2 * cos_omega;
      a2 = 1 - alpha;
    } else {
      // Unknown filter type - return unity coefficients
      const coeffs = { b0: 1, b1: 0, b2: 0, a1: 0, a2: 0 };
      filterCoefficientsCache.set(cacheKey, coeffs);
      return coeffs;
    }

    // Normalize coefficients
    b0 /= a0;
    b1 /= a0;
    b2 /= a0;
    a1 /= a0;
    a2 /= a0;

    // Cache the coefficients
    const coeffs = { b0, b1, b2, a1, a2 };
    filterCoefficientsCache.set(cacheKey, coeffs);

    return coeffs;
  }

  // Calculate frequency response for a filter using cached coefficients
  function calculateFilterResponse(filter: Filter, frequency: number): number {
    const sampleRate = 48000; // 48kHz sample rate
    const passes = filter.passes || 0;

    // If no passes (0dB attenuation), return flat response
    if (passes === 0) {
      return 0;
    }

    // PERFORMANCE: Check filter-specific response cache first
    const filterCacheKey = `${filter.type}-${filter.frequency}-${filter.q ?? 0}-${filter.width ?? 0}-${passes}`;
    let freqCache = filterResponseCache.get(filterCacheKey);
    if (!freqCache) {
      freqCache = new Map<number, number>();
      filterResponseCache.set(filterCacheKey, freqCache);
    }

    const cachedResponse = freqCache.get(frequency);
    if (cachedResponse !== undefined) {
      return cachedResponse;
    }

    // Get cached coefficients
    const { b0, b1, b2, a1, a2 } = getFilterCoefficients(filter);

    // Calculate frequency response at the given frequency
    // H(e^jω) = (b0 + b1*e^-jω + b2*e^-j2ω) / (1 + a1*e^-jω + a2*e^-j2ω)
    const w = (2 * Math.PI * frequency) / sampleRate;

    // PERFORMANCE: Use cached trig values
    let trigValues = trigCache.get(w);
    if (!trigValues) {
      const cos_w = Math.cos(w);
      const sin_w = Math.sin(w);
      trigValues = {
        cos: cos_w,
        sin: sin_w,
        cos2: 2 * cos_w * cos_w - 1, // cos(2w) = 2cos²(w) - 1
        sin2: 2 * sin_w * cos_w, // sin(2w) = 2sin(w)cos(w)
      };
      // Limit trig cache size
      if (trigCache.size > 500) {
        const firstKey = trigCache.keys().next();
        if (!firstKey.done && firstKey.value !== undefined) {
          trigCache.delete(firstKey.value);
        }
      }
      trigCache.set(w, trigValues);
    }

    const { cos: cos_w, sin: sin_w, cos2: cos_2w, sin2: sin_2w } = trigValues;

    // Complex numerator: b0 + b1*e^-jω + b2*e^-j2ω
    // e^-jω = cos(ω) - j*sin(ω)
    const num_real = b0 + b1 * cos_w + b2 * cos_2w;
    const num_imag = -b1 * sin_w - b2 * sin_2w;

    // Complex denominator: 1 + a1*e^-jω + a2*e^-j2ω
    const den_real = 1 + a1 * cos_w + a2 * cos_2w;
    const den_imag = -a1 * sin_w - a2 * sin_2w;

    // Calculate magnitude |H| = |numerator| / |denominator|
    // PERFORMANCE: Use hypot for better numerical stability and potential SIMD optimization
    const num_magnitude = Math.hypot(num_real, num_imag);
    const den_magnitude = Math.hypot(den_real, den_imag);

    // Avoid division by zero and ensure stability
    let magnitude = num_magnitude / Math.max(1e-10, den_magnitude);

    // For high-pass filters, ensure the response doesn't exceed unity gain at high frequencies
    // This is a physical constraint - passive filters can't amplify
    if (filter.type === 'HighPass') {
      // At frequencies much higher than cutoff, response should approach 1 (0 dB)
      const freq_ratio = frequency / filter.frequency;
      if (freq_ratio > 10) {
        // Far above cutoff, response should be very close to 1
        magnitude = Math.min(magnitude, 1.0);
      }
    }

    // Apply cascaded filter response for multiple passes
    // Each pass is a 2nd order filter (biquad)
    const cascaded_magnitude = Math.pow(magnitude, passes);

    // Convert to dB
    const db = 20 * Math.log10(Math.max(1e-10, cascaded_magnitude));

    // Clamp to reasonable range
    const result = Math.max(-96, Math.min(12, db));

    // PERFORMANCE: Cache the result for this filter and frequency
    freqCache.set(frequency, result);

    // Limit per-filter cache size
    if (freqCache.size > 200) {
      const firstKey = freqCache.keys().next();
      if (!firstKey.done && firstKey.value !== undefined) {
        freqCache.delete(firstKey.value);
      }
    }

    return result;
  }

  // Calculate combined response of all filters (returns 0dB flat response when no filters)
  function calculateCombinedResponse(frequency: number): number {
    let totalGain = 0;
    for (const filter of filters) {
      const filterGain = calculateFilterResponse(filter, frequency);

      // Skip this filter if it returns NaN or Infinity
      if (!isFinite(filterGain)) {
        continue;
      }

      totalGain += filterGain;
    }

    // Return flat response if calculation fails
    if (!isFinite(totalGain)) {
      return 0;
    }

    return Math.max(MIN_DB, Math.min(MAX_DB, totalGain));
  }

  // Convert frequency to x position using modified log scale optimized for audio work
  function freqToX(freq: number): number {
    // Modified logarithmic scale that gives more visual space to high frequencies
    // This is similar to what professional audio software uses

    if (freq <= 1000) {
      // Standard log scale for low frequencies (20Hz - 1kHz)
      const logMin = Math.log10(MIN_FREQ);
      const log1k = Math.log10(1000);
      const logFreq = Math.log10(freq);
      const lowEndPortion = 0.3; // 30% of the width for 20Hz-1kHz
      return margins.left + ((logFreq - logMin) / (log1k - logMin)) * plotWidth * lowEndPortion;
    } else {
      // Modified scale for high frequencies (1kHz - 20kHz) with more visual space
      const highFreqStart = 1000;
      const highFreqRange = MAX_FREQ - highFreqStart;
      const freqInHighRange = freq - highFreqStart;

      // Use a gentler logarithmic curve for high frequencies
      const normalizedHighFreq =
        Math.log10(1 + (freqInHighRange / highFreqRange) * 9) / Math.log10(10);

      const lowEndPortion = 0.3;
      const highEndStart = margins.left + plotWidth * lowEndPortion;
      const highEndWidth = plotWidth * (1 - lowEndPortion);

      return highEndStart + normalizedHighFreq * highEndWidth;
    }
  }

  // Convert x position to frequency (inverse of freqToX)
  function xToFreq(x: number): number {
    const plotX = x - margins.left;
    const lowEndPortion = 0.3;
    const lowEndWidth = plotWidth * lowEndPortion;

    if (plotX <= lowEndWidth) {
      // Low frequency range (20Hz - 1kHz)
      const logMin = Math.log10(MIN_FREQ);
      const log1k = Math.log10(1000);
      const ratio = plotX / lowEndWidth;
      const logFreq = logMin + ratio * (log1k - logMin);
      return Math.pow(10, logFreq);
    } else {
      // High frequency range (1kHz - 20kHz)
      const highEndWidth = plotWidth * (1 - lowEndPortion);
      const highRatio = (plotX - lowEndWidth) / highEndWidth;

      // Inverse of the modified high-frequency scale
      const normalizedVal = highRatio;
      const logVal = normalizedVal * Math.log10(10);
      const scaledVal = Math.pow(10, logVal) - 1;
      const freqInHighRange = (scaledVal / 9) * (MAX_FREQ - 1000);

      return 1000 + freqInHighRange;
    }
  }

  // Convert dB to y position within plot area
  function dbToY(db: number): number {
    return margins.top + plotHeight - ((db - MIN_DB) / (MAX_DB - MIN_DB)) * plotHeight;
  }

  // Update canvas dimensions based on container size with proper DPR handling
  function updateCanvasDimensions(immediate = false) {
    if (containerElement && canvas) {
      const containerWidth = containerElement.clientWidth;
      // Use most of the container width while maintaining reasonable limits
      const newCanvasWidth = Math.min(Math.max(containerWidth * 0.95, 600), 1200);
      const newCanvasHeight = height;

      // Get device pixel ratio for high-DPI displays
      const dpr = window.devicePixelRatio || 1;

      // Set the internal canvas size (accounting for device pixel ratio)
      const internalWidth = Math.round(newCanvasWidth * dpr);
      const internalHeight = Math.round(newCanvasHeight * dpr);

      // Only update if dimensions actually changed to avoid unnecessary redraws
      if (
        canvas.width !== internalWidth ||
        canvas.height !== internalHeight ||
        canvasWidth !== newCanvasWidth ||
        canvasHeight !== newCanvasHeight
      ) {
        // Update internal canvas dimensions
        canvas.width = internalWidth;
        canvas.height = internalHeight;

        // Scale the context to match device pixel ratio
        const ctx = canvas.getContext('2d');
        if (ctx) {
          ctx.scale(dpr, dpr);
        }

        // Set CSS dimensions (what the user sees)
        canvas.style.width = `${newCanvasWidth}px`;
        canvas.style.height = `${newCanvasHeight}px`;

        // Update reactive state
        canvasWidth = newCanvasWidth;
        canvasHeight = newCanvasHeight;

        // PERFORMANCE: Only clear coordinate-dependent caches
        // Don't clear expensive filter caches during active resize
        if (!isResizing || immediate) {
          responseCache.clear(); // Always clear this since coordinates changed
          if (immediate) {
            // Only clear filter caches on final resize, not during active resize
            filterCoefficientsCache.clear();
            filterResponseCache.clear();
          }
        } else {
          // During resize, only clear response cache
          responseCache.clear();
        }
      }
    }
  }

  // Schedule graph drawing with requestAnimationFrame
  function scheduleDrawGraph(immediate = false) {
    // Cancel any pending animation frame
    if (animationFrameId !== null) {
      window.cancelAnimationFrame(animationFrameId);
    }

    // PERFORMANCE: Track interaction timing for quality adjustment
    const now = Date.now();
    if (immediate) {
      lastInteractionTime = now;
      useReducedQuality = true;
    } else if (now - lastInteractionTime > 500) {
      useReducedQuality = false;
    }

    // Schedule new draw
    animationFrameId = window.requestAnimationFrame(() => {
      drawGraph();
      animationFrameId = null;

      // Reset quality after draw if interaction is done
      if (useReducedQuality && Date.now() - lastInteractionTime > 300) {
        useReducedQuality = false;
        scheduleDrawGraph(); // Redraw with full quality
      }
    });
  }

  // Create efficient cache key without JSON.stringify
  function createCacheKey(filters: Filter[], quality?: string): string {
    if (filters.length === 0) return quality ? `empty:${quality}` : 'empty';
    const filterKey = filters
      .map(f => `${f.type}:${f.frequency}:${f.q ?? 0}:${f.width ?? 0}:${f.passes ?? 0}`)
      .join('|');
    return quality ? `${filterKey}:${quality}` : filterKey;
  }

  // Compute entire frequency response curve with caching
  function computeResponseCurve(): { response: Float32Array; xPositions: Float32Array } {
    // PERFORMANCE: Use efficient cache key generation with quality
    const quality = isResizing ? 'resize' : useReducedQuality ? 'low' : 'high';
    const cacheKey = createCacheKey(filters, quality);

    // Return cached response if available
    if (cacheKey === lastResponseCacheKey && responseCache.has(cacheKey)) {
      return responseCache.get(cacheKey)!;
    }

    // SMART SAMPLING: Higher resolution with intelligent frequency distribution
    // Ensure sufficient resolution for smooth curves
    let minSteps, maxSteps;

    if (isResizing) {
      // Extra-low quality during active resize for maximum responsiveness
      minSteps = 150;
      maxSteps = 300;
    } else if (useReducedQuality) {
      // Normal reduced quality for interactions
      minSteps = 400;
      maxSteps = 600;
    } else {
      // Full quality for final render
      minSteps = 800;
      maxSteps = 1200;
    }

    // Collect critical frequencies that need dense sampling
    const criticalFreqs: number[] = [];

    // Skip critical frequency sampling during resize for performance
    if (!isResizing) {
      for (const filter of filters) {
        // Add filter center frequency
        criticalFreqs.push(filter.frequency);

        if (filter.type === 'BandReject' && filter.width) {
          // For band reject, add points around the notch for smooth transition
          const halfWidth = filter.width / 2;
          // Sample densely around the notch edges
          const factors = useReducedQuality ? [0.7, 1.0, 1.3] : [0.5, 0.7, 0.9, 1.0, 1.1, 1.3, 1.5];
          for (let factor of factors) {
            criticalFreqs.push(Math.max(20, filter.frequency - halfWidth * factor));
            criticalFreqs.push(Math.min(20000, filter.frequency + halfWidth * factor));
          }
        } else if (filter.type === 'HighPass' || filter.type === 'LowPass') {
          // For HP/LP, sample around cutoff frequency
          const factors = useReducedQuality
            ? [0.5, 1.0, 2.0]
            : [0.25, 0.5, 0.7, 0.9, 1.1, 1.4, 2.0, 4.0];
          for (let factor of factors) {
            const freq = filter.frequency * factor;
            if (freq >= 20 && freq <= 20000) {
              criticalFreqs.push(freq);
            }
          }
        }
      }
    }

    // Build a comprehensive set of sampling frequencies
    const samplingFreqs = new Set<number>();

    // Add uniform logarithmic sampling as base
    const logMin = Math.log10(MIN_FREQ);
    const logMax = Math.log10(MAX_FREQ);
    for (let i = 0; i <= minSteps; i++) {
      const logFreq = logMin + (i / minSteps) * (logMax - logMin);
      samplingFreqs.add(Math.pow(10, logFreq));
    }

    // Add all critical frequencies
    for (const freq of criticalFreqs) {
      samplingFreqs.add(freq);
      // Add extra points around each critical frequency (skip during resize)
      if (!useReducedQuality && !isResizing) {
        for (let offset = 0.9; offset <= 1.1; offset += 0.01) {
          const nearFreq = freq * offset;
          if (nearFreq >= 20 && nearFreq <= 20000) {
            samplingFreqs.add(nearFreq);
          }
        }
      }
    }

    // Convert to sorted array and limit size
    let sortedFreqs = Array.from(samplingFreqs).sort((a, b) => a - b);
    if (sortedFreqs.length > maxSteps) {
      // Downsample if we have too many points
      const skipFactor = Math.ceil(sortedFreqs.length / maxSteps);
      sortedFreqs = sortedFreqs.filter((_, i) => i % skipFactor === 0);
    }

    const steps = sortedFreqs.length;
    const response = new Float32Array(steps);
    const xPositions = new Float32Array(steps);

    // Calculate response at each frequency
    for (let i = 0; i < steps; i++) {
      // eslint-disable-next-line security/detect-object-injection -- Safe: numeric array index
      const freq = sortedFreqs[i];
      const x = freqToX(freq);
      // eslint-disable-next-line security/detect-object-injection -- Safe: numeric array index
      xPositions[i] = x;
      // eslint-disable-next-line security/detect-object-injection -- Safe: numeric array index
      response[i] = calculateCombinedResponse(freq);
    }

    // Cache the computed response and positions
    const result = { response, xPositions };
    responseCache.set(cacheKey, result);
    lastResponseCacheKey = cacheKey;

    // Limit cache size to prevent memory leaks
    if (responseCache.size > 10) {
      const firstKey = responseCache.keys().next();
      if (!firstKey.done && firstKey.value !== undefined) {
        responseCache.delete(firstKey.value);
      }
    }

    return result;
  }

  // Draw the frequency response graph
  function drawGraph() {
    if (!canvas) return;

    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    // Guard against test environments with incomplete canvas context
    const requiredMethods = [
      'beginPath',
      'stroke',
      'clearRect',
      'fillRect',
      'moveTo',
      'lineTo',
      'createLinearGradient',
    ] as const;
    // eslint-disable-next-line security/detect-object-injection -- safe method name validation from const array
    if (requiredMethods.some(method => typeof (ctx as any)[method] !== 'function')) {
      return;
    }

    // Clear entire canvas
    ctx.clearRect(0, 0, canvasWidth, canvasHeight);

    // Set up styles with anti-aliasing for smooth curves
    ctx.imageSmoothingEnabled = true;
    ctx.lineCap = 'round';
    ctx.lineJoin = 'round';
    ctx.font = '11px system-ui, -apple-system, sans-serif';

    // Draw full canvas background with proper dark mode support
    ctx.fillStyle = colors.background;
    ctx.fillRect(0, 0, canvasWidth, canvasHeight);

    // Add subtle gradient overlay for depth (professional audio software style)
    if (colors.background === '#0d1117') {
      // Dark mode subtle gradient overlay
      const gradient = ctx.createLinearGradient(0, 0, 0, canvasHeight);
      gradient.addColorStop(0, 'rgba(255, 255, 255, 0.015)');
      gradient.addColorStop(0.5, 'rgba(255, 255, 255, 0.005)');
      gradient.addColorStop(1, 'rgba(0, 0, 0, 0.02)');
      ctx.fillStyle = gradient;
      ctx.fillRect(0, 0, canvasWidth, canvasHeight);
    }

    // Draw plot area border
    ctx.strokeStyle = colors.grid;
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.rect(margins.left, margins.top, plotWidth, plotHeight);
    ctx.stroke();

    // Draw grid lines within plot area
    ctx.strokeStyle = colors.grid;
    ctx.lineWidth = 0.5;

    // Vertical frequency grid lines
    for (const freq of freqGridLines) {
      const x = freqToX(freq);
      ctx.beginPath();
      ctx.moveTo(x, margins.top);
      ctx.lineTo(x, margins.top + plotHeight);
      ctx.stroke();
    }

    // Horizontal dB grid lines
    for (const db of dbGridLines) {
      const y = dbToY(db);
      ctx.beginPath();
      ctx.moveTo(margins.left, y);
      ctx.lineTo(margins.left + plotWidth, y);
      ctx.stroke();
    }

    // Draw 0dB reference line (professional style)
    ctx.strokeStyle = colors.reference;
    ctx.lineWidth = 2;
    ctx.setLineDash([8, 4]); // Dashed line for 0dB reference
    const zeroY = dbToY(0);
    ctx.beginPath();
    ctx.moveTo(margins.left, zeroY);
    ctx.lineTo(margins.left + plotWidth, zeroY);
    ctx.stroke();
    ctx.setLineDash([]); // Reset dash pattern

    // Individual filter curves removed - only show combined response

    // Draw combined response curve with clean professional styling
    ctx.globalAlpha = 1;
    ctx.strokeStyle = colors.primary;
    ctx.lineWidth = 3;

    // No glow effects - keep it clean and professional
    ctx.shadowColor = 'transparent';
    ctx.shadowBlur = 0;

    // PERFORMANCE: Use precomputed response curve
    const { response, xPositions } = computeResponseCurve();
    const steps = response.length;

    // CONTINUOUS CURVE: Draw as single path for smoothness
    if (steps > 0) {
      ctx.beginPath();

      // Start from first point
      const firstGain = response[0];
      // Clamp at visual minimum instead of skipping for continuity
      const firstY = dbToY(Math.max(MIN_DB + 0.5, firstGain));
      const firstX = xPositions[0];
      ctx.moveTo(firstX, Math.max(margins.top, Math.min(margins.top + plotHeight, firstY)));

      // Draw continuous line through all points
      for (let i = 1; i < steps; i++) {
        // eslint-disable-next-line security/detect-object-injection -- Safe: numeric array index
        const gain = response[i];
        // eslint-disable-next-line security/detect-object-injection -- Safe: numeric array index
        const x = xPositions[i];

        // CRITICAL: Always draw to maintain continuity
        // Clamp values at visual minimum instead of breaking the line
        const clampedGain = Math.max(MIN_DB + 0.5, gain);
        const y = dbToY(clampedGain);

        // Ensure y is within plot bounds
        const boundedY = Math.max(margins.top, Math.min(margins.top + plotHeight, y));

        ctx.lineTo(x, boundedY);
      }

      // Stroke the complete path once for best performance and smoothness
      ctx.stroke();
    }

    // Add subtle text when no filters are present - professional styling
    if (filters.length === 0) {
      ctx.fillStyle = colors.text;
      ctx.font = '13px system-ui, -apple-system, sans-serif';
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';
      ctx.globalAlpha = colors.background === '#0d1117' ? 0.4 : 0.5; // Lower opacity in dark mode
      ctx.fillText(
        'Flat Response (No Filters Applied)',
        canvasWidth / 2,
        margins.top + plotHeight / 2 - 20
      );
      ctx.globalAlpha = 1;
    }

    // Draw labels
    ctx.fillStyle = colors.text;
    ctx.textAlign = 'center';
    ctx.textBaseline = 'top';

    // Frequency labels (positioned below plot area) with better formatting for audio work
    for (const freq of freqGridLines) {
      const x = freqToX(freq);
      let label;
      if (freq >= 1000) {
        const kHz = freq / 1000;
        label = kHz % 1 === 0 ? `${kHz}k` : `${kHz.toFixed(1)}k`;
      } else {
        label = freq.toString();
      }
      ctx.fillText(label, x, margins.top + plotHeight + 20);
    }

    // dB labels (positioned to the left of plot area)
    ctx.textAlign = 'right';
    ctx.textBaseline = 'middle';
    for (const db of dbGridLines) {
      const y = dbToY(db);
      ctx.fillText(`${db}dB`, margins.left - 10, y);
    }

    // Axes labels with proper positioning
    ctx.fillStyle = colors.text;
    ctx.font = '12px system-ui, -apple-system, sans-serif';
    ctx.textAlign = 'center';
    ctx.textBaseline = 'bottom';
    ctx.fillText('Frequency (Hz)', canvasWidth / 2, canvasHeight - 10);

    // Y-axis label with better positioning
    ctx.save();
    ctx.translate(15, canvasHeight / 2);
    ctx.rotate(-Math.PI / 2);
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    ctx.fillText('Gain (dB)', 0, 0);
    ctx.restore();
  }

  // Handle mouse move for tooltip
  function handleMouseMove(event: MouseEvent) {
    const rect = canvas.getBoundingClientRect();
    // Use CSS coordinates for mouse positioning (not affected by DPR)
    const x = event.clientX - rect.left;
    const y = event.clientY - rect.top;

    // Only show tooltip when mouse is within plot area
    if (
      x >= margins.left &&
      x <= margins.left + plotWidth &&
      y >= margins.top &&
      y <= margins.top + plotHeight
    ) {
      const freq = Math.round(xToFreq(x));
      const gain = calculateCombinedResponse(freq);

      tooltip = {
        visible: true,
        x: x, // CSS coordinates for tooltip positioning
        y: y, // CSS coordinates for tooltip positioning
        freq,
        gain: Math.round(gain * 10) / 10,
      };
    } else {
      tooltip.visible = false;
    }
  }

  // Handle mouse leave
  function handleMouseLeave() {
    tooltip.visible = false;
  }

  // Update colors and dimensions when mounted
  onMount(() => {
    updateColors();
    updateCanvasDimensions();

    // Listen for theme changes
    const observer = new MutationObserver(() => {
      updateColors();
      scheduleDrawGraph();
    });

    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['data-theme', 'class'],
    });

    // Listen for window resize - optimized for responsiveness
    // eslint-disable-next-line no-undef -- ResizeObserver checked via globalThis
    let resizeObserver: ResizeObserver | undefined;
    if (typeof globalThis.ResizeObserver !== 'undefined' && containerElement) {
      // eslint-disable-next-line no-undef -- ResizeObserver available via globalThis
      const RO = globalThis.ResizeObserver as typeof ResizeObserver;
      resizeObserver = new RO(() => {
        // IMMEDIATE: First resize happens instantly with reduced quality
        if (!isResizing) {
          isResizing = true;
          useReducedQuality = true;
          lastInteractionTime = Date.now();

          // Immediate low-quality update for responsive feel
          updateCanvasDimensions();
          scheduleDrawGraph(true);
        }

        // Clear any existing timers
        if (resizeDebounceTimer !== null) {
          window.clearTimeout(resizeDebounceTimer);
        }
        if (qualityUpgradeTimer !== null) {
          window.clearTimeout(qualityUpgradeTimer);
        }

        // Short debounce for continued resizing (60fps)
        resizeDebounceTimer = window.setTimeout(() => {
          updateCanvasDimensions();
          scheduleDrawGraph(true);
          resizeDebounceTimer = null;
        }, 16); // 16ms = ~60fps for smooth resizing

        // Longer delay for final high-quality render
        qualityUpgradeTimer = window.setTimeout(() => {
          isResizing = false;
          useReducedQuality = false;

          // Final high-quality update with full cache clearing
          updateCanvasDimensions(true);
          scheduleDrawGraph();
          qualityUpgradeTimer = null;
        }, 150); // 150ms after resize stops
      });
      resizeObserver.observe(containerElement);
    }

    return () => {
      observer.disconnect();
      resizeObserver?.disconnect();
      // Clean up any pending animation frame
      if (animationFrameId !== null) {
        window.cancelAnimationFrame(animationFrameId);
      }
      // Clean up resize timers
      if (resizeDebounceTimer !== null) {
        window.clearTimeout(resizeDebounceTimer);
      }
      if (qualityUpgradeTimer !== null) {
        window.clearTimeout(qualityUpgradeTimer);
      }
    };
  });

  // Redraw graph when filters change or dimensions update
  $effect(() => {
    if (canvas) {
      // PERFORMANCE: Use efficient cache key
      const currentFilterState = createCacheKey(filters);
      if (currentFilterState !== lastFilterState) {
        // Clear ALL caches when filters change
        filterCoefficientsCache.clear();
        filterResponseCache.clear();
        responseCache.clear(); // Clear response cache
        // Keep trig cache as it's frequency-specific, not filter-specific
        lastFilterState = currentFilterState;
      }

      updateColors();
      scheduleDrawGraph();
    }
  });

  // Initialize canvas when it becomes available
  $effect(() => {
    // This effect will run when canvas element becomes available
    if (canvas) {
      // Initial setup when canvas ref becomes available
      updateCanvasDimensions();
      scheduleDrawGraph();
    }
  });
</script>

<!-- Centered container with proper spacing -->
<div class="w-full flex justify-center py-4" bind:this={containerElement}>
  <div class="relative">
    <canvas
      bind:this={canvas}
      width={canvasWidth}
      height={canvasHeight}
      class="border border-base-300 rounded-lg cursor-crosshair shadow-lg"
      style:background-color={colors.background}
      onmousemove={handleMouseMove}
      onmouseleave={handleMouseLeave}
    ></canvas>

    {#if tooltip && tooltip.visible && tooltip.x != null && tooltip.y != null}
      <div
        class="absolute z-10 px-3 py-2 text-xs bg-base-300 border border-base-content/20 rounded-lg shadow-lg pointer-events-none"
        style:left="{(tooltip.x ?? 0) + 10}px"
        style:top="{(tooltip.y ?? 0) - 10}px"
        style:transform="translateY(-100%)"
      >
        <div class="font-semibold">{tooltip.freq} Hz</div>
        <div class={tooltip.gain > 0 ? 'text-success' : tooltip.gain < -12 ? 'text-error' : ''}>
          {tooltip.gain > 0 ? '+' : ''}{tooltip.gain} dB
        </div>
      </div>
    {/if}
  </div>
</div>

<style>
  canvas {
    /* Enable smooth rendering for professional appearance */
    image-rendering: -webkit-optimize-contrast;
    image-rendering: crisp-edges;
  }
</style>
