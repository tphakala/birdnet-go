<script lang="ts">
  import { scheme, type SchemeId } from '$lib/stores/scheme';
  import { logoStyle } from '$lib/stores/logoStyle';
  import { settingsActions, dashboardSettings } from '$lib/stores/settings';
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import { Check } from '@lucide/svelte';
  import LogoBadge from '$lib/components/LogoBadge.svelte';
  import { SCHEME_GRADIENT_MAP, type LogoVariant } from '$lib/stores/logoVariant';
  import { radioGroupKeys } from '$lib/utils/radioGroupKeys';
  import { generateId } from '$lib/utils/uuid';

  interface Props {
    /** Why the picker is blocked. Giving a reason is what blocks it, and the text is shown beside the title. */
    disabledReason?: string;
  }

  let { disabledReason }: Props = $props();

  const GROUP_LABEL_ID = generateId('color-scheme-label');
  const REASON_ID = generateId('color-scheme-reason');

  let blocked = $derived(disabledReason !== undefined);

  const customColorsStore = scheme.customColors;

  /** A fixed scheme previews its primary colour in each theme; the custom scheme previews a gradient. */
  interface SchemeOption {
    id: SchemeId;
    labelKey: string;
    color?: string;
    darkColor?: string;
    gradient?: string;
  }

  const schemes: ReadonlyArray<SchemeOption> = [
    {
      id: 'blue',
      labelKey: 'settings.appearance.schemeBlue',
      color: '#2563eb',
      darkColor: '#3b82f6',
    },
    {
      id: 'forest',
      labelKey: 'settings.appearance.schemeForest',
      color: '#047857',
      darkColor: '#059669',
    },
    {
      id: 'amber',
      labelKey: 'settings.appearance.schemeAmber',
      color: '#b45309',
      darkColor: '#f59e0b',
    },
    {
      id: 'violet',
      labelKey: 'settings.appearance.schemeViolet',
      color: '#7c3aed',
      darkColor: '#8b5cf6',
    },
    {
      id: 'rose',
      labelKey: 'settings.appearance.schemeRose',
      color: '#e11d48',
      darkColor: '#fb7185',
    },
    {
      id: 'custom',
      labelKey: 'settings.appearance.schemeCustom',
      gradient: 'linear-gradient(135deg, #f43f5e, #8b5cf6, #3b82f6, #22c55e)',
    },
  ];

  function selectScheme(id: SchemeId) {
    if (blocked) return;
    scheme.setScheme(id);
    // Persist to server settings so all visitors see the chosen scheme
    if ($dashboardSettings) {
      settingsActions.updateSection('realtime', {
        dashboard: { ...$dashboardSettings, colorScheme: id },
      });
    }
  }

  function updateCustomPrimary(e: Event) {
    const target = e.target as HTMLInputElement;
    const newColors = { ...$customColorsStore, primary: target.value };
    scheme.setCustomColors(newColors);
    if ($dashboardSettings) {
      settingsActions.updateSection('realtime', {
        dashboard: { ...$dashboardSettings, customColors: newColors },
      });
    }
  }

  function updateCustomAccent(e: Event) {
    const target = e.target as HTMLInputElement;
    const newColors = { ...$customColorsStore, accent: target.value };
    scheme.setCustomColors(newColors);
    if ($dashboardSettings) {
      settingsActions.updateSection('realtime', {
        dashboard: { ...$dashboardSettings, customColors: newColors },
      });
    }
  }

  // Logo preview variant based on current style and scheme
  let logoPreviewVariant: LogoVariant = $derived(
    // eslint-disable-next-line security/detect-object-injection -- $scheme is a controlled color scheme identifier
    $logoStyle === 'solid' ? 'solid' : (SCHEME_GRADIENT_MAP[$scheme] ?? 'scheme')
  );

  function toggleLogoStyle() {
    if (blocked) return;
    const newStyle = $logoStyle === 'gradient' ? 'solid' : 'gradient';
    logoStyle.setStyle(newStyle);
    // Persist to server settings so all visitors see the chosen style
    if ($dashboardSettings) {
      settingsActions.updateSection('realtime', {
        dashboard: { ...$dashboardSettings, logoStyle: newStyle },
      });
    }
  }
</script>

<div class="space-y-4">
  <div class="flex items-start justify-between gap-3">
    <div>
      <span id={GROUP_LABEL_ID} class="text-sm font-medium text-[var(--color-base-content)]">
        {t('settings.appearance.colorScheme')}
      </span>
      <p class="text-xs text-[var(--color-base-content)]/60 mt-0.5">
        {t('settings.appearance.colorSchemeDescription')}
      </p>
    </div>
    {#if blocked}
      <p id={REASON_ID} class="shrink-0 text-xs text-[var(--color-base-content)]/70">
        {disabledReason}
      </p>
    {/if}
  </div>

  <div
    class="flex flex-wrap gap-3"
    role="radiogroup"
    aria-labelledby={GROUP_LABEL_ID}
    use:radioGroupKeys
  >
    {#each schemes as opt (opt.id)}
      {@const fixed = opt.gradient === undefined}
      <button
        type="button"
        role="radio"
        class="group flex flex-col items-center gap-1.5 rounded-lg focus-ring aria-disabled:cursor-not-allowed"
        onclick={() => selectScheme(opt.id)}
        aria-checked={$scheme === opt.id}
        tabindex={$scheme === opt.id ? 0 : -1}
        aria-disabled={blocked ? 'true' : undefined}
        aria-describedby={blocked ? REASON_ID : undefined}
        data-testid={`color-scheme-${opt.id}`}
      >
        <div
          class={cn(
            'relative size-10 rounded-full border-2 transition-all forced-color-adjust-none group-aria-disabled:opacity-50',
            fixed && 'bg-[var(--swatch)] dark:bg-[var(--swatch-dark)]',
            $scheme === opt.id
              ? 'border-[var(--color-base-content)] scale-110 shadow-md'
              : 'border-transparent group-not-aria-disabled:hover:border-[var(--color-base-content)]/30 group-not-aria-disabled:hover:scale-105'
          )}
          style:background={opt.gradient}
          style:--swatch={opt.color}
          style:--swatch-dark={opt.darkColor}
        >
          {#if $scheme === opt.id}
            <div class="absolute inset-0 flex items-center justify-center">
              <Check
                class={cn(
                  'size-5 drop-shadow-md',
                  fixed ? 'text-[var(--color-primary-content)]' : 'text-white'
                )}
              />
            </div>
          {/if}
        </div>
        <span
          class={cn(
            'text-xs group-aria-disabled:opacity-50',
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
          disabled={blocked}
          aria-describedby={blocked ? REASON_ID : undefined}
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
          disabled={blocked}
          aria-describedby={blocked ? REASON_ID : undefined}
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
      aria-disabled={blocked ? 'true' : undefined}
      aria-describedby={blocked ? REASON_ID : undefined}
      class={cn(
        'relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors focus-visible:outline-2 focus-visible:outline-[var(--color-primary)] focus-visible:outline-offset-2 aria-disabled:opacity-50 aria-disabled:focus-visible:opacity-100 aria-disabled:cursor-not-allowed',
        $logoStyle === 'gradient'
          ? 'bg-[var(--color-primary)]'
          : 'bg-[var(--color-base-content)]/20'
      )}
      onclick={toggleLogoStyle}
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
