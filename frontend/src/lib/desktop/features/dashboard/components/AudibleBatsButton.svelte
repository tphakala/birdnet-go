<!--
  AudibleBatsButton.svelte

  A button + popup that controls "Audible bats" playback for bat detections.
  Ultrasonic bat calls are slowed (time-expanded) into the human hearing range,
  producing a derived review copy of the audio. The popup only exposes the time
  expansion factor: volume gain and normalization are already covered by the
  regular Audio Settings popup (gain applies via the shared Web Audio graph
  regardless of source), and the chosen factor is always remembered.

  The component owns the UI preference only. The parent (AudioPlayer) owns the
  actual generation/playback of the derived audio and reports back via the
  `active`, `generating` and `error` props.

  Props:
  - active: boolean - Whether audible bats playback is currently active
  - generating: boolean - Whether the derived audio is being generated
  - error?: string | null - Error message from the last generation attempt
  - disabled?: boolean - Whether the control is disabled
  - disabledReason?: string | null - Explanation shown when disabled is true
  - onEnable: (settings) => void - Enable audible bats mode with the chosen settings
  - onDisable: () => void - Disable audible bats mode, returning to normal playback
  - onMenuOpen / onMenuClose - Open-menu tracking callbacks (mirror AudioSettingsButton)
-->
<script lang="ts" module>
  export interface AudibleBatsSettings {
    expansion: number;
  }
</script>

<script lang="ts">
  import { dropdown } from '$lib/utils/transitions';
  import { computeAnchorPosition, applyAnchorPosition } from '$lib/utils/anchorPosition';
  import { t } from '$lib/i18n';
  import BatIcon from '$lib/components/icons/BatIcon.svelte';
  import { getStoredValue, setStoredValue } from '$lib/utils/storage';

  interface Props {
    active: boolean;
    generating: boolean;
    error?: string | null;
    disabled?: boolean;
    disabledReason?: string | null;
    onEnable: (_settings: AudibleBatsSettings) => void;
    onDisable: () => void;
    onMenuOpen?: () => void;
    onMenuClose?: () => void;
  }

  let {
    active,
    generating,
    error = null,
    disabled = false,
    disabledReason = null,
    onEnable,
    onDisable,
    onMenuOpen,
    onMenuClose,
  }: Props = $props();

  // Constants
  const EXPANSION_OPTIONS = [5, 10, 16, 20] as const;
  const DEFAULT_EXPANSION = 5;
  // Gap in px between the trigger button and the popup.
  const MENU_OFFSET = 8;
  // localStorage key for the remembered time-expansion preference.
  const STORAGE_KEY = 'birdnet:audibleBats';

  // Generate a unique ID for this component instance (used by the disabled-reason description).
  const instanceId = Math.random().toString(36).slice(2, 9);
  const disabledDescId = `bat-disabled-reason-${instanceId}`;

  // Validate a persisted settings object before trusting it.
  function isStoredSettings(v: unknown): v is AudibleBatsSettings {
    if (typeof v !== 'object' || v === null) return false;
    return typeof (v as Record<string, unknown>).expansion === 'number';
  }

  // Load the remembered time-expansion factor (default: 5x).
  const stored = getStoredValue<AudibleBatsSettings | null>(
    STORAGE_KEY,
    null,
    (v): v is AudibleBatsSettings | null => v === null || isStoredSettings(v)
  );

  let expansion = $state(
    stored && EXPANSION_OPTIONS.includes(stored.expansion as (typeof EXPANSION_OPTIONS)[number])
      ? stored.expansion
      : DEFAULT_EXPANSION
  );

  let showSettings = $state(false);
  let buttonElement: HTMLButtonElement;
  // svelte-ignore non_reactive_update
  let menuElement: HTMLDivElement;

  const currentSettings = (): AudibleBatsSettings => ({ expansion });

  // The chosen time-expansion factor is always remembered.
  function persist() {
    setStoredValue(STORAGE_KEY, currentSettings());
  }

  function handleExpansionChange(value: number) {
    if (disabled || expansion === value) return;
    expansion = value;
    persist();
    // Changing the setting invalidates the active derived copy: drop back to
    // normal playback until the user presses Enable again.
    if (active) {
      onDisable();
    }
  }

  function handlePrimaryAction() {
    if (disabled || generating) return;
    if (active) {
      onDisable();
    } else {
      persist();
      onEnable(currentSettings());
    }
  }

  // Force-close the popup if it becomes disabled while open (e.g. a conflicting
  // mode activates elsewhere) so the user can't interact with a stale control.
  $effect(() => {
    if (disabled && showSettings) {
      closeMenu();
    }
  });

  function updateMenuPosition() {
    if (!menuElement || !buttonElement) return;
    const position = computeAnchorPosition({
      triggerRect: buttonElement.getBoundingClientRect(),
      floatingHeight: menuElement.offsetHeight,
      floatingWidth: menuElement.offsetWidth,
      offset: MENU_OFFSET,
      align: 'end',
    });
    applyAnchorPosition(menuElement, position);
    menuElement.style.zIndex = '9999';
  }

  function handleToggle(event: MouseEvent) {
    event.stopPropagation();
    if (disabled) return;
    showSettings = !showSettings;

    if (showSettings) {
      onMenuOpen?.();
      globalThis.requestAnimationFrame(() => {
        updateMenuPosition();
      });
    } else {
      onMenuClose?.();
    }
  }

  function closeMenu() {
    if (!showSettings) return;
    showSettings = false;
    onMenuClose?.();
  }

  function handleClickOutside(event: MouseEvent) {
    if (
      showSettings &&
      menuElement &&
      !menuElement.contains(event.target as Node) &&
      buttonElement &&
      !buttonElement.contains(event.target as Node)
    ) {
      closeMenu();
    }
  }

  function handleKeydown(event: KeyboardEvent) {
    if (showSettings && event.key === 'Escape') {
      closeMenu();
      buttonElement?.focus();
    }
  }

  $effect(() => {
    if (showSettings) {
      function handleResize() {
        updateMenuPosition();
      }

      document.addEventListener('click', handleClickOutside);
      document.addEventListener('keydown', handleKeydown);
      window.addEventListener('resize', handleResize);
      window.addEventListener('scroll', handleResize, true);

      return () => {
        document.removeEventListener('click', handleClickOutside);
        document.removeEventListener('keydown', handleKeydown);
        window.removeEventListener('resize', handleResize);
        window.removeEventListener('scroll', handleResize, true);
      };
    }
  });

  // Cleanup on unmount: notify parent if the menu is still open (mirrors AudioSettingsButton).
  $effect(() => {
    return () => {
      if (showSettings) {
        onMenuClose?.();
      }
    };
  });
