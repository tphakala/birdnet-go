<script lang="ts">
  import Card from '$lib/desktop/components/ui/Card.svelte';
  import SupportDumpCard from '$lib/desktop/components/ui/SupportDumpCard.svelte';
  import { Bug, ExternalLink, Info, ClipboardCopy, CheckCircle } from '@lucide/svelte';
  import { onMount } from 'svelte';
  import { t } from '$lib/i18n';
  import { api } from '$lib/utils/api';
  import { copyToClipboard, COPY_FEEDBACK_TIMEOUT_MS } from '$lib/utils/clipboard';
  import { GITHUB_ISSUES_URL } from '$lib/utils/externalUrls';

  interface HealthResponse {
    version?: string;
    build_date?: string;
  }

  interface SystemInfoResponse {
    os_display?: string;
    architecture?: string;
    system_model?: string;
    environment?: string;
  }

  interface SystemInfo {
    version: string;
    buildDate: string;
    os: string;
    architecture: string;
    hardware: string;
    environment: string;
  }

  let systemInfo = $state<SystemInfo>({
    version: '',
    buildDate: '',
    os: '',
    architecture: '',
    hardware: '',
    environment: '',
  });

  let copied = $state(false);
  let copyTimer: ReturnType<typeof setTimeout> | null = null;

  async function fetchSystemInfo() {
    const [healthResult, sysResult] = await Promise.allSettled([
      api.get<HealthResponse>('/api/v2/health'),
      api.get<SystemInfoResponse>('/api/v2/system/info'),
    ]);

    if (healthResult.status === 'fulfilled') {
      systemInfo.version = healthResult.value.version || '';
      systemInfo.buildDate = healthResult.value.build_date || '';
    }

    if (sysResult.status === 'fulfilled') {
      systemInfo.os = sysResult.value.os_display || '';
      systemInfo.architecture = sysResult.value.architecture || '';
      systemInfo.hardware = sysResult.value.system_model || '';
      systemInfo.environment = sysResult.value.environment || '';
    }
  }

  async function copySystemInfo() {
    const lines = [
      `Version: ${systemInfo.version || 'unknown'}`,
      `Build: ${systemInfo.buildDate || 'unknown'}`,
      `OS: ${systemInfo.os || 'unknown'}`,
      `Architecture: ${systemInfo.architecture || 'unknown'}`,
    ];
    if (systemInfo.hardware) lines.push(`Hardware: ${systemInfo.hardware}`);
    if (systemInfo.environment) lines.push(`Environment: ${systemInfo.environment}`);
    lines.push(`Browser: ${navigator.userAgent}`);

    const ok = await copyToClipboard(lines.join('\n'));
    if (!ok) return;
    copied = true;
    if (copyTimer !== null) clearTimeout(copyTimer);
    copyTimer = setTimeout(() => {
      copied = false;
      copyTimer = null;
    }, COPY_FEEDBACK_TIMEOUT_MS);
  }

  onMount(() => {
    fetchSystemInfo();
    return () => {
      if (copyTimer !== null) clearTimeout(copyTimer);
    };
  });
</script>

