<script lang="ts">
  import { cn } from '$lib/utils/cn';

  interface Option {
    value: string;
    label: string;
  }

  interface Props {
    value: string;
    options: Option[];
    id?: string;
    className?: string;
    disabled?: boolean;
    placeholder?: string;
  }

  let {
    value = $bindable(),
    options,
    id,
    className = '',
    disabled = false,
    placeholder,
    ...rest
  }: Props = $props();
</script>

<select {id} bind:value class={cn('select select-bordered w-full', className)} {disabled} {...rest}>
  {#if placeholder}
    <option value="" disabled>{placeholder}</option>
  {/if}

  {#each options as option}
    <option value={option.value}>{option.label}</option>
  {/each}
</select>
