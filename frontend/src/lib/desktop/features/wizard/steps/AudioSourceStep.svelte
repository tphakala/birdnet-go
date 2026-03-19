<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '$lib/i18n';
  import { api } from '$lib/utils/api';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import { settingsActions, type RealtimeSettings } from '$lib/stores/settings';
  import { Mic, Video } from '@lucide/svelte';
  import type { WizardStepProps } from '../types';
  import { getLogger } from '$lib/utils/logger';

  const logger = getLogger('AudioSourceStep');

  let { onValidChange }: WizardStepProps = $props();

  type SourceType = 'soundcard' | 'rtsp';

  let sourceType = $state<SourceType>('soundcard');
  let selectedDevice = $state('');
  let rtspUrl = $state('');
  let devices = $state<Array<{ value: string; label: string }>>([]);
  let devicesLoading = $state(true);

  let isValid = $derived(
    sourceType === 'soundcard' ? selectedDevice !== '' : rtspUrl.trim() !== ''
  );

  $effect(() => {
    onValidChange?.(isValid);
  });

  onMount(() => {
    api
      .get<Array<{ name: string; index: number; id: string }>>('/api/v2/system/audio/devices')
      .then(data => {
        devices = (data ?? []).map(d => ({
          value: d.name,
          label: d.name,
        }));
        if (devices.length === 0) {
          sourceType = 'rtsp';
        }
      })
      .catch(err => {
        logger.error('Failed to load audio devices', err);
        devices = [];
        sourceType = 'rtsp';
      })
      .finally(() => {
        devicesLoading = false;
      });
  });

  $effect(() => {
    return () => {
      if (sourceType === 'soundcard' && selectedDevice) {
        settingsActions.updateSection('realtime', {
          audio: { source: selectedDevice } as RealtimeSettings['audio'],
        });
      } else if (sourceType === 'rtsp' && rtspUrl.trim()) {
        settingsActions.updateSection('realtime', {
          rtsp: {
            streams: [
              {
                name: 'Stream 1',
                url: rtspUrl.trim(),
                type: 'rtsp' as const,
                transport: 'tcp' as const,
              },
            ],
          } as RealtimeSettings['rtsp'],
        });
      }
      settingsActions.saveSettings().catch(() => {});
    };
  });
</script>

<div class="space-y-5">
  <div>
    <label class="mb-2 block text-sm font-medium text-[var(--color-base-content)]">
      {t('wizard.steps.audioSource.sourceTypeLabel')}
    </label>
    <div class="grid grid-cols-2 gap-3">
      <button
        type="button"
        class="flex items-center gap-3 rounded-lg border-2 p-3 text-left transition-colors {sourceType ===
        'soundcard'
          ? 'border-[var(--color-primary)] bg-[var(--color-primary)]/5'
          : 'border-[var(--border-200)] hover:border-[var(--border-300)]'}"
        onclick={() => {
          sourceType = 'soundcard';
        }}
      >
        <Mic class="size-5 shrink-0 text-[var(--color-base-content)]" />
        <span class="text-sm font-medium text-[var(--color-base-content)]">
          {t('wizard.steps.audioSource.soundcard')}
        </span>
      </button>

      <button
        type="button"
        class="flex items-center gap-3 rounded-lg border-2 p-3 text-left transition-colors {sourceType ===
        'rtsp'
          ? 'border-[var(--color-primary)] bg-[var(--color-primary)]/5'
          : 'border-[var(--border-200)] hover:border-[var(--border-300)]'}"
        onclick={() => {
          sourceType = 'rtsp';
        }}
      >
        <Video class="size-5 shrink-0 text-[var(--color-base-content)]" />
        <span class="text-sm font-medium text-[var(--color-base-content)]">
          {t('wizard.steps.audioSource.rtspStream')}
        </span>
      </button>
    </div>
  </div>

  {#if sourceType === 'soundcard'}
    <div>
      <label class="mb-1 block text-sm font-medium text-[var(--color-base-content)]">
        {t('wizard.steps.audioSource.deviceLabel')}
      </label>
      {#if devicesLoading}
        <p class="text-sm text-[var(--color-base-content)] opacity-60">
          {t('wizard.steps.audioSource.deviceLoading')}
        </p>
      {:else if devices.length === 0}
        <p class="rounded-lg bg-[var(--color-info)]/10 p-3 text-sm text-[var(--color-info)]">
          {t('wizard.steps.audioSource.noDevicesFound')}
        </p>
      {:else}
        <SelectDropdown
          options={devices}
          value={selectedDevice}
          searchable={true}
          onChange={value => {
            if (typeof value === 'string') selectedDevice = value;
          }}
        />
      {/if}
    </div>
  {/if}

  {#if sourceType === 'rtsp'}
    <div>
      <label class="mb-1 block text-sm font-medium text-[var(--color-base-content)]">
        {t('wizard.steps.audioSource.rtspUrlLabel')}
      </label>
      <p class="mb-2 text-xs text-[var(--color-base-content)] opacity-60">
        {t('wizard.steps.audioSource.rtspUrlHelp')}
      </p>
      <TextInput
        bind:value={rtspUrl}
        placeholder={t('wizard.steps.audioSource.rtspUrlPlaceholder')}
      />
    </div>
  {/if}

  <p class="text-xs text-[var(--color-base-content)] opacity-50">
    {t('wizard.steps.audioSource.additionalSourcesHint')}
  </p>
</div>
