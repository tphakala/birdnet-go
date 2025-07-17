<script lang="ts">
  import { cn } from '$lib/utils/cn.js';
  import type { HTMLAttributes } from 'svelte/elements';

  interface Props extends HTMLAttributes<HTMLDivElement> {
    label: string;
    description?: string;
    value: boolean;
    disabled?: boolean;
    error?: string;
    onUpdate: (_value: boolean) => void;
    required?: boolean;
    className?: string;
  }

  let {
    label,
    description,
    value = $bindable(),
    disabled = false,
    error,
    onUpdate,
    required = false,
    className = '',
    ...rest
  }: Props = $props();

  let fieldId = `toggle-${Math.random().toString(36).substring(2, 11)}`;

  function handleChange(event: Event) {
    const target = event.target as HTMLInputElement;
    const newValue = target.checked;
    value = newValue;
    onUpdate(newValue);
  }
</script>

<div class={cn('form-control', className)} {...rest}>
  <div class="flex items-center justify-between">
    <div class="flex-1">
      <label for={fieldId} class="label cursor-pointer justify-start gap-0 p-0">
        <div>
          <div class="label-text font-medium">
            {label}
            {#if required}
              <span class="text-error">*</span>
            {/if}
          </div>
          {#if description}
            <div class="label-text-alt text-base-content/70 mt-1">
              {description}
            </div>
          {/if}
        </div>
      </label>
    </div>

    <div class="flex-shrink-0 ml-4">
      <input
        id={fieldId}
        type="checkbox"
        class={cn('toggle toggle-primary', { 'toggle-error': !!error })}
        checked={value}
        {disabled}
        {required}
        onchange={handleChange}
        aria-describedby={error
          ? `${fieldId}-error`
          : description
            ? `${fieldId}-description`
            : undefined}
      />
    </div>
  </div>

  {#if error}
    <div id="{fieldId}-error" class="label">
      <span class="label-text-alt text-error">{error}</span>
    </div>
  {/if}
</div>
