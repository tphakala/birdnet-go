<script lang="ts">
  import { cn } from '$lib/utils/cn.js';
  import { generateId } from '$lib/utils/uuid';
  import type { HTMLAttributes } from 'svelte/elements';
  import type { Component } from 'svelte';

  interface Props extends HTMLAttributes<HTMLDivElement> {
    label: string;
    description?: string;
    /** Optional icon component displayed before the label */
    icon?: Component<{ class?: string }>;
    /** Toggle switch size */
    size?: 'sm' | 'md';
    /**
     * 'default' is a row with the switch on the right. 'card' draws a bordered card that is
     * itself the click target (the whole card toggles), with the icon turning primary while
     * on and the keyboard focus ring around the card instead of the switch.
     */
    variant?: 'default' | 'card';
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
    icon,
    size = 'sm',
    variant = 'default',
    value = $bindable(),
    disabled = false,
    error,
    onUpdate,
    required = false,
    className = '',
    ...rest
  }: Props = $props();

  const fieldId = generateId('toggle');
  // The switch is named by the label text only; the description is its accessible
  // description, so a screen reader does not read it twice
  const labelId = `${fieldId}-label`;
  const descriptionId = `${fieldId}-description`;

  function handleChange(event: Event) {
    const target = event.target as HTMLInputElement;
    const newValue = target.checked;
    // Only notify parent via onUpdate, let bindable value handle internal state
    onUpdate(newValue);
  }

  // Native Tailwind toggle classes — shared base + size variants
  const toggleSharedClasses = `
    appearance-none rounded-full cursor-pointer transition-all relative
    bg-[var(--color-base-300)]
    before:content-[''] before:absolute before:top-0.5 before:left-0.5
    before:rounded-full before:bg-[var(--color-base-100)]
    before:shadow-sm before:transition-transform
    checked:bg-[var(--color-primary)]
    disabled:opacity-50 disabled:cursor-not-allowed
  `.trim();

  // The default variant rings the switch itself; the card variant rings the card instead
  const toggleFocusClasses =
    'focus-visible:outline-2 focus-visible:outline-[var(--color-primary)] focus-visible:outline-offset-2';

  const toggleSizeClasses = {
    sm: 'w-10 h-5 before:w-4 before:h-4 checked:before:translate-x-5',
    md: 'w-12 h-6 before:w-5 before:h-5 checked:before:translate-x-6',
  };

  // eslint-disable-next-line security/detect-object-injection -- size is typed as 'sm' | 'md'
  let toggleBaseClasses = $derived(`${toggleSharedClasses} ${toggleSizeClasses[size]}`);

  const toggleErrorClasses = 'checked:bg-[var(--color-error)]';

  const CARD_BASE_CLASS =
    'flex w-full cursor-pointer items-start gap-3 rounded-lg border-2 p-4 text-left transition-[border-color,background-color] motion-reduce:transition-none has-[input:focus-visible]:outline-2 has-[input:focus-visible]:outline-[var(--color-primary)] has-[input:focus-visible]:outline-offset-2';
  const CARD_ON_CLASS = 'border-[var(--color-primary)] bg-[var(--color-primary)]/5';
  const CARD_OFF_CLASS = 'border-[var(--border-200)] hover:border-[var(--border-300)]';

  let cardClasses = $derived(
    cn(
      CARD_BASE_CLASS,
      value ? CARD_ON_CLASS : CARD_OFF_CLASS,
      disabled && 'cursor-not-allowed opacity-50'
    )
  );
  let cardIconClasses = $derived(
    cn(
      'mt-0.5 size-5 shrink-0',
      value ? 'text-[var(--color-primary)]' : 'text-[var(--color-base-content)] opacity-70'
    )
  );

  let describedBy = $derived(error ? `${fieldId}-error` : description ? descriptionId : undefined);
</script>

<div class={cn('min-w-0', className)} {...rest}>
  {#if variant === 'card'}
    <label for={fieldId} class={cardClasses}>
      {#if icon}
        {@const Icon = icon}
        <Icon class={cardIconClasses} />
      {/if}
      <span class="block min-w-0 flex-1">
        <span id={labelId} class="block text-sm font-medium text-[var(--color-base-content)]">
          {label}
          {#if required}
            <span class="text-[var(--color-error)]">*</span>
          {/if}
        </span>
        {#if description}
          <span
            id={descriptionId}
            class="mt-0.5 block text-sm text-[var(--color-base-content)] opacity-80"
          >
            {description}
          </span>
        {/if}
      </span>
      <input
        id={fieldId}
        type="checkbox"
        class={cn(toggleBaseClasses, 'mt-0.5 shrink-0', error && toggleErrorClasses)}
        checked={value}
        {disabled}
        {required}
        onchange={handleChange}
        aria-labelledby={labelId}
        aria-describedby={describedBy}
      />
    </label>
  {:else}
    <div class="flex items-center justify-between">
      <div class="flex-1">
        <label for={fieldId} class="flex cursor-pointer items-start gap-2 p-0">
          {#if icon}
            {@const Icon = icon}
            <Icon class="size-4 text-[var(--color-base-content)] opacity-60 mt-0.5 shrink-0" />
          {/if}
          <div>
            <div id={labelId} class="text-sm font-medium text-[var(--color-base-content)]">
              {label}
              {#if required}
                <span class="text-[var(--color-error)]">*</span>
              {/if}
            </div>
            {#if description}
              <div id={descriptionId} class="help-text mt-1">
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
          class={cn(toggleBaseClasses, toggleFocusClasses, error && toggleErrorClasses)}
          checked={value}
          {disabled}
          {required}
          onchange={handleChange}
          aria-labelledby={labelId}
          aria-describedby={describedBy}
        />
      </div>
    </div>
  {/if}

  {#if error}
    <div id="{fieldId}-error" class="py-1">
      <span class="text-xs text-[var(--color-error)]">{error}</span>
    </div>
  {/if}
</div>
