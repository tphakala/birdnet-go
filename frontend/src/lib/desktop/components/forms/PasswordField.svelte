<script lang="ts">
  import FormField from './FormField.svelte';
  import { cn } from '$lib/utils/cn.js';
  import type { HTMLAttributes } from 'svelte/elements';
  import { alertIconsSvg, systemIcons } from '$lib/utils/icons';
  import { t } from '$lib/i18n';

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

<div class={cn('form-control', className)} {...rest}>
  <FormField
    type={showPassword ? 'text' : 'password'}
    name={name || 'password-field'}
    {label}
    bind:value
    {placeholder}
    {helpText}
    {required}
    {disabled}
    {autocomplete}
    onChange={handleChange}
    inputClassName={cn(
      'pr-12', // Make room for toggle button
      error ? 'input-error' : ''
    )}
  />

  <!-- Password reveal toggle -->
  {#if allowReveal}
    <div class="absolute inset-y-0 right-0 flex items-center pr-3">
      <button
        type="button"
        class="btn btn-ghost btn-sm btn-square"
        onclick={togglePasswordVisibility}
        {disabled}
        aria-label={showPassword ? t('forms.labels.hidePassword') : t('forms.labels.showPassword')}
      >
        {#if showPassword}
          {@html systemIcons.eyeOff}
        {:else}
          {@html systemIcons.eye}
        {/if}
      </button>
    </div>
  {/if}

  <!-- Password strength indicator -->
  {#if showStrength && passwordStrength && value}
    <div class="mt-2">
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
          <div class="text-xs text-base-content/70 mb-1">{t('forms.password.strength.suggestions.title')}</div>
          <ul class="text-xs text-base-content/70 space-y-1">
            {#each passwordStrength.feedback as suggestion}
              <li class="flex items-center gap-1">
                <div class="h-3 w-3 flex-shrink-0">
                  {@html alertIconsSvg.warningSmall}
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

<style>
  .form-control {
    position: relative;
  }
</style>
