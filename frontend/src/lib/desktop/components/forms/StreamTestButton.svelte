<!--
  Stream Test Button Component

  Purpose: Test stream connectivity and audio properties via the probe API.
  Shows stream info (sample rate, codec, channels) and model-specific
  compatibility badges when relevant.

  @component
-->
<script lang="ts">
  import { Search, CircleCheck, CircleX, Loader2, AlertTriangle } from '@lucide/svelte';
  import { api } from '$lib/utils/api';
  import { t } from '$lib/i18n';

  interface ModelOption {
    id: string;
    name: string;
    category: string;
    minSampleRate?: number;
    recommendedSampleRate?: number;
  }

  interface ProbeResult {
    sampleRate: number;
    channels: number;
    codec: string;
    batCompatible: boolean;
    warnings: string[];
  }

  interface Props {
    url: string;
    models: ModelOption[];
    selectedModels: string[];
    disabled?: boolean;
    onResult?: (_result: ProbeResult | null) => void;
  }

  let { url, models, selectedModels, disabled = false, onResult }: Props = $props();

  let isTesting = $state(false);
  let testResult = $state<ProbeResult | null>(null);
  let testError = $state<string | null>(null);
  let testController: AbortController | null = null;

  let hasBatModel = $derived(
    selectedModels.some(id => {
      const model = models.find(m => m.id === id);
      return model?.category === 'bat';
    })
  );

  async function testStream() {
    if (!url.trim()) return;
    testController?.abort();
    testController = new AbortController();
    isTesting = true;
    testResult = null;
    testError = null;
    onResult?.(null);
    try {
      const result = await api.post<ProbeResult>(
        '/api/v2/streams/test',
        { url: url.trim() },
        { signal: testController.signal }
      );
      testResult = result;
      onResult?.(result);
    } catch (err: unknown) {
      if (err instanceof Error && err.name === 'AbortError') return;
      testError = err instanceof Error ? err.message : '';
    } finally {
      isTesting = false;
    }
  }

  let prevUrl = '';
  $effect(() => {
    if (url !== prevUrl) {
      if (prevUrl !== '') {
        testResult = null;
        testError = null;
        onResult?.(null);
      }
      prevUrl = url;
    }
    return () => {
      testController?.abort();
    };
  });
</script>

<div class="space-y-2">
  <div class="flex items-center gap-2">
    <button
      type="button"
      class="inline-flex items-center gap-1.5 h-8 px-3 text-sm font-medium rounded-lg border border-[var(--border-200)] bg-[var(--color-base-200)] hover:bg-[var(--color-base-300)] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
      onclick={testStream}
      disabled={isTesting || !url.trim() || disabled}
    >
      {#if isTesting}
        <Loader2 class="size-4 animate-spin" />
        {t('settings.audio.streams.probe.probing')}
      {:else}
        <Search class="size-4" />
        {t('settings.audio.streams.probe.button')}
      {/if}
    </button>

    {#if testResult}
      <div class="flex items-center gap-2 text-xs">
        <span class="font-mono text-[var(--color-base-content)]">
          {testResult.sampleRate / 1000} kHz, {testResult.codec}, {testResult.channels}ch
        </span>
        {#if hasBatModel}
          {#if testResult.batCompatible}
            <span
              class="inline-flex items-center gap-1 px-1.5 py-0.5 rounded font-medium bg-[var(--color-success)]/15 text-[var(--color-success)]"
            >
              <CircleCheck class="size-3" />
              {t('settings.audio.streams.probe.batCompatible')}
            </span>
          {:else}
            <span
              class="inline-flex items-center gap-1 px-1.5 py-0.5 rounded font-medium bg-[var(--color-error)]/15 text-[var(--color-error)]"
            >
              <CircleX class="size-3" />
              {t('settings.audio.streams.probe.batIncompatible')}
            </span>
          {/if}
        {/if}
      </div>
    {/if}
  </div>

  {#if testResult && hasBatModel && !testResult.batCompatible && testResult.sampleRate >= 96000}
    <div
      class="flex items-start gap-2 p-2.5 rounded-lg text-xs leading-relaxed bg-[color-mix(in_srgb,var(--color-warning)_10%,transparent)] border border-[color-mix(in_srgb,var(--color-warning)_30%,transparent)]"
      role="status"
    >
      <AlertTriangle class="size-3.5 shrink-0 mt-0.5 text-[var(--color-warning)]" />
      <span class="text-[var(--color-base-content)]">
        {t('settings.audio.streams.codecWarning.lossy')}
      </span>
    </div>
  {/if}

  {#if testError !== null}
    <p class="text-xs text-[var(--color-error)]">
      {t('settings.audio.streams.probe.error')}{testError ? `: ${testError}` : ''}
    </p>
  {/if}
</div>
