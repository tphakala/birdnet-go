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
     * on and the keyboard focus ring around the card instead of the switch. 'inline' is a
     * compact control for toolbars and header bars: small muted label text, then the switch,
     * both inside one label so the text toggles too. Its hit area reaches 0.375rem above and
     * below the label, so the host leaves that space free of other controls.
     */
    variant?: 'default' | 'card' | 'inline';
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

  // A disabled inline switch uses aria-disabled instead of native disabled, so Tab still
  // reaches it and a screen reader reads its description (the reason it is off). The click
  // (from the switch, its label or Space) is cancelled instead, so it never flips.
  let softDisabled = $derived(variant === 'inline' && disabled);

  function handleClick(event: MouseEvent) {
    if (softDisabled) event.preventDefault();
  }

  function handleChange(event: Event) {
    const target = event.target as HTMLInputElement;
    // A click cancel can be skipped when an ancestor stops the click before it reaches the
    // delegated handler, so a disabled inline switch also restores its state here
    if (softDisabled) {
      target.checked = value;
      return;
    }
    const newValue = target.checked;
    // Only notify parent via onUpdate, let bindable value handle internal state
    onUpdate(newValue);
  }

  // Native Tailwind toggle classes: shared base + size variants. The off track is
  // base-content at 60% for at least 3:1 against the card (WCAG 1.4.11), as the Checkbox box is.
  const toggleSharedClasses = `
    appearance-none rounded-full cursor-pointer transition-all relative
    bg-[var(--color-base-content)]/60
    before:content-[''] before:absolute before:top-0.5 before:left-0.5
    before:rounded-full before:bg-[var(--color-base-100)]
    before:shadow-sm before:transition-transform
    checked:bg-[var(--color-primary)]
    disabled:cursor-not-allowed
  `.trim();

  // The default variant rings and dims the switch itself. The card variant rings and dims
  // the card instead, and clears the browser's own ring so it does not double the card ring.
  const toggleDefaultClasses =
    'focus-visible:outline-2 focus-visible:outline-[var(--color-primary)] focus-visible:outline-offset-2 disabled:opacity-50';
  const toggleCardClasses = 'focus-visible:outline-none';

  const toggleSizeClasses = {
    sm: 'w-10 h-5 before:w-4 before:h-4 checked:before:translate-x-5',
    md: 'w-12 h-6 before:w-5 before:h-5 checked:before:translate-x-6',
  };

  // eslint-disable-next-line security/detect-object-injection -- size is typed as 'sm' | 'md'
  let toggleBaseClasses = $derived(`${toggleSharedClasses} ${toggleSizeClasses[size]}`);

  const toggleErrorClasses = 'checked:bg-[var(--color-error)]';

  // cn does not merge conflicting utilities, so the cursor and the hover border are chosen
  // by state here instead of being overridden later
  const CARD_BASE_CLASS =
    'flex w-full items-start gap-3 rounded-lg border-2 p-4 text-left transition-[border-color,background-color] motion-reduce:transition-none has-[input:focus-visible]:outline-2 has-[input:focus-visible]:outline-[var(--color-primary)] has-[input:focus-visible]:outline-offset-2';
  const CARD_ON_CLASS = 'border-[var(--color-primary)] bg-[var(--color-primary)]/5';
  const CARD_OFF_CLASS = 'border-[var(--border-200)]';
  const CARD_ENABLED_CLASS = 'cursor-pointer';
  const CARD_DISABLED_CLASS = 'cursor-not-allowed opacity-50';
  // Only an unchecked, enabled card reacts to hover, so a checked card keeps its primary border
  const CARD_HOVER_CLASS = 'hover:border-[var(--border-300)]';

  // The inline label is 2rem tall to sit in a toolbar row. Its transparent ::after layer
  // stretches the hit area to 2.75rem (44 px, a comfortable touch target) into the padding
  // above and below without making the row taller. It does not extend sideways, so it stays
  // off controls beside it. isolate and -z-10 keep the layer behind the text and the switch,
  // so pointer hits on them land on them.
  const INLINE_LABEL_CLASS =
    "relative isolate inline-flex min-h-8 items-center gap-2 after:absolute after:inset-x-0 after:-inset-y-1.5 after:-z-10 after:content-['']";

  // The inline switch is aria-disabled rather than :disabled, so it dims on that attribute
  const INLINE_SWITCH_DISABLED_CLASS = 'aria-disabled:opacity-50 aria-disabled:cursor-not-allowed';

  let labelCursorClass = $derived(disabled ? 'cursor-not-allowed' : 'cursor-pointer');
  // Error text and the required mark use the error text token; --color-error is for fills
  const ERROR_TEXT_CLASS = 'text-[var(--text-error)]';

  let cardClasses = $derived(
    cn(
      CARD_BASE_CLASS,
      value ? CARD_ON_CLASS : CARD_OFF_CLASS,
      disabled ? CARD_DISABLED_CLASS : CARD_ENABLED_CLASS,
      !value && !disabled && CARD_HOVER_CLASS
    )
  );
  let cardIconClasses = $derived(
    cn(
      'mt-0.5 size-5 shrink-0',
      value ? 'text-[var(--color-primary)]' : 'text-[var(--color-base-content)] opacity-70'
    )
  );

  // The description first, then the error: neither is part of the accessible name
  let describedBy = $derived(
    [description ? descriptionId : undefined, error ? `${fieldId}-error` : undefined]
      .filter(Boolean)
      .join(' ') || undefined
  );
