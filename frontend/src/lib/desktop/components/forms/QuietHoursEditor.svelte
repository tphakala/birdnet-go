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
  import { Moon } from '@lucide/svelte';
  import SelectDropdown from './SelectDropdown.svelte';
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

  function handleEnabledToggle(event: Event) {
    const target = event.target as HTMLInputElement;
    update({ enabled: target.checked });
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

  function handleStartOffsetChange(event: Event) {
    const target = event.target as HTMLInputElement;
    const val = parseInt(target.value, 10);
    update({ startOffset: isNaN(val) ? 0 : val });
  }

  function handleEndOffsetChange(event: Event) {
    const target = event.target as HTMLInputElement;
    const val = parseInt(target.value, 10);
    update({ endOffset: isNaN(val) ? 0 : val });
  }

  const modeOptions = [
    { value: 'fixed', label: t('settings.audio.quietHours.modeFixed') },
    { value: 'solar', label: t('settings.audio.quietHours.modeSolar') },
  ];

  const solarEventOptions = [
    { value: 'sunset', label: t('settings.audio.quietHours.sunset') },
    { value: 'sunrise', label: t('settings.audio.quietHours.sunrise') },
  ];

  // Toggle styling (matching ToggleField pattern)
  const toggleClasses = `
    appearance-none w-10 h-5 rounded-full cursor-pointer transition-all relative
    bg-[var(--color-base-300)]
    before:content-[''] before:absolute before:top-0.5 before:left-0.5
    before:w-4 before:h-4 before:rounded-full before:bg-[var(--color-base-100)]
    before:shadow-sm before:transition-transform
    checked:bg-[var(--color-primary)] checked:before:translate-x-5
    focus-visible:outline-2 focus-visible:outline-[var(--color-primary)] focus-visible:outline-offset-2
    disabled:opacity-50 disabled:cursor-not-allowed
  `.trim();
</script>

<div class="space-y-3">
  <!-- Enable Toggle -->
  <div class="flex items-center justify-between">
    <label for="{idPrefix}-enabled" class="flex items-center gap-2 cursor-pointer">
      <Moon class="size-4 text-[var(--color-base-content)] opacity-60" />
      <span class="text-xs font-medium text-[var(--color-base-content)]">
        {t('settings.audio.quietHours.title')}
      </span>
    </label>
    <input
      id="{idPrefix}-enabled"
      type="checkbox"
      class={toggleClasses}
      checked={safeConfig.enabled}
      {disabled}
      onchange={handleEnabledToggle}
    />
  </div>

  {#if safeConfig.enabled}
    <div class="space-y-3 pl-6 border-l-2 border-[var(--border-200)]">
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
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="block py-0.5" for="{idPrefix}-start-time">
              <span class="text-xs font-medium text-[var(--color-base-content)]">
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
            <label class="block py-0.5" for="{idPrefix}-end-time">
              <span class="text-xs font-medium text-[var(--color-base-content)]">
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
        <p class="text-xs text-[var(--color-base-content)] opacity-50">
          {t('settings.audio.quietHours.fixedHint')}
        </p>
      {:else}
        <!-- Solar Mode -->
        <div class="space-y-3">
          <!-- Start Event -->
          <div class="grid grid-cols-2 gap-3">
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
            <div>
              <label class="block py-0.5" for="{idPrefix}-start-offset">
                <span class="text-xs font-medium text-[var(--color-base-content)]">
                  {t('settings.audio.quietHours.offsetMinutes')}
                </span>
              </label>
              <input
                id="{idPrefix}-start-offset"
                type="number"
                value={safeConfig.startOffset}
                min={MIN_SOLAR_OFFSET_MINUTES}
                max={MAX_SOLAR_OFFSET_MINUTES}
                {disabled}
                oninput={handleStartOffsetChange}
                class="w-full h-8 px-2 text-sm rounded-lg border border-[var(--border-200)] bg-[var(--color-base-200)] text-[var(--color-base-content)] focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] focus:border-transparent transition-colors"
              />
            </div>
          </div>

          <!-- End Event -->
          <div class="grid grid-cols-2 gap-3">
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
            <div>
              <label class="block py-0.5" for="{idPrefix}-end-offset">
                <span class="text-xs font-medium text-[var(--color-base-content)]">
                  {t('settings.audio.quietHours.offsetMinutes')}
                </span>
              </label>
              <input
                id="{idPrefix}-end-offset"
                type="number"
                value={safeConfig.endOffset}
                min={MIN_SOLAR_OFFSET_MINUTES}
                max={MAX_SOLAR_OFFSET_MINUTES}
                {disabled}
                oninput={handleEndOffsetChange}
                class="w-full h-8 px-2 text-sm rounded-lg border border-[var(--border-200)] bg-[var(--color-base-200)] text-[var(--color-base-content)] focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] focus:border-transparent transition-colors"
              />
            </div>
          </div>
          <p class="text-xs text-[var(--color-base-content)] opacity-50">
            {t('settings.audio.quietHours.solarHint')}
          </p>
        </div>
      {/if}
    </div>
  {/if}
</div>
