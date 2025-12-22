<script lang="ts">
  import { cn } from '$lib/utils/cn.js';
  import type { HTMLAttributes } from 'svelte/elements';
  import { Eye, EyeOff, TriangleAlert } from '@lucide/svelte';
  import { t } from '$lib/i18n';

  // Generate unique ID using crypto.randomUUID for SSR compatibility
  const generateUniqueId = () => {
    if (typeof crypto !== 'undefined' && crypto.randomUUID) {
      return `password-field-${crypto.randomUUID()}`;
    }
    return `password-field-${Math.random().toString(36).substr(2, 9)}-${Date.now()}`;
  };

  interface Props extends HTMLAttributes<HTMLDivElement> {
    label: string;
    name?: string;
    value: string;
    onUpdate: (_value: string) => void;
    placeholder?: string;
    helpText?: string;
    required?: boolean;
    disabled?: boolean;
    error?: string;
    className?: string;
    showStrength?: boolean;
    allowReveal?: boolean;
    autocomplete?: 'current-password' | 'new-password' | 'off';
  }

  let {
    label,
    name,
    value = $bindable(),
    onUpdate,
    placeholder = '',
    helpText = '',
    required = false,
    disabled = false,
    error,
    className = '',
    showStrength = false,
    allowReveal = true,
    autocomplete = 'current-password',
    ...rest
  }: Props = $props();

  // Generate unique ID for this component instance (captured at creation, intentionally non-reactive)
  const fieldId = (() => name || generateUniqueId())();

  let showPassword = $state(false);

  // Password strength calculation
  let passwordStrength = $derived.by(() => {
    if (!showStrength || !value) return null;

    let score = 0;
    let feedback: string[] = [];

    // Length check
    if (value.length >= 8) {
      score += 1;
    } else {
      feedback.push(t('forms.password.strength.suggestions.minLength'));
    }

    // Character variety checks
    if (/[a-z]/.test(value) && /[A-Z]/.test(value)) {
      score += 1;
    } else {
      feedback.push(t('forms.password.strength.suggestions.mixedCase'));
    }

    if (/\d/.test(value)) {
      score += 1;
    } else {
      feedback.push(t('forms.password.strength.suggestions.number'));
    }

    if (/[^a-zA-Z0-9]/.test(value)) {
      score += 1;
    } else {
      feedback.push(t('forms.password.strength.suggestions.special'));
    }

    // Determine strength level
    let level: 'weak' | 'fair' | 'good' | 'strong';
    let color: string;

    if (score <= 1) {
      level = 'weak';
      color = 'text-error';
    } else if (score === 2) {
      level = 'fair';
      color = 'text-warning';
    } else if (score === 3) {
      level = 'good';
      color = 'text-info';
    } else {
      level = 'strong';
      color = 'text-success';
    }

    return { score, level, color, feedback };
  });

  function handleChange(newValue: string | number | boolean | string[]) {
    const stringValue = String(newValue);
    value = stringValue;
    onUpdate(stringValue);
  }

  function togglePasswordVisibility() {
    showPassword = !showPassword;
  }
</script>

<div class={cn('form-control min-w-0', className)} {...rest}>
  <!-- Label rendered separately for proper button positioning -->
  {#if label}
    <label for={fieldId} class="label">
      <span class="label-text">
        {label}
        {#if required}
          <span class="text-error">*</span>
        {/if}
      </span>
    </label>
  {/if}

  <!-- Input wrapper for toggle button positioning -->
  <div class="relative">
    <input
      id={fieldId}
      type={showPassword ? 'text' : 'password'}
      name={fieldId}
      bind:value
      {placeholder}
      {required}
      {disabled}
      {autocomplete}
      onchange={e => handleChange(e.currentTarget.value)}
      class={cn('input input-sm w-full pr-10', error ? 'input-error' : '')}
    />

    <!-- Password reveal toggle - vertically centered on input -->
    {#if allowReveal}
      <button
        type="button"
        class="absolute right-2 top-1/2 -translate-y-1/2 flex items-center justify-center p-1 rounded-sm text-base-content/60 hover:text-base-content transition-colors disabled:opacity-50"
        onclick={togglePasswordVisibility}
        {disabled}
        aria-label={showPassword ? t('forms.labels.hidePassword') : t('forms.labels.showPassword')}
      >
        {#if showPassword}
          <EyeOff class="size-4" />
        {:else}
          <Eye class="size-4" />
        {/if}
      </button>
    {/if}
  </div>

  <!-- Help text rendered after input wrapper -->
  {#if helpText}
    <span class="help-text">{helpText}</span>
  {/if}

  <!-- Password strength indicator -->
  {#if showStrength && passwordStrength && value}
    <div class="mt-2" role="status" aria-live="polite">
      <div class="flex items-center justify-between mb-1">
        <span class="text-sm font-medium">{t('forms.password.strength.label')}</span>
        <span class="text-sm font-medium {passwordStrength?.color || ''}">
          {passwordStrength?.level
            ? t(`forms.password.strength.levels.${passwordStrength.level}`)
            : ''}
        </span>
      </div>

      <!-- Strength progress bar -->
      <div class="w-full bg-base-200 rounded-full h-2">
        <div
          class={cn('h-2 rounded-full transition-all duration-300', {
            'bg-error': passwordStrength?.level === 'weak',
            'bg-warning': passwordStrength?.level === 'fair',
            'bg-info': passwordStrength?.level === 'good',
            'bg-success': passwordStrength?.level === 'strong',
          })}
          style:width="{passwordStrength ? (passwordStrength.score / 4) * 100 : 0}%"
        ></div>
      </div>

      <!-- Feedback -->
      {#if passwordStrength?.feedback && passwordStrength.feedback.length > 0}
        <div class="mt-2">
          <div class="text-xs text-base-content opacity-70 mb-1">
            {t('forms.password.strength.suggestions.title')}
          </div>
          <ul class="text-xs text-base-content opacity-70 space-y-1">
            {#each passwordStrength.feedback as suggestion, index (index)}
              <li class="flex items-center gap-1">
                <div class="shrink-0">
                  <TriangleAlert class="size-3" />
                </div>
                {suggestion}
              </li>
            {/each}
          </ul>
        </div>
      {/if}
    </div>
  {/if}

  <!-- Error display -->
  {#if error}
    <div class="label">
      <span class="label-text-alt text-error">{error}</span>
    </div>
  {/if}
</div>
