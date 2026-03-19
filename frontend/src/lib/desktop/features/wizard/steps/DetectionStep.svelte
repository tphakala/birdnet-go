<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { t } from '$lib/i18n';
  import { settingsActions, settingsStore } from '$lib/stores/settings';
  import { get } from 'svelte/store';
  import { getLogger } from '$lib/utils/logger';
  import { Scale, Target, Radio } from '@lucide/svelte';
  import type { WizardStepProps } from '../types';

  const logger = getLogger('DetectionStep');

  let { onValidChange }: WizardStepProps = $props();

  interface Preset {
    id: string;
    titleKey: string;
    descKey: string;
    threshold: number;
    icon: typeof Scale;
    recommended?: boolean;
  }

  const presets: Preset[] = [
    {
      id: 'balanced',
      titleKey: 'wizard.steps.detection.balanced',
      descKey: 'wizard.steps.detection.balancedDesc',
      threshold: 0.8,
      icon: Scale,
      recommended: true,
    },
    {
      id: 'high-accuracy',
      titleKey: 'wizard.steps.detection.highAccuracy',
      descKey: 'wizard.steps.detection.highAccuracyDesc',
      threshold: 0.9,
      icon: Target,
    },
    {
      id: 'high-sensitivity',
      titleKey: 'wizard.steps.detection.highSensitivity',
      descKey: 'wizard.steps.detection.highSensitivityDesc',
      threshold: 0.6,
      icon: Radio,
    },
  ];

  let selectedPreset = $state('balanced');
  let dirty = $state(false);

  $effect(() => {
    untrack(() => onValidChange?.(true));
  });

  onMount(() => {
    // Load current threshold and match to a preset
    const store = get(settingsStore);
    const currentThreshold = store?.formData?.birdnet?.threshold;
    if (currentThreshold !== undefined) {
      const match = presets.find(p => p.threshold === currentThreshold);
      if (match) {
        selectedPreset = match.id;
      }
    }
  });

  function selectPreset(id: string) {
    selectedPreset = id;
    dirty = true;
  }

  // Save on unmount — only if user made changes
  $effect(() => {
    return () => {
      if (!dirty) return;
      const preset = presets.find(p => p.id === selectedPreset);
      if (preset) {
        settingsActions.updateSection('birdnet', {
          threshold: preset.threshold,
        });
        settingsActions.saveSettings().catch(err => {
          logger.error('Failed to save detection settings', err);
        });
      }
    };
  });
</script>

<div class="space-y-4">
  <p class="text-sm text-[var(--color-base-content)] opacity-70">
    {t('wizard.steps.detection.description')}
  </p>

  <div class="space-y-3" role="radiogroup" aria-label={t('wizard.steps.detection.title')}>
    {#each presets as preset (preset.id)}
      {@const PresetIcon = preset.icon}
      <button
        type="button"
        role="radio"
        aria-checked={selectedPreset === preset.id}
        class="flex w-full items-start gap-3 rounded-lg border-2 p-4 text-left transition-colors {selectedPreset ===
        preset.id
          ? 'border-[var(--color-primary)] bg-[var(--color-primary)]/5'
          : 'border-[var(--border-200)] hover:border-[var(--border-300)]'}"
        onclick={() => selectPreset(preset.id)}
      >
        <PresetIcon class="mt-0.5 size-5 shrink-0 text-[var(--color-base-content)]" />
        <div class="flex-1">
          <div class="flex items-center gap-2">
            <span class="text-sm font-medium text-[var(--color-base-content)]">
              {t(preset.titleKey)}
            </span>
            {#if preset.recommended}
              <span
                class="rounded-full bg-[var(--color-primary)]/10 px-2 py-0.5 text-xs font-medium text-[var(--color-primary)]"
              >
                {t('wizard.steps.detection.balancedRecommended')}
              </span>
            {/if}
          </div>
          <p class="mt-0.5 text-xs text-[var(--color-base-content)] opacity-60">
            {t(preset.descKey)}
          </p>
          <p class="mt-1 text-xs font-mono text-[var(--color-base-content)] opacity-40">
            {t('wizard.steps.detection.threshold')}: {preset.threshold}
          </p>
        </div>
      </button>
    {/each}
  </div>

  <p class="rounded-lg bg-[var(--color-info)]/10 p-3 text-xs text-[var(--color-info)]">
    {t('wizard.steps.detection.fpFilterNote')}
  </p>
</div>
