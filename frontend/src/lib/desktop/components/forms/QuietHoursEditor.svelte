<!--
  Quiet Hours Editor Component

  Purpose: Reusable editor for quiet hours configuration. Used in both
  stream cards and sound card settings to configure time windows during
  which audio capture is paused to reduce CPU usage.

  Features:
  - Toggle to enable/disable quiet hours
  - Mode selector: fixed times or solar-based
  - Fixed mode: HH:MM time inputs for start and end
  - Solar mode: event selector (sunrise/sunset) with minute offset
  - Compact inline layout for stream cards
  - Accessible form controls with proper labels

  @component
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import SelectDropdown from './SelectDropdown.svelte';
  import NumberField from './NumberField.svelte';
  import Checkbox from './Checkbox.svelte';
  import type { QuietHoursConfig } from '$lib/stores/settings';
  import { defaultQuietHoursConfig } from '$lib/stores/settings';

  interface Props {
    config: QuietHoursConfig;
    onChange: (_config: QuietHoursConfig) => void;
    disabled?: boolean;
    /** Unique identifier prefix for form element IDs */
    idPrefix?: string;
  }

  let { config, onChange, disabled = false, idPrefix = 'qh' }: Props = $props();

  /** Maximum offset in minutes from a solar event (3 hours) */
  const MAX_SOLAR_OFFSET_MINUTES = 180;
  /** Minimum offset in minutes from a solar event (-3 hours) */
  const MIN_SOLAR_OFFSET_MINUTES = -180;

  // Ensure we always have a valid config with defaults
  let safeConfig = $derived({
    ...defaultQuietHoursConfig,
    ...config,
  });

  function update(partial: Partial<QuietHoursConfig>) {
    onChange({ ...safeConfig, ...partial });
  }

  function handleEnabledToggle(enabled: boolean) {
    update({ enabled });
  }

  function handleModeChange(value: string | string[]) {
    const mode = value as 'fixed' | 'solar';
    update({ mode });
  }

  function handleStartTimeChange(event: Event) {
    const target = event.target as HTMLInputElement;
    update({ startTime: target.value });
  }

  function handleEndTimeChange(event: Event) {
    const target = event.target as HTMLInputElement;
    update({ endTime: target.value });
  }

  function handleStartEventChange(value: string | string[]) {
    update({ startEvent: value as 'sunrise' | 'sunset' });
  }

  function handleEndEventChange(value: string | string[]) {
    update({ endEvent: value as 'sunrise' | 'sunset' });
  }

  function handleStartOffsetChange(value: number) {
    update({ startOffset: value });
  }

  function handleEndOffsetChange(value: number) {
    update({ endOffset: value });
  }

  const modeOptions = $derived([
    { value: 'fixed', label: t('settings.audio.quietHours.modeFixed') },
    { value: 'solar', label: t('settings.audio.quietHours.modeSolar') },
  ]);

  const solarEventOptions = $derived([
    { value: 'sunset', label: t('settings.audio.quietHours.sunset') },
    { value: 'sunrise', label: t('settings.audio.quietHours.sunrise') },
  ]);
</script>

<div class="space-y-3">
  <!-- Enable Quiet Hours -->
  <Checkbox
    checked={safeConfig.enabled}
    label={t('settings.audio.quietHours.title')}
    {disabled}
    onchange={handleEnabledToggle}
  />

  {#if safeConfig.enabled}
    <div class="space-y-4 transition-opacity duration-200">
      <!-- Mode Selector -->
      <SelectDropdown
        value={safeConfig.mode}
        label={t('settings.audio.quietHours.modeLabel')}
        options={modeOptions}
        onChange={handleModeChange}
        {disabled}
        groupBy={false}
        menuSize="sm"
        size="sm"
      />

      {#if safeConfig.mode === 'fixed'}
        <!-- Fixed Time Inputs -->
        <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div>
            <label class="block py-1" for="{idPrefix}-start-time">
              <span class="text-sm font-medium text-[var(--color-base-content)]">
                {t('settings.audio.quietHours.startTime')}
              </span>
            </label>
            <input
              id="{idPrefix}-start-time"
              type="time"
              value={safeConfig.startTime}
              {disabled}
              oninput={handleStartTimeChange}
              class="w-full h-8 px-2 text-sm rounded-lg border border-[var(--border-200)] bg-[var(--color-base-200)] text-[var(--color-base-content)] focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] focus:border-transparent transition-colors"
            />
          </div>
          <div>
            <label class="block py-1" for="{idPrefix}-end-time">
              <span class="text-sm font-medium text-[var(--color-base-content)]">
                {t('settings.audio.quietHours.endTime')}
              </span>
            </label>
            <input
              id="{idPrefix}-end-time"
              type="time"
              value={safeConfig.endTime}
              {disabled}
              oninput={handleEndTimeChange}
              class="w-full h-8 px-2 text-sm rounded-lg border border-[var(--border-200)] bg-[var(--color-base-200)] text-[var(--color-base-content)] focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] focus:border-transparent transition-colors"
            />
          </div>
        </div>
        <p class="help-text">
          {t('settings.audio.quietHours.fixedHint')}
        </p>
      {:else}
        <!-- Solar Mode -->
        <div class="space-y-4">
          <!-- Start Event -->
          <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
            <SelectDropdown
              value={safeConfig.startEvent}
              label={t('settings.audio.quietHours.startEvent')}
              options={solarEventOptions}
              onChange={handleStartEventChange}
              {disabled}
              groupBy={false}
              menuSize="sm"
              size="sm"
            />
            <NumberField
              label={t('settings.audio.quietHours.offsetMinutes')}
              value={safeConfig.startOffset}
              onUpdate={handleStartOffsetChange}
              min={MIN_SOLAR_OFFSET_MINUTES}
              max={MAX_SOLAR_OFFSET_MINUTES}
              step={1}
              placeholder="0"
              {disabled}
            />
          </div>

          <!-- End Event -->
          <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
            <SelectDropdown
              value={safeConfig.endEvent}
              label={t('settings.audio.quietHours.endEvent')}
              options={solarEventOptions}
              onChange={handleEndEventChange}
              {disabled}
              groupBy={false}
              menuSize="sm"
              size="sm"
            />
            <NumberField
              label={t('settings.audio.quietHours.offsetMinutes')}
              value={safeConfig.endOffset}
              onUpdate={handleEndOffsetChange}
              min={MIN_SOLAR_OFFSET_MINUTES}
              max={MAX_SOLAR_OFFSET_MINUTES}
              step={1}
              placeholder="0"
              {disabled}
            />
          </div>
          <p class="help-text">
            {t('settings.audio.quietHours.solarHint')}
          </p>
        </div>
      {/if}
    </div>
  {/if}
</div>
