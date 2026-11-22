<!--
  PlaybackDefaultsModal - edits the values every clip player starts with
  (default gain, denoise and high-pass cutoff), from the player toolbar.

  The dialog edits a local draft, filled from the saved settings when it opens
  (and again when settings finish loading while it is open), and writes the
  configuration only when Save is pressed: one dashboard PATCH with the changed
  keys only. Opening, closing, Cancel, Escape, Retry and "Use current values"
  write nothing.

  It is portaled to document.body, so it covers the viewport and stacks as a
  sibling of any Modal its opener sits in, instead of being laid out inside
  that Modal's box.
-->
<script lang="ts">
  import { onDestroy, tick, untrack } from 'svelte';
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
  import Button from '$lib/desktop/components/ui/Button.svelte';
  import InlineSlider from '$lib/desktop/components/forms/InlineSlider.svelte';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import type { SelectOption } from '$lib/desktop/components/forms/SelectDropdown.types';
  import LoadingSpinner from '$lib/desktop/components/ui/LoadingSpinner.svelte';
  import { t } from '$lib/i18n';
  import { settingsActions, settingsDataLoaded, settingsStore } from '$lib/stores/settings';
  import { toastActions } from '$lib/stores/toast';
  import { isDenoisePresetId } from '$lib/utils/denoisePresets';
  import { loggers } from '$lib/utils/logger';
  import {
    PLAYBACK_DEFAULT_ITEMS,
    changedPlaybackDefaults,
    clampPlaybackGain,
    highPassFromStored,
    playbackDefaultsPatch,
    readPlaybackDefaults,
    type PlaybackDefaultValues,
  } from '$lib/utils/playbackDefaults';
  import { portal } from '$lib/utils/portal';
  import { useUnsavedChangesGuard } from '$lib/utils/useUnsavedChangesGuard.svelte';
  import { generateId } from '$lib/utils/uuid';

  interface Props {
    isOpen: boolean;
    /** The values the player is set to now, for "Use current values" */
    current: PlaybackDefaultValues;
    onClose: () => void;
    /** Called after a successful save with the items that changed */
    onSaved?: (_changed: Partial<PlaybackDefaultValues>) => void;
  }

  let { isOpen, current, onClose, onSaved }: Props = $props();

  const logger = loggers.ui;

  // SelectDropdown shows its placeholder for an empty value, so the "off" option
  // (id '') gets a value of its own inside the dialog
  const DENOISE_OFF_VALUE = 'off';

  const descriptionId = generateId('playback-defaults-description');
  const useCurrentHelpId = generateId('playback-defaults-use-current-help');
  const footerStatusId = generateId('playback-defaults-status');
  const unavailableId = generateId('playback-defaults-unavailable');

  // The saved values the draft is compared with
  let baseline = $state<PlaybackDefaultValues>(readPlaybackDefaults(undefined));
  let draft = $state<PlaybackDefaultValues>(readPlaybackDefaults(undefined));
  let saving = $state(false);
  let errorMessage = $state('');
  let announcement = $state('');
  // Bumped by every announcement and every clear, so an announcement still
  // waiting for its tick never lands after a newer one
  let announcementSeq = 0;
  let destroyed = false;

  let loaded = $derived($settingsDataLoaded);
  let loading = $derived($settingsStore.isLoading);
  let useCurrentArea = $state<HTMLElement>();
  // Export-stage denoise wins over the playback default: the server does not
  // denoise clips that were denoised when saved
  let exportDenoiseOn = $derived(
    $settingsStore.originalData.realtime?.audio?.export?.denoise?.enabled === true
  );
  let changed = $derived(changedPlaybackDefaults(draft, baseline));
  let dirty = $derived(Object.keys(changed).length > 0);
  let canSave = $derived(loaded && dirty && !saving);

  let denoiseOptions = $derived<SelectOption[]>(
    PLAYBACK_DEFAULT_ITEMS.flatMap(item =>
      item.kind === 'select'
        ? item.options.map(option => ({
            value: option.id === '' ? DENOISE_OFF_VALUE : option.id,
            label: t(option.labelKey),
          }))
        : []
    )
  );

  // Without loaded settings the body says why, and Save points at that text
  let footerStatus = $derived.by(() => {
    if (saving) return t('settings.actions.saving');
    if (!loaded) return '';
    if (dirty) return t('settings.actions.unsavedChanges');
    return t('settings.actions.noChangesToSave');
  });
  let saveBlockedReasonId = $derived(loaded ? footerStatusId : unavailableId);

  // Browser Back or closing the tab with an unsaved draft asks first; moves inside
  // the app are already blocked by the dialog
  useUnsavedChangesGuard(() => isOpen && dirty);

  // Each opening starts from the saved values, and so does the moment settings
  // finish loading while the dialog is open. Other store changes are not tracked,
  // so a save made elsewhere cannot move a control under the user.
  $effect.pre(() => {
    if (!isOpen) return;
    const ready = loaded;
    untrack(() => {
      const saved = readPlaybackDefaults(ready ? $settingsStore.originalData.realtime : undefined);
      baseline = { ...saved };
      draft = { ...saved };
      errorMessage = '';
      clearAnnouncement();
    });
  });

  onDestroy(() => {
    destroyed = true;
  });

  function setDenoise(value: string | string[]) {
    if (typeof value !== 'string') return;
    draft.denoise = isDenoisePresetId(value) ? value : '';
  }

  async function announce(message: string) {
    const seq = ++announcementSeq;
    // Cleared first, so the same sentence is announced again on a second press
    announcement = '';
    await tick();
    if (seq === announcementSeq) announcement = message;
  }

  function clearAnnouncement() {
    announcementSeq++;
    announcement = '';
  }

  function useCurrentValues() {
    if (saving) return;
    draft.gainDb = clampPlaybackGain(current.gainDb);
    // While export denoise is on, the denoise default cannot be changed here
    if (!exportDenoiseOn) {
      draft.denoise = isDenoisePresetId(current.denoise) ? current.denoise : '';
    }
    draft.highPassHz = highPassFromStored(current.highPassHz);
    void announce(t('components.audioPlayer.defaults.copied'));
  }

  // Reads settings again (a GET only); the fields appear once they load
  async function retryLoad() {
    if (loading) return;
    void announce(t('common.ui.loadingSettings'));
    try {
      await settingsActions.loadSettings();
    } catch (error) {
      logger.warn('Loading settings for the playback defaults failed', {
        component: 'PlaybackDefaultsModal',
        error: error instanceof Error ? error.message : String(error),
      });
      if (isOpen && !destroyed) void announce(t('components.audioPlayer.defaults.unavailable'));
      return;
    }
    clearAnnouncement();
    // The Retry button is gone now. Focus that went with it falls to the page: put it
    // on the first field. Focus the user moved elsewhere, or a closed dialog, is left alone.
    await tick();
    if (isOpen && document.activeElement === document.body) {
      useCurrentArea?.querySelector<HTMLElement>('button')?.focus();
    }
  }

  async function save() {
    if (!canSave) return;
    const toSave = { ...changed };
    saving = true;
    errorMessage = '';
    void announce(t('settings.actions.saving'));
    try {
      await settingsActions.saveSection(
        'dashboard',
        playbackDefaultsPatch(toSave, $settingsStore.originalData.realtime?.dashboard)
      );
    } catch (error) {
      logger.error('Saving the playback defaults failed', error, {
        component: 'PlaybackDefaultsModal',
      });
      const detail = error instanceof Error && error.message ? ` ${error.message}` : '';
      clearAnnouncement();
      errorMessage = `${t('components.audioPlayer.defaults.saveFailed')}${detail}`;
      // The player can close the dialog, or unmount it, while the request runs; the
      // inline alert is not seen then
      if (!isOpen || destroyed) toastActions.error(errorMessage);
      saving = false;
      return;
    }
    saving = false;
    clearAnnouncement();
    toastActions.success(t('components.audioPlayer.defaults.saved'));
    // The player this dialog belongs to may be gone by now
    if (destroyed) return;
    onSaved?.(toSave);
    onClose();
  }
