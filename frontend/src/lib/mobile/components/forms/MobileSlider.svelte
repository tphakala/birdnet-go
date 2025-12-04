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
    suffix?: string;
    helpText?: string;
    onUpdate?: (value: number) => void;
  }

  let {
    id = crypto.randomUUID(),
    label,
    value = $bindable(),
    min = 0,
    max = 100,
    step = 1,
    disabled = false,
    suffix = '',
    helpText,
    onUpdate,
  }: Props = $props();

  function handleInput(e: Event) {
    const target = e.target as HTMLInputElement;
    value = parseFloat(target.value);
    onUpdate?.(value);
  }
</script>

<div class="form-control w-full">
  <label class="label" for={id}>
    <span class="label-text font-medium">{label}</span>
    <span class="label-text-alt font-semibold">{value}{suffix}</span>
  </label>
  <input
    {id}
    type="range"
    {min}
    {max}
    {step}
    bind:value
    {disabled}
    class="range range-primary"
    oninput={handleInput}
  />
  <div class="flex justify-between text-xs text-base-content/50 px-1 mt-1">
    <span>{min}{suffix}</span>
    <span>{max}{suffix}</span>
  </div>
  {#if helpText}
    <label class="label">
      <span class="label-text-alt text-base-content/60">{helpText}</span>
    </label>
  {/if}
</div>