</script>

<div>
  <button
    bind:this={buttonElement}
    onclick={handleToggle}
    class="bats-trigger"
    class:active={showSettings || active}
    aria-label={t('media.audio.audibleBats.title')}
    aria-haspopup="true"
    aria-expanded={showSettings}
    aria-describedby={disabled && disabledReason ? disabledDescId : undefined}
    title={disabled && disabledReason ? disabledReason : t('media.audio.audibleBats.title')}
    {disabled}
  >
    <BatIcon class="size-5" />
    {#if active}
      <span class="active-indicator" aria-hidden="true"></span>
    {/if}
  </button>
  {#if disabled && disabledReason}
    <span id={disabledDescId} class="sr-only">{disabledReason}</span>
  {/if}

  {#if showSettings}
    <div
      bind:this={menuElement}
      in:dropdown
      out:dropdown={{ duration: 100 }}
      class="settings-menu"
      onclick={e => e.stopPropagation()}
      onmousedown={e => e.stopPropagation()}
      ontouchstart={e => e.stopPropagation()}
      onkeydown={e => {
        if (e.key === 'Escape') {
          closeMenu();
          buttonElement?.focus();
        }
        e.stopPropagation();
      }}
      role="dialog"
      aria-label={t('media.audio.audibleBats.title')}
      tabindex="-1"
    >
      <div class="settings-title">{t('media.audio.audibleBats.title')}</div>
      <p class="settings-subtitle">{t('media.audio.audibleBats.subtitle')}</p>

      <!-- Time expansion -->
      <div class="setting-item">
        <span class="setting-label">{t('media.audio.audibleBats.timeExpansion')}</span>
        <div
          class="option-button-group"
          role="group"
          aria-label={t('media.audio.audibleBats.timeExpansion')}
        >
          {#each EXPANSION_OPTIONS as option (option)}
            <button
              class="option-button"
              class:active={expansion === option}
              onclick={() => handleExpansionChange(option)}
              disabled={disabled || undefined}
              aria-pressed={expansion === option}
            >
              {option}×
            </button>
          {/each}
        </div>
      </div>

      {#if error}
        <p class="settings-error" role="alert">{error}</p>
      {/if}

      <!-- Primary action -->
      <button
        class="action-button"
        class:is-active={active}
        onclick={handlePrimaryAction}
        disabled={disabled || generating}
        aria-busy={generating}
      >
        {#if generating}
          <span class="action-spinner" aria-hidden="true"></span>
          {t('media.audio.audibleBats.generating')}
        {:else if active}
          {t('media.audio.audibleBats.disable')}
        {:else}
          {t('media.audio.audibleBats.enable')}
        {/if}
      </button>
    </div>
  {/if}
</div>

<style>
  .bats-trigger {
    position: relative;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 2rem;
    height: 2rem;
    border-radius: 9999px;
    background-color: rgb(0 0 0 / 0.5);
    backdrop-filter: blur(4px);
    color: white;
    transition: background-color 0.15s ease;
  }

  .bats-trigger:hover {
    background-color: rgb(51 65 85 / 0.8);
  }

  .bats-trigger.active {
    background-color: rgb(59 130 246 / 0.6);
  }

  .bats-trigger:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .active-indicator {
    position: absolute;
    top: 2px;
    right: 2px;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background-color: rgb(59 130 246);
    box-shadow: 0 0 4px rgb(59 130 246 / 0.6);
    border: 1px solid white;
  }

  .settings-menu {
    position: fixed;
    min-width: 15rem;
    padding: 0.75rem;
    background-color: rgb(30 41 59 / 0.95);
    border: 1px solid rgb(51 65 85);
    border-radius: 0.5rem;
    box-shadow: 0 10px 25px rgb(0 0 0 / 0.4);
    backdrop-filter: blur(8px);
    z-index: 1100;
  }

  :global([data-theme='light']) .settings-menu {
    background-color: rgb(255 255 255 / 0.95);
    border-color: var(--color-base-300);
    box-shadow: 0 10px 25px rgb(0 0 0 / 0.15);
  }

  .settings-title {
    font-size: 0.75rem;
    font-weight: 600;
    color: rgb(148 163 184);
    text-transform: uppercase;
    letter-spacing: 0.05em;
    margin-bottom: 0.25rem;
    padding-bottom: 0.375rem;
    border-bottom: 1px solid rgb(51 65 85);
  }

  :global([data-theme='light']) .settings-title {
    color: var(--color-base-content);
    border-color: var(--color-base-300);
  }

  .settings-subtitle {
    font-size: 0.7rem;
    color: rgb(148 163 184);
    margin-bottom: 0.625rem;
    line-height: 1.3;
  }

  :global([data-theme='light']) .settings-subtitle {
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
  }

  .setting-item {
    margin-bottom: 0.625rem;
  }

  .setting-label {
    display: block;
    font-size: 0.75rem;
    color: rgb(226 232 240);
    margin-bottom: 0.25rem;
  }

  :global([data-theme='light']) .setting-label {
    color: var(--color-base-content);
  }

  /* Expansion option buttons */
  .option-button-group {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem;
  }

  .option-button {
    padding: 0.25rem 0.5rem;
    font-size: 0.7rem;
    font-weight: 500;
    color: rgb(148 163 184);
    background-color: rgb(51 65 85 / 0.5);
    border-radius: 0.25rem;
    transition: all 0.15s ease;
    font-variant-numeric: tabular-nums;
  }

  .option-button:hover {
    background-color: rgb(51 65 85);
    color: white;
  }

  .option-button.active {
    background-color: rgb(59 130 246);
    color: white;
  }

  .option-button:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  :global([data-theme='light']) .option-button {
    color: var(--color-base-content);
    background-color: var(--color-base-200);
  }

  :global([data-theme='light']) .option-button:hover {
    background-color: var(--color-base-300);
  }

  :global([data-theme='light']) .option-button.active {
    background-color: rgb(59 130 246);
    color: white;
  }

  .settings-error {
    font-size: 0.7rem;
    color: rgb(248 113 113);
    margin: 0.25rem 0 0.5rem;
    line-height: 1.3;
  }

  /* Primary action button */
  .action-button {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 0.375rem;
    width: 100%;
    padding: 0.4rem 0.5rem;
    margin-top: 0.375rem;
    font-size: 0.75rem;
    font-weight: 600;
    color: white;
    background-color: rgb(59 130 246);
    border-radius: 0.25rem;
    transition: all 0.15s ease;
  }

  .action-button:hover:not(:disabled) {
    background-color: rgb(37 99 235);
  }

  .action-button.is-active {
    background-color: rgb(51 65 85 / 0.6);
    color: rgb(226 232 240);
  }

  .action-button.is-active:hover:not(:disabled) {
    background-color: rgb(51 65 85);
  }

  .action-button:disabled {
    opacity: 0.7;
    cursor: not-allowed;
  }

  :global([data-theme='light']) .action-button.is-active {
    background-color: var(--color-base-200);
    color: var(--color-base-content);
  }

  :global([data-theme='light']) .action-button.is-active:hover:not(:disabled) {
    background-color: var(--color-base-300);
  }

  .action-spinner {
    width: 0.85rem;
    height: 0.85rem;
    border: 2px solid currentcolor;
    border-top-color: transparent;
    border-radius: 50%;
    animation: bat-spin 0.7s linear infinite;
  }

  @keyframes bat-spin {
    to {
      transform: rotate(360deg);
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .action-spinner {
      animation-duration: 1.5s;
    }
  }
</style>
