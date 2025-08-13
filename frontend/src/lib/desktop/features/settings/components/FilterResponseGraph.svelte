<!--
  Filter Response Graph Component
  
  Purpose: Visualizes frequency response curves for audio filters
  
  Features:
  - Real-time frequency response visualization
  - Logarithmic frequency scale (20Hz - 20kHz)
  - Gain display (-48dB to +12dB)
  - Interactive tooltips on hover
  - Multiple filter curve overlay
  - Combined response curve
  
  Props:
  - filters: Array of filter configurations
  - width: Canvas width (optional)
  - height: Canvas height (optional)
  
  @component
-->
<script lang="ts">
  interface Filter {
    type: string;
    frequency: number;
    q?: number;
    passes?: number;
    [key: string]: any;
  }

  interface Props {
    filters: Filter[];
    width?: number;
    height?: number;
  }

  let { filters = [], width = 600, height = 300 }: Props = $props();

  // Canvas element reference
  let canvas: HTMLCanvasElement;
  let tooltip = $state({ visible: false, x: 0, y: 0, freq: 0, gain: 0 });

  // Frequency range (logarithmic scale)
  const MIN_FREQ = 20;
  const MAX_FREQ = 20000;
  const MIN_DB = -48;
  const MAX_DB = 12;

  // Grid lines for frequency (Hz)
  const freqGridLines = [20, 50, 100, 200, 500, 1000, 2000, 5000, 10000, 20000];
  const dbGridLines = [-48, -36, -24, -12, 0, 12];

  // Calculate frequency response for a filter
  function calculateFilterResponse(filter: Filter, frequency: number): number {
    const omega = (2 * Math.PI * frequency) / 48000; // Assuming 48kHz sample rate
    const sin = Math.sin(omega);
    const cos = Math.cos(omega);

    const q = filter.q || 0.707;
    const attenuation = (filter.passes || 0) * 12; // 12dB per pass

    if (filter.type === 'LowPass') {
      // Butterworth lowpass response
      const alpha = sin / (2 * q);
      const b0 = (1 - cos) / 2;
      const b1 = 1 - cos;
      const b2 = (1 - cos) / 2;
      const a0 = 1 + alpha;
      const a1 = -2 * cos;
      const a2 = 1 - alpha;

      // Frequency response magnitude
      const numerator = Math.sqrt(
        Math.pow(b0 + b1 * cos + b2 * Math.cos(2 * omega), 2) +
          Math.pow(b1 * sin + b2 * Math.sin(2 * omega), 2)
      );
      const denominator = Math.sqrt(
        Math.pow(a0 + a1 * cos + a2 * Math.cos(2 * omega), 2) +
          Math.pow(a1 * sin + a2 * Math.sin(2 * omega), 2)
      );

      const magnitude = numerator / denominator;
      const db = 20 * Math.log10(magnitude);

      // Apply additional attenuation for multiple passes
      return db - (frequency > filter.frequency ? attenuation : 0);
    } else if (filter.type === 'HighPass') {
      // Butterworth highpass response
      const alpha = sin / (2 * q);
      const b0 = (1 + cos) / 2;
      const b1 = -(1 + cos);
      const b2 = (1 + cos) / 2;
      const a0 = 1 + alpha;
      const a1 = -2 * cos;
      const a2 = 1 - alpha;

      const numerator = Math.sqrt(
        Math.pow(b0 + b1 * cos + b2 * Math.cos(2 * omega), 2) +
          Math.pow(b1 * sin + b2 * Math.sin(2 * omega), 2)
      );
      const denominator = Math.sqrt(
        Math.pow(a0 + a1 * cos + a2 * Math.cos(2 * omega), 2) +
          Math.pow(a1 * sin + a2 * Math.sin(2 * omega), 2)
      );

      const magnitude = numerator / denominator;
      const db = 20 * Math.log10(magnitude);

      // Apply additional attenuation for multiple passes
      return db - (frequency < filter.frequency ? attenuation : 0);
    } else if (filter.type === 'BandPass') {
      // Butterworth bandpass response
      const alpha = sin / (2 * q);
      const b0 = alpha;
      const b1 = 0;
      const b2 = -alpha;
      const a0 = 1 + alpha;
      const a1 = -2 * cos;
      const a2 = 1 - alpha;

      const numerator = Math.sqrt(
        Math.pow(b0 + b1 * cos + b2 * Math.cos(2 * omega), 2) +
          Math.pow(b1 * sin + b2 * Math.sin(2 * omega), 2)
      );
      const denominator = Math.sqrt(
        Math.pow(a0 + a1 * cos + a2 * Math.cos(2 * omega), 2) +
          Math.pow(a1 * sin + a2 * Math.sin(2 * omega), 2)
      );

      const magnitude = numerator / denominator;
      return 20 * Math.log10(magnitude) - attenuation;
    } else if (filter.type === 'BandStop' || filter.type === 'Notch') {
      // Butterworth bandstop/notch response
      const alpha = sin / (2 * q);
      const b0 = 1;
      const b1 = -2 * cos;
      const b2 = 1;
      const a0 = 1 + alpha;
      const a1 = -2 * cos;
      const a2 = 1 - alpha;

      const numerator = Math.sqrt(
        Math.pow(b0 + b1 * cos + b2 * Math.cos(2 * omega), 2) +
          Math.pow(b1 * sin + b2 * Math.sin(2 * omega), 2)
      );
      const denominator = Math.sqrt(
        Math.pow(a0 + a1 * cos + a2 * Math.cos(2 * omega), 2) +
          Math.pow(a1 * sin + a2 * Math.sin(2 * omega), 2)
      );

      const magnitude = numerator / denominator;
      return 20 * Math.log10(magnitude) - attenuation;
    }

    return 0; // No filter effect
  }

  // Calculate combined response of all filters
  function calculateCombinedResponse(frequency: number): number {
    let totalGain = 0;
    for (const filter of filters) {
      totalGain += calculateFilterResponse(filter, frequency);
    }
    return Math.max(MIN_DB, Math.min(MAX_DB, totalGain));
  }

  // Convert frequency to x position (logarithmic scale)
  function freqToX(freq: number): number {
    const logMin = Math.log10(MIN_FREQ);
    const logMax = Math.log10(MAX_FREQ);
    const logFreq = Math.log10(freq);
    return ((logFreq - logMin) / (logMax - logMin)) * width;
  }

  // Convert x position to frequency
  function xToFreq(x: number): number {
    const logMin = Math.log10(MIN_FREQ);
    const logMax = Math.log10(MAX_FREQ);
    const logFreq = logMin + (x / width) * (logMax - logMin);
    return Math.pow(10, logFreq);
  }

  // Convert dB to y position
  function dbToY(db: number): number {
    return height - ((db - MIN_DB) / (MAX_DB - MIN_DB)) * height;
  }

  // Draw the frequency response graph
  function drawGraph() {
    if (!canvas) return;

    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    // Clear canvas
    ctx.clearRect(0, 0, width, height);

    // Set up styles
    ctx.font = '10px system-ui, -apple-system, sans-serif';

    // Draw background
    ctx.fillStyle = 'oklch(var(--b1))';
    ctx.fillRect(0, 0, width, height);

    // Draw grid lines
    ctx.strokeStyle = 'oklch(var(--b3))';
    ctx.lineWidth = 0.5;

    // Vertical frequency grid lines
    for (const freq of freqGridLines) {
      const x = freqToX(freq);
      ctx.beginPath();
      ctx.moveTo(x, 0);
      ctx.lineTo(x, height);
      ctx.stroke();
    }

    // Horizontal dB grid lines
    for (const db of dbGridLines) {
      const y = dbToY(db);
      ctx.beginPath();
      ctx.moveTo(0, y);
      ctx.lineTo(width, y);
      ctx.stroke();
    }

    // Draw 0dB reference line
    ctx.strokeStyle = 'oklch(var(--bc) / 0.3)';
    ctx.lineWidth = 1;
    const zeroY = dbToY(0);
    ctx.beginPath();
    ctx.moveTo(0, zeroY);
    ctx.lineTo(width, zeroY);
    ctx.stroke();

    // Draw individual filter responses
    const filterColors = [
      'oklch(var(--in))', // Blue
      'oklch(var(--wa))', // Yellow
      'oklch(var(--su))', // Green
      'oklch(var(--er))', // Red
    ];

    filters.forEach((filter, index) => {
      ctx.strokeStyle = filterColors[index % filterColors.length];
      ctx.lineWidth = 1.5;
      ctx.globalAlpha = 0.6;

      ctx.beginPath();
      for (let x = 0; x <= width; x += 2) {
        const freq = xToFreq(x);
        const gain = calculateFilterResponse(filter, freq);
        const y = dbToY(gain);

        if (x === 0) {
          ctx.moveTo(x, y);
        } else {
          ctx.lineTo(x, y);
        }
      }
      ctx.stroke();
    });

    // Draw combined response
    ctx.globalAlpha = 1;
    ctx.strokeStyle = 'oklch(var(--p))';
    ctx.lineWidth = 2;

    ctx.beginPath();
    for (let x = 0; x <= width; x += 1) {
      const freq = xToFreq(x);
      const gain = calculateCombinedResponse(freq);
      const y = dbToY(gain);

      if (x === 0) {
        ctx.moveTo(x, y);
      } else {
        ctx.lineTo(x, y);
      }
    }
    ctx.stroke();

    // Draw labels
    ctx.fillStyle = 'oklch(var(--bc) / 0.6)';
    ctx.textAlign = 'center';
    ctx.textBaseline = 'top';

    // Frequency labels
    for (const freq of freqGridLines) {
      const x = freqToX(freq);
      const label = freq >= 1000 ? `${freq / 1000}k` : freq.toString();
      ctx.fillText(label, x, height - 20);
    }

    // dB labels
    ctx.textAlign = 'right';
    ctx.textBaseline = 'middle';
    for (const db of dbGridLines) {
      const y = dbToY(db);
      ctx.fillText(`${db}dB`, 30, y);
    }

    // Axes labels
    ctx.fillStyle = 'oklch(var(--bc) / 0.8)';
    ctx.font = '11px system-ui, -apple-system, sans-serif';
    ctx.textAlign = 'center';
    ctx.textBaseline = 'bottom';
    ctx.fillText('Frequency (Hz)', width / 2, height);

    ctx.save();
    ctx.translate(10, height / 2);
    ctx.rotate(-Math.PI / 2);
    ctx.fillText('Gain (dB)', 0, 0);
    ctx.restore();
  }

  // Handle mouse move for tooltip
  function handleMouseMove(event: MouseEvent) {
    const rect = canvas.getBoundingClientRect();
    const x = event.clientX - rect.left;

    const freq = Math.round(xToFreq(x));
    const gain = calculateCombinedResponse(freq);

    tooltip = {
      visible: true,
      x: event.clientX,
      y: event.clientY,
      freq,
      gain: Math.round(gain * 10) / 10,
    };
  }

  // Handle mouse leave
  function handleMouseLeave() {
    tooltip.visible = false;
  }

  // Redraw graph when filters change
  $effect(() => {
    drawGraph();
  });

  // Initial draw when canvas is ready
  $effect(() => {
    if (canvas) {
      drawGraph();
    }
  });
</script>

<div class="relative">
  <canvas
    bind:this={canvas}
    {width}
    {height}
    class="border border-base-300 rounded-lg cursor-crosshair"
    onmousemove={handleMouseMove}
    onmouseleave={handleMouseLeave}
  ></canvas>

  {#if tooltip.visible}
    <div
      class="absolute z-10 px-2 py-1 text-xs bg-base-300 border border-base-content/20 rounded shadow-lg pointer-events-none"
      style:left="{tooltip.x}px"
      style:top="{tooltip.y - 30}px"
      style:transform="translateX(-50%)"
    >
      <div class="font-semibold">{tooltip.freq} Hz</div>
      <div class={tooltip.gain > 0 ? 'text-success' : tooltip.gain < -12 ? 'text-error' : ''}>
        {tooltip.gain > 0 ? '+' : ''}{tooltip.gain} dB
      </div>
    </div>
  {/if}
</div>

<style>
  canvas {
    image-rendering: pixelated;
  }
</style>
