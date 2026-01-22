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

  // Generate unique ID using crypto.randomUUID() with fallback
  const fieldId = `toggle-${crypto?.randomUUID?.() ?? Math.random().toString(36).substr(2, 9)}`;

  function handleChange(event: Event) {
    const target = event.target as HTMLInputElement;
    const newValue = target.checked;
    // Only notify parent via onUpdate, let bindable value handle internal state
    onUpdate(newValue);
  }

  // Native Tailwind toggle classes
  const toggleBaseClasses = `
    appearance-none w-12 h-6 rounded-full cursor-pointer transition-all relative
    bg-[var(--color-base-300)]
    before:content-[''] before:absolute before:top-0.5 before:left-0.5
    before:w-5 before:h-5 before:rounded-full before:bg-[var(--color-base-100)]
    before:shadow-sm before:transition-transform
    checked:bg-[var(--color-primary)] checked:before:translate-x-6
    focus-visible:outline-2 focus-visible:outline-[var(--color-primary)] focus-visible:outline-offset-2
    disabled:opacity-50 disabled:cursor-not-allowed
  `.trim();

  const toggleErrorClasses = 'checked:bg-[var(--color-error)]';
</script>

<div class={cn('min-w-0', className)} {...rest}>
  <div class="flex items-center justify-between">
    <div class="flex-1">
      <label for={fieldId} class="flex cursor-pointer justify-start gap-0 p-0">
        <div>
          <div class="text-sm font-medium text-[var(--color-base-content)]">
            {label}
            {#if required}
              <span class="text-[var(--color-error)]">*</span>
            {/if}
          </div>
          {#if description}
            <div class="text-xs text-[var(--color-base-content)] opacity-70 mt-1">
              {description}
            </div>
          {/if}
        </div>
      </label>
    </div>

    <div class="shrink-0 ml-4">
      <input
        id={fieldId}
        type="checkbox"
        class={cn(toggleBaseClasses, error && toggleErrorClasses)}
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
    <div id="{fieldId}-error" class="py-1">
      <span class="text-xs text-[var(--color-error)]">{error}</span>
    </div>
  {/if}
</div>
