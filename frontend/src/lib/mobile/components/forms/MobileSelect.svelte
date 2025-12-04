<script lang="ts">
  /* eslint-disable no-unused-vars -- $bindable() props are used in template bind: directives */
  interface Option {
    value: string;
    label: string;
  }

  interface Props {
    id?: string;
    label: string;
    value: string;
    options: Option[];
    disabled?: boolean;
    error?: string;
    helpText?: string;
    onchange?: (value: string) => void;
  }

  let {
    id = crypto.randomUUID(),
    label,
    value = $bindable(),
    options,
    disabled = false,
    error,
    helpText,
    onchange,
  }: Props = $props();

  function handleChange(e: Event) {
    const target = e.target as HTMLSelectElement;
    value = target.value;
    onchange?.(value);
  }
</script>

<div class="form-control w-full">
  <label class="label" for={id}>
    <span class="label-text font-medium">{label}</span>
  </label>
  <select
    {id}
    bind:value
    {disabled}
    class="select select-bordered w-full h-12"
    class:select-error={error}
    onchange={handleChange}
  >
    {#each options as option (option.value)}
      <option value={option.value}>{option.label}</option>
    {/each}
  </select>
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