</script>

{#snippet requiredMark()}
  {#if required}
    <span class={ERROR_TEXT_CLASS}>*</span>
  {/if}
{/snippet}

<!-- The switch is the same in every variant; only its extra classes differ -->
{#snippet switchInput(variantClasses: string)}
  <input
    id={fieldId}
    type="checkbox"
    class={cn(toggleBaseClasses, variantClasses, error && toggleErrorClasses)}
    checked={value}
    disabled={disabled && !softDisabled}
    aria-disabled={softDisabled ? 'true' : undefined}
    required={required && !softDisabled}
    onclick={handleClick}
    onchange={handleChange}
    aria-labelledby={labelId}
    aria-describedby={describedBy}
    aria-invalid={error ? 'true' : undefined}
  />
{/snippet}

<!-- The inline variant sets regular weight so a semibold toolbar or table header does not
     make its text bold -->
<div class={cn('min-w-0', variant === 'inline' && 'font-normal', className)} {...rest}>
  {#if variant === 'card'}
    <label for={fieldId} class={cardClasses}>
      {#if icon}
        {@const Icon = icon}
        <Icon class={cardIconClasses} />
      {/if}
      <span class="block min-w-0 flex-1">
        <span id={labelId} class="block text-sm font-medium text-[var(--color-base-content)]">
          {label}
          {@render requiredMark()}
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
      {@render switchInput(cn(toggleCardClasses, 'mt-0.5 shrink-0'))}
    </label>
  {:else if variant === 'inline'}
    <label for={fieldId} class={cn(INLINE_LABEL_CLASS, labelCursorClass)}>
      {#if icon}
        {@const Icon = icon}
        <Icon class="size-3.5 shrink-0 text-muted" />
      {/if}
      <span id={labelId} class="text-xs text-muted">
        {label}
        {@render requiredMark()}
      </span>
      {@render switchInput(cn(toggleDefaultClasses, INLINE_SWITCH_DISABLED_CLASS, 'shrink-0'))}
    </label>
    <!-- The text below starts 0.375rem under the label, outside its hit layer -->
    {#if description}
      <div id={descriptionId} class="help-text mt-1.5">{description}</div>
    {/if}
  {:else}
    <div class="flex items-center justify-between">
      <div class="flex-1">
        <label for={fieldId} class={cn('flex items-start gap-2 p-0', labelCursorClass)}>
          {#if icon}
            {@const Icon = icon}
            <Icon class="size-4 text-[var(--color-base-content)] opacity-60 mt-0.5 shrink-0" />
          {/if}
          <div>
            <div id={labelId} class="text-sm font-medium text-[var(--color-base-content)]">
              {label}
              {@render requiredMark()}
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
        {@render switchInput(toggleDefaultClasses)}
      </div>
    </div>
  {/if}

  {#if error}
    <div id="{fieldId}-error" class={cn('py-1', variant === 'inline' && !description && 'mt-0.5')}>
      <span class={cn('text-xs', ERROR_TEXT_CLASS)}>{error}</span>
    </div>
  {/if}
</div>
