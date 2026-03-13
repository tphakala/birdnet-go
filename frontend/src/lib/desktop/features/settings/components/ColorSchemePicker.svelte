<script lang="ts">
  import { scheme, type SchemeId } from '$lib/stores/scheme';
  import { logoStyle } from '$lib/stores/logoStyle';
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import { Check } from '@lucide/svelte';
  import LogoBadge from '$lib/components/LogoBadge.svelte';

  interface Props {
    disabled?: boolean;
  }

  let { disabled = false }: Props = $props();

  const customColorsStore = scheme.customColors;

  interface SchemeOption {
    id: SchemeId;
    labelKey: string;
    color: string;
    gradient?: string;
  }

  const schemes: ReadonlyArray<SchemeOption> = [
    { id: 'blue', labelKey: 'settings.appearance.schemeBlue', color: '#2563eb' },
    { id: 'forest', labelKey: 'settings.appearance.schemeForest', color: '#047857' },
    { id: 'amber', labelKey: 'settings.appearance.schemeAmber', color: '#d97706' },
    { id: 'violet', labelKey: 'settings.appearance.schemeViolet', color: '#7c3aed' },
    { id: 'rose', labelKey: 'settings.appearance.schemeRose', color: '#e11d48' },
    {
      id: 'custom',
      labelKey: 'settings.appearance.schemeCustom',
      color: '',
      gradient: 'linear-gradient(135deg, #f43f5e, #8b5cf6, #3b82f6, #22c55e)',
    },
  ];

  function selectScheme(id: SchemeId) {
    if (disabled) return;
    scheme.setScheme(id);
  }

  function updateCustomPrimary(e: Event) {
    const target = e.target as HTMLInputElement;
    scheme.setCustomColors({ ...$customColorsStore, primary: target.value });
  }

  function updateCustomAccent(e: Event) {
    const target = e.target as HTMLInputElement;
    scheme.setCustomColors({ ...$customColorsStore, accent: target.value });
  }

  // Logo preview variant based on current style and scheme
  let logoPreviewVariant = $derived(
    $logoStyle === 'solid' ? 'solid' : $scheme === 'blue' ? 'ocean' : 'scheme'
  ) as 'ocean' | 'scheme' | 'solid';

  function toggleLogoStyle() {
    if (disabled) return;
    logoStyle.setStyle($logoStyle === 'gradient' ? 'solid' : 'gradient');
  }
</script>

<div class="space-y-4">
  <div>
    <span class="text-sm font-medium text-[var(--color-base-content)]">
      {t('settings.appearance.colorScheme')}
    </span>
    <p class="text-xs text-[var(--color-base-content)]/60 mt-0.5">
      {t('settings.appearance.colorSchemeDescription')}
    </p>
  </div>

  <div
    class="flex flex-wrap gap-3"
    role="radiogroup"
    aria-label={t('settings.appearance.colorScheme')}
  >
    {#each schemes as opt (opt.id)}
      <button
        type="button"
        role="radio"
        class={cn(
          'group flex flex-col items-center gap-1.5',
          disabled && 'opacity-50 cursor-not-allowed'
        )}
        onclick={() => selectScheme(opt.id)}
        aria-label={t(opt.labelKey)}
        aria-checked={$scheme === opt.id}
        {disabled}
      >
        <div
          class={cn(
            'relative size-10 rounded-full border-2 transition-all',
            $scheme === opt.id
              ? 'border-[var(--color-base-content)] scale-110 shadow-md'
              : 'border-transparent hover:border-[var(--color-base-content)]/30 hover:scale-105'
          )}
          style={opt.gradient
            ? `background: ${opt.gradient}`
            : `background-color: ${opt.id === 'custom' ? $customColorsStore.primary : opt.color}`}
        >
          {#if $scheme === opt.id}
            <div class="absolute inset-0 flex items-center justify-center">
              <Check class="size-5 text-white drop-shadow-md" />
            </div>
          {/if}
        </div>
        <span
          class={cn(
            'text-xs',
            $scheme === opt.id
              ? 'font-medium text-[var(--color-base-content)]'
              : 'text-[var(--color-base-content)]/60'
          )}
        >
          {t(opt.labelKey)}
        </span>
      </button>
    {/each}
  </div>

  {#if $scheme === 'custom'}
    <div
      class="mt-4 flex flex-wrap gap-6 rounded-lg border border-[var(--color-base-content)]/10 bg-[var(--surface-200)] p-4"
    >
      <div class="flex items-center gap-3">
        <input
          type="color"
          value={$customColorsStore.primary}
          oninput={updateCustomPrimary}
          class="size-9 cursor-pointer rounded border border-[var(--color-base-content)]/20 p-0.5"
          aria-label={t('settings.appearance.customPrimary')}
          {disabled}
        />
        <div>
          <span class="text-sm font-medium text-[var(--color-base-content)]">
            {t('settings.appearance.customPrimary')}
          </span>
          <p class="text-xs text-[var(--color-base-content)]/50 font-mono">
            {$customColorsStore.primary}
          </p>
        </div>
      </div>
      <div class="flex items-center gap-3">
        <input
          type="color"
          value={$customColorsStore.accent}
          oninput={updateCustomAccent}
          class="size-9 cursor-pointer rounded border border-[var(--color-base-content)]/20 p-0.5"
          aria-label={t('settings.appearance.customAccent')}
          {disabled}
        />
        <div>
          <span class="text-sm font-medium text-[var(--color-base-content)]">
            {t('settings.appearance.customAccent')}
          </span>
          <p class="text-xs text-[var(--color-base-content)]/50 font-mono">
            {$customColorsStore.accent}
          </p>
        </div>
      </div>
    </div>
  {/if}

  <!-- Logo style toggle -->
  <div
    class="mt-4 flex items-center justify-between rounded-lg border border-[var(--color-base-content)]/10 bg-[var(--surface-200)] p-4"
  >
    <div class="flex items-center gap-3">
      <LogoBadge size="sm" variant={logoPreviewVariant} />
      <div>
        <span class="text-sm font-medium text-[var(--color-base-content)]">
          {t('settings.appearance.logoGradient')}
        </span>
        <p class="text-xs text-[var(--color-base-content)]/60">
          {t('settings.appearance.logoGradientDescription')}
        </p>
      </div>
    </div>
    <button
      type="button"
      role="switch"
      aria-checked={$logoStyle === 'gradient'}
      aria-label={t('settings.appearance.logoGradient')}
      class={cn(
        'relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors',
        $logoStyle === 'gradient'
          ? 'bg-[var(--color-primary)]'
          : 'bg-[var(--color-base-content)]/20',
        disabled && 'opacity-50 cursor-not-allowed'
      )}
      onclick={toggleLogoStyle}
      {disabled}
    >
      <span
        class={cn(
          'pointer-events-none inline-block size-5 rounded-full bg-white shadow-lg ring-0 transition-transform',
          $logoStyle === 'gradient' ? 'translate-x-5' : 'translate-x-0'
        )}
      ></span>
    </button>
  </div>
</div>
