<script lang="ts">
  /* eslint-disable no-unused-vars -- $bindable() props are used in template bind: directives */
  interface Props {
    id?: string;
    label: string;
    value: number;
    min?: number;
    max?: number;
    step?: number;
    disabled?: boolean;
    error?: string;
    helpText?: string;
    suffix?: string;
    onUpdate?: (value: number) => void;
  }

  let {
    id = crypto.randomUUID(),
    label,
    value = $bindable(),
    min,
    max,
    step = 1,
    disabled = false,
    error,
    helpText,
    suffix,
    onUpdate,
  }: Props = $props();

  function increment() {
    if (max !== undefined && value >= max) return;
    value = value + step;
    onUpdate?.(value);
  }

  function decrement() {
    if (min !== undefined && value <= min) return;
    value = value - step;
    onUpdate?.(value);
  }

  function handleInput(e: Event) {
    const target = e.target as HTMLInputElement;
    const parsed = parseFloat(target.value);
    if (!isNaN(parsed)) {
      value = parsed;
      onUpdate?.(value);
    }
  }
</script>

<div class="form-control w-full">
  <label class="label" for={id}>
    <span class="label-text font-medium">{label}</span>
  </label>
  <div class="flex items-center gap-2">
    <button
      type="button"
      class="btn btn-square btn-outline h-12 w-12"
      onclick={decrement}
      {disabled}
      aria-label="Decrease"
    >
      −
    </button>
    <div class="relative flex-1">
      <input
        {id}
        type="number"
        bind:value
        {min}
        {max}
        {step}
        {disabled}
        class="input input-bordered w-full h-12 text-center"
        class:input-error={error}
        oninput={handleInput}
      />
      {#if suffix}
        <span class="absolute right-3 top-1/2 -translate-y-1/2 text-base-content/60">
          {suffix}
        </span>
      {/if}
    </div>
    <button
      type="button"
      class="btn btn-square btn-outline h-12 w-12"
      onclick={increment}
      {disabled}
      aria-label="Increase"
    >
      +
    </button>
  </div>
  {#if helpText && !error}
    <label class="label">
      <span class="label-text-alt text-base-content/60">{helpText}</span>
    </label>
  {/if}
  {#if error}
    <label class="label">
      <span class="label-text-alt text-error">{error}</span>
    </label>
  {/if}
</div>
