<script lang="ts">
  import FormField from './FormField.svelte';
  import { cn } from '$lib/utils/cn.js';
  import type { HTMLAttributes } from 'svelte/elements';

  interface Props extends HTMLAttributes<HTMLDivElement> {
    label: string;
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
      feedback.push('At least 8 characters');
    }

    // Character variety checks
    if (/[a-z]/.test(value) && /[A-Z]/.test(value)) {
      score += 1;
    } else {
      feedback.push('Mix of uppercase and lowercase');
    }

    if (/\d/.test(value)) {
      score += 1;
    } else {
      feedback.push('At least one number');
    }

    if (/[^a-zA-Z0-9]/.test(value)) {
      score += 1;
    } else {
      feedback.push('At least one special character');
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
    name={label.toLowerCase().replace(/\s+/g, '-')}
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
        aria-label={showPassword ? 'Hide password' : 'Show password'}
      >
        {#if showPassword}
          <!-- Eye slash icon -->
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-5 w-5"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M13.875 18.825A10.05 10.05 0 0112 19c-4.478 0-8.268-2.943-9.543-7a9.97 9.97 0 011.563-3.029m5.858.908a3 3 0 114.243 4.243M9.878 9.878l4.242 4.242M9.878 9.878L3 3m6.878 6.878L21 21"
            />
          </svg>
        {:else}
          <!-- Eye icon -->
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-5 w-5"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"
            />
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M2.458 12C3.732 7.943 7.523 5 12 5c4.478 0 8.268 2.943 9.542 7-1.274 4.057-5.064 7-9.542 7-4.477 0-8.268-2.943-9.542-7z"
            />
          </svg>
        {/if}
      </button>
    </div>
  {/if}

  <!-- Password strength indicator -->
  {#if showStrength && passwordStrength && value}
    <div class="mt-2">
      <div class="flex items-center justify-between mb-1">
        <span class="text-sm font-medium">Password Strength:</span>
        <span class="text-sm font-medium {passwordStrength?.color || ''}">
          {passwordStrength?.level
            ? passwordStrength.level.charAt(0).toUpperCase() + passwordStrength.level.slice(1)
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
          <div class="text-xs text-base-content/70 mb-1">Suggestions:</div>
          <ul class="text-xs text-base-content/70 space-y-1">
            {#each passwordStrength.feedback as suggestion}
              <li class="flex items-center gap-1">
                <svg
                  xmlns="http://www.w3.org/2000/svg"
                  class="h-3 w-3"
                  fill="none"
                  viewBox="0 0 24 24"
                  stroke="currentColor"
                >
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    stroke-width="2"
                    d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.964-.833-2.732 0L3.082 16.5c-.77.833.192 2.5 1.732 2.5z"
                  />
                </svg>
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