<div class="col-span-12 space-y-4">
  <!-- Page Header -->
  <Card className="bg-[var(--color-base-100)] shadow-sm">
    <div class="flex flex-col items-center text-center">
      <div
        class="w-20 h-20 rounded-full bg-gradient-to-b from-[var(--surface-200)] to-[var(--color-base-100)] flex items-center justify-center border border-[var(--border-100)]"
      >
        <Bug class="size-10 text-[var(--color-primary)]" />
      </div>
      <div class="mt-3">
        <h1 class="text-3xl font-bold">{t('reportBug.title')}</h1>
        <p class="text-[var(--color-base-content)] opacity-70 text-base mt-2">
          {t('reportBug.subtitle')}
        </p>
      </div>
    </div>
  </Card>

  <!-- Step 1: System Information -->
  <Card title={t('reportBug.systemInfo.title')} className="bg-[var(--color-base-100)] shadow-sm">
    <p class="text-[var(--color-base-content)] opacity-80 mb-4">
      {t('reportBug.systemInfo.description')}
    </p>

    <div class="rounded-lg bg-[var(--color-base-200)] p-4 font-mono text-sm space-y-1">
      <div>
        <span class="text-[var(--color-base-content)] opacity-60"
          >{t('reportBug.systemInfo.version')}:</span
        >
        <span class="ml-2">{systemInfo.version || '...'}</span>
      </div>
      <div>
        <span class="text-[var(--color-base-content)] opacity-60"
          >{t('reportBug.systemInfo.buildDate')}:</span
        >
        <span class="ml-2">{systemInfo.buildDate || '...'}</span>
      </div>
      <div>
        <span class="text-[var(--color-base-content)] opacity-60"
          >{t('reportBug.systemInfo.os')}:</span
        >
        <span class="ml-2">{systemInfo.os || '...'}</span>
      </div>
      <div>
        <span class="text-[var(--color-base-content)] opacity-60"
          >{t('reportBug.systemInfo.architecture')}:</span
        >
        <span class="ml-2">{systemInfo.architecture || '...'}</span>
      </div>
      {#if systemInfo.hardware}
        <div>
          <span class="text-[var(--color-base-content)] opacity-60"
            >{t('reportBug.systemInfo.hardware')}:</span
          >
          <span class="ml-2">{systemInfo.hardware}</span>
        </div>
      {/if}
      {#if systemInfo.environment}
        <div>
          <span class="text-[var(--color-base-content)] opacity-60"
            >{t('reportBug.systemInfo.environment')}:</span
          >
          <span class="ml-2">{systemInfo.environment}</span>
        </div>
      {/if}
    </div>

    <div class="mt-3">
      <button
        onclick={copySystemInfo}
        class="inline-flex items-center gap-2 px-3 py-1.5 text-sm font-medium rounded-md transition-all bg-[var(--color-base-200)] text-[var(--color-base-content)] hover:bg-[var(--color-base-300)] focus-visible:outline-2 focus-visible:outline-[var(--color-primary)] focus-visible:outline-offset-2"
      >
        {#if copied}
          <CheckCircle class="size-4 text-[var(--color-success)]" />
          {t('reportBug.systemInfo.copied')}
        {:else}
          <ClipboardCopy class="size-4" />
          {t('reportBug.systemInfo.copy')}
        {/if}
      </button>
    </div>
  </Card>

  <!-- Step 2: What to Include -->
  <Card title={t('reportBug.whatToInclude.title')} className="bg-[var(--color-base-100)] shadow-sm">
    <p class="text-[var(--color-base-content)] opacity-80 mb-4">
      {t('reportBug.whatToInclude.description')}
    </p>

    <div class="space-y-3">
      <div class="flex items-start gap-3 p-3 rounded-lg bg-[var(--color-base-200)]">
        <div
          class="flex items-center justify-center w-7 h-7 rounded-full bg-[var(--color-primary)] text-[var(--color-primary-content)] text-sm font-bold shrink-0"
        >
          1
        </div>
        <div>
          <p class="font-medium text-sm">{t('reportBug.whatToInclude.step1.title')}</p>
          <p class="text-sm text-[var(--color-base-content)] opacity-70 mt-0.5">
            {t('reportBug.whatToInclude.step1.description')}
          </p>
        </div>
      </div>

      <div class="flex items-start gap-3 p-3 rounded-lg bg-[var(--color-base-200)]">
        <div
          class="flex items-center justify-center w-7 h-7 rounded-full bg-[var(--color-primary)] text-[var(--color-primary-content)] text-sm font-bold shrink-0"
        >
          2
        </div>
        <div>
          <p class="font-medium text-sm">{t('reportBug.whatToInclude.step2.title')}</p>
          <p class="text-sm text-[var(--color-base-content)] opacity-70 mt-0.5">
            {t('reportBug.whatToInclude.step2.description')}
          </p>
        </div>
      </div>

      <div class="flex items-start gap-3 p-3 rounded-lg bg-[var(--color-base-200)]">
        <div
          class="flex items-center justify-center w-7 h-7 rounded-full bg-[var(--color-primary)] text-[var(--color-primary-content)] text-sm font-bold shrink-0"
        >
          3
        </div>
        <div>
          <p class="font-medium text-sm">{t('reportBug.whatToInclude.step3.title')}</p>
          <p class="text-sm text-[var(--color-base-content)] opacity-70 mt-0.5">
            {t('reportBug.whatToInclude.step3.description')}
          </p>
        </div>
      </div>

      <div class="flex items-start gap-3 p-3 rounded-lg bg-[var(--color-base-200)]">
        <div
          class="flex items-center justify-center w-7 h-7 rounded-full bg-[var(--color-primary)] text-[var(--color-primary-content)] text-sm font-bold shrink-0"
        >
          4
        </div>
        <div>
          <p class="font-medium text-sm">{t('reportBug.whatToInclude.step4.title')}</p>
          <p class="text-sm text-[var(--color-base-content)] opacity-70 mt-0.5">
            {t('reportBug.whatToInclude.step4.description')}
          </p>
        </div>
      </div>
    </div>
  </Card>

  <!-- Step 3: Open GitHub Issue -->
  <Card title={t('reportBug.openIssue.title')} className="bg-[var(--color-base-100)] shadow-sm">
    <p class="text-[var(--color-base-content)] opacity-80 mb-4">
      {t('reportBug.openIssue.description')}
    </p>

    <a
      href={GITHUB_ISSUES_URL}
      target="_blank"
      rel="noopener noreferrer"
      class="inline-flex items-center gap-2 px-4 py-2 text-sm font-medium rounded-md transition-all bg-[var(--color-primary)] text-[var(--color-primary-content)] hover:bg-[var(--color-primary-hover)] focus-visible:outline-2 focus-visible:outline-[var(--color-primary)] focus-visible:outline-offset-2"
      aria-label={t('navigation.reportBugAriaLabel')}
    >
      <Bug class="size-4" />
      {t('reportBug.openIssue.button')}
      <ExternalLink class="size-3 opacity-60" />
    </a>
  </Card>

  <!-- Step 4: Generate Support Dump -->
  <Card title={t('reportBug.supportDump.title')} className="bg-[var(--color-base-100)] shadow-sm">
    <div
      class="flex items-start gap-3 p-3 rounded-lg bg-[color-mix(in_srgb,var(--color-info)_10%,transparent)] mb-4"
    >
      <Info class="size-5 shrink-0 text-[var(--color-info)]" />
      <p class="text-sm text-[var(--color-base-content)] opacity-80">
        {t('reportBug.supportDump.description')}
      </p>
    </div>
    <SupportDumpCard />
  </Card>
</div>