</script>

<div class="contents" use:portal>
  <Modal
    {isOpen}
    title={t('components.audioPlayer.defaults.title')}
    size="lg"
    scrollBody
    loading={saving}
    closeOnBackdrop={!dirty}
    aria-describedby={descriptionId}
    {onClose}
  >
    {#snippet children()}
      <div class="space-y-5">
        <p id={descriptionId} class="text-sm text-[var(--color-base-content)]/80">
          {t('components.audioPlayer.defaults.description')}
        </p>

        {#if !loaded}
          <div class="space-y-3">
            <p
              id={unavailableId}
              class={loading
                ? 'text-sm text-[var(--color-base-content)]/80'
                : 'text-sm text-[var(--text-error)]'}
            >
              {loading
                ? t('common.ui.loadingSettings')
                : t('components.audioPlayer.defaults.unavailable')}
            </p>
            <Button
              variant="default"
              size="sm"
              aria-disabled={loading ? 'true' : undefined}
              aria-describedby={unavailableId}
              aria-busy={loading}
              onclick={retryLoad}
            >
              {#if loading}
                <LoadingSpinner size="xs" color="text-current" aria-hidden="true" />
              {/if}
              {t('common.ui.retry')}
            </Button>
          </div>
        {:else}
          <div bind:this={useCurrentArea}>
            <Button
              variant="default"
              size="sm"
              aria-describedby={saving ? footerStatusId : useCurrentHelpId}
              aria-disabled={saving ? 'true' : undefined}
              onclick={useCurrentValues}
            >
              {t('components.audioPlayer.defaults.useCurrent')}
            </Button>
            <p id={useCurrentHelpId} class="help-text mt-1">
              {t('components.audioPlayer.defaults.useCurrentHelp')}
            </p>
          </div>

          <div class="space-y-5 border-t border-[var(--color-base-300)] pt-5">
            {#each PLAYBACK_DEFAULT_ITEMS as item (item.key)}
              {#if item.kind === 'slider'}
                <InlineSlider
                  label={t(item.labelKey)}
                  helpText={t(item.helpKey)}
                  value={draft[item.key]}
                  onUpdate={value => (draft[item.key] = value)}
                  min={item.min}
                  max={item.max}
                  step={item.step}
                  formatValue={item.format}
                  disabled={saving}
                />
              {:else}
                <SelectDropdown
                  label={t(item.labelKey)}
                  helpText={exportDenoiseOn
                    ? t('settings.userInterface.audioPlayback.denoiseExportPrecedence')
                    : t(item.helpKey)}
                  options={denoiseOptions}
                  value={draft.denoise === '' ? DENOISE_OFF_VALUE : draft.denoise}
                  onChange={setDenoise}
                  groupBy={false}
                  menuSize="sm"
                  disabled={saving || exportDenoiseOn}
                />
              {/if}
            {/each}
          </div>
        {/if}

        {#if errorMessage}
          <p class="text-sm text-[var(--text-error)]" role="alert">{errorMessage}</p>
        {/if}
        <p class="sr-only" role="status">{announcement}</p>
      </div>
    {/snippet}

    {#snippet footer()}
      <p
        id={footerStatusId}
        class="mr-auto min-w-0 self-center text-sm text-[var(--color-base-content)]/70"
      >
        {footerStatus}
      </p>
      <!-- Kept on one line: the status text beside them wraps instead -->
      <Button
        variant="ghost"
        size="md"
        className="whitespace-nowrap"
        aria-disabled={saving ? 'true' : undefined}
        aria-describedby={saving ? footerStatusId : undefined}
        onclick={() => {
          if (!saving) onClose();
        }}
      >
        {t('common.buttons.cancel')}
      </Button>
      <Button
        variant="primary"
        size="md"
        className="whitespace-nowrap"
        aria-disabled={!canSave ? 'true' : undefined}
        aria-describedby={!canSave ? saveBlockedReasonId : undefined}
        aria-busy={saving}
        onclick={save}
      >
        {#if saving}
          <LoadingSpinner size="xs" color="text-current" aria-hidden="true" />
          {t('settings.actions.saving')}
        {:else}
          {t('settings.actions.save')}
        {/if}
      </Button>
    {/snippet}
  </Modal>
</div>
