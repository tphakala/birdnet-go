<!--
  SettingsButton Component

  Purpose: A reusable button component for action buttons in settings forms
  like "Test Connection". Uses DaisyUI's btn component for proper theming.

  Features:
  - Uses DaisyUI btn-primary for consistent theming across Tailwind v4/DaisyUI 5
  - Auto-sizing width to fit content
  - Loading state support with spinner
  - Disabled state handling
  - Theme-compatible colors
  - Multiple style variants (primary, secondary, ghost)

  Props:
  - onclick: Click handler function
  - disabled: Whether button is disabled
  - loading: Whether to show loading spinner
  - loadingText: Text to show when loading (default: from translation)
  - variant: Button style variant (primary, secondary, ghost)
  - className: Additional CSS classes
  - children: Button content snippet
  - any other button attribute (aria-describedby, aria-label, ...) is passed
    through to the button. aria-disabled marks it unavailable but keeps it in
    the tab order, so its description is still announced; clicks, Enter and
    Space then do nothing.

  @component
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import type { HTMLButtonAttributes } from 'svelte/elements';

  type ButtonVariant = 'primary' | 'secondary' | 'ghost';

  interface Props extends Omit<
    HTMLButtonAttributes,
    'onclick' | 'disabled' | 'class' | 'type' | 'children' | 'aria-busy'
  > {
    onclick?: () => void;
    disabled?: boolean;
    loading?: boolean;
    loadingText?: string;
    variant?: ButtonVariant;
    className?: string;
    children?: import('svelte').Snippet;
  }

  let {
    onclick,
    disabled = false,
    loading = false,
    loadingText,
    variant = 'primary',
    className = '',
    children,
    ...rest
  }: Props = $props();

  // An aria-disabled button stays focusable, so it must ignore activation itself
  let ariaDisabled = $derived(rest['aria-disabled'] === true || rest['aria-disabled'] === 'true');

  // PERFORMANCE OPTIMIZATION: Use $derived for reactive default loading text
  let defaultLoadingText = $derived(loadingText || t('common.loading'));

  // Combined disabled state for both loading and disabled
  let isDisabled = $derived(disabled || loading);

  // Map variant to DaisyUI class
  const variantClasses: Record<ButtonVariant, string> = {
    primary: 'btn-primary',
    secondary: 'btn-secondary',
    ghost: 'btn-ghost',
  };

  // Runtime type guard to satisfy static analysis (object injection sink warning)
  const isButtonVariant = (v: unknown): v is ButtonVariant =>
    typeof v === 'string' && v in variantClasses;

  // eslint-disable-next-line security/detect-object-injection -- Validated by isButtonVariant type guard
  let variantClass = $derived(isButtonVariant(variant) ? variantClasses[variant] : 'btn-primary');
</script>

<button
  {...rest}
  type="button"
  class={cn(
    'btn btn-sm gap-2 aria-disabled:cursor-not-allowed aria-disabled:opacity-50 aria-disabled:focus-visible:opacity-75',
    variantClass,
    className
  )}
  onclick={() => !isDisabled && !ariaDisabled && onclick?.()}
  disabled={isDisabled}
  aria-busy={loading}
>
  {#if loading}
    <span class="loading loading-spinner loading-xs"></span>
    {defaultLoadingText}
  {:else if children}
    {@render children()}
  {/if}
</button>
