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

  @component
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';

  type ButtonVariant = 'primary' | 'secondary' | 'ghost';

  interface Props {
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
  }: Props = $props();

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
  type="button"
  class={cn('btn btn-sm gap-2', variantClass, className)}
  onclick={() => !isDisabled && onclick?.()}
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
