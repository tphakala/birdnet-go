<!--
  Low Noise Auto Suspend Editor Component

  Purpose: Reusable editor for per-source low-noise auto-suspend configuration.
  Used in sound card add/edit forms to control suspend/resume thresholds and
  debounce frame counts for analysis suspension.

  Features:
  - Toggle to enable/disable low-noise auto-suspend
  - Threshold controls for suspend/resume (0-100)
  - Frame count controls for suspend/resume debounce
  - Inline validation for hysteresis (resume > suspend)
  - Reusable controlled component API (config + onChange)

  @component
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import NumberField from './NumberField.svelte';
  import Checkbox from './Checkbox.svelte';
  import {
    defaultLowNoiseAutoSuspendSettings,
    type LowNoiseAutoSuspendSettings,
  } from '$lib/stores/settings';
  import { hasValidLowNoiseAutoSuspendThresholds } from '$lib/utils/lowNoiseAutoSuspend';

  interface Props {
    config: LowNoiseAutoSuspendSettings;
    onChange: (_config: LowNoiseAutoSuspendSettings) => void;
    disabled?: boolean;
  }

  let { config, onChange, disabled = false }: Props = $props();

  let safeConfig = $derived({
    ...defaultLowNoiseAutoSuspendSettings,
    ...config,
  });

  const validationError = $derived.by(() => {
    if (!hasValidLowNoiseAutoSuspendThresholds(safeConfig)) {
      return t('settings.audio.lowNoiseAutoSuspend.validation.resumeGreaterThanSuspend');
    }
    return '';
  });

  function update(partial: Partial<LowNoiseAutoSuspendSettings>) {
    onChange({ ...safeConfig, ...partial });
  }

  function handleEnabledToggle(enabled: boolean) {
    update({ enabled });
  }
</script>

<div class="space-y-3">
  <Checkbox
    checked={safeConfig.enabled}
    label={t('settings.audio.lowNoiseAutoSuspend.enable')}
    {disabled}
    onchange={handleEnabledToggle}
  />

  {#if safeConfig.enabled}
    <div class="space-y-4 transition-opacity duration-200">
      <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
        <NumberField
          label={t('settings.audio.lowNoiseAutoSuspend.suspendThreshold')}
          value={safeConfig.suspendThreshold}
          onUpdate={value => update({ suspendThreshold: value })}
          min={0}
          max={100}
          step={1}
          {disabled}
        />
        <NumberField
          label={t('settings.audio.lowNoiseAutoSuspend.resumeThreshold')}
          value={safeConfig.resumeThreshold}
          onUpdate={value => update({ resumeThreshold: value })}
          min={0}
          max={100}
          step={1}
          {disabled}
        />
        <NumberField
          label={t('settings.audio.lowNoiseAutoSuspend.minSuspendFrames')}
          value={safeConfig.minSuspendFrames}
          onUpdate={value => update({ minSuspendFrames: value })}
          min={0}
          step={1}
          {disabled}
        />
        <NumberField
          label={t('settings.audio.lowNoiseAutoSuspend.minResumeFrames')}
          value={safeConfig.minResumeFrames}
          onUpdate={value => update({ minResumeFrames: value })}
          min={0}
          step={1}
          {disabled}
        />
      </div>

      {#if validationError}
        <p class="text-xs text-[var(--color-error)]">{validationError}</p>
      {/if}
    </div>
  {/if}
</div>
