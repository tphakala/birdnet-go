<!--
  EditorSlider - Range slider with label and value display.

  Features:
  - Pure Tailwind styling (no DaisyUI)
  - Label with value badge on the right
  - Custom value formatting
  - Full ARIA attributes
  - Clamping to min/max

  @component
-->
<script lang="ts">
  interface Props {
    label: string;
    value: number;
    onUpdate: (_value: number) => void;
    min: number;
    max: number;
    step?: number;
    formatValue?: (_value: number) => string;
    disabled?: boolean;
    id?: string;
  }

  let {
    label,
    value = $bindable(),
    onUpdate,
    min,
    max,
    step = 0.01,
    formatValue,
    disabled = false,
    id,
  }: Props = $props();

  // Unique suffix per instance to prevent ID collisions when labels repeat
  const idSuffix = crypto?.randomUUID?.().slice(0, 8) ?? Math.random().toString(36).slice(2, 10);

  const fieldId = $derived(
    id ||
      `editor-slider-${label
        .toLowerCase()
        .replace(/\s+/g, '-')
        .replace(/[^a-z0-9-]/g, '')}-${idSuffix}`
  );

  let displayValue = $derived(formatValue ? formatValue(value) : value.toFixed(2));

  function handleInput(event: Event) {
    const target = event.currentTarget as HTMLInputElement;
    const num = parseFloat(target.value);
    if (!isNaN(num)) {
      const clamped = Math.min(Math.max(num, min), max);
      value = clamped;
      onUpdate(clamped);
    }
  }
</script>

<div>
  <div class="flex items-center justify-between mb-1">
    <label for={fieldId} class="text-xs font-medium text-[var(--color-base-content)]/60">
      {label}
    </label>
    <span class="text-xs font-mono tabular-nums text-[var(--color-base-content)]">
      {displayValue}
    </span>
  </div>
  <input
    type="range"
    id={fieldId}
    {min}
    {max}
    {step}
    {disabled}
    {value}
    oninput={handleInput}
    onchange={handleInput}
    class="w-full h-2 bg-[var(--color-base-300)] rounded-lg appearance-none cursor-pointer accent-[var(--color-primary)] disabled:opacity-50 disabled:cursor-not-allowed"
    aria-label={label}
    aria-valuemin={min}
    aria-valuemax={max}
    aria-valuenow={value}
    aria-valuetext={displayValue}
    aria-disabled={disabled}
  />
</div>
