<script lang="ts">
  import Card from '$lib/desktop/components/ui/Card.svelte';
  import SupportDumpCard from '$lib/desktop/components/ui/SupportDumpCard.svelte';
  import { Bug, ExternalLink, Info, ClipboardCopy, CheckCircle } from '@lucide/svelte';
  import { onMount } from 'svelte';
  import { t } from '$lib/i18n';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { GITHUB_ISSUES_URL } from '$lib/utils/externalUrls';

  interface SystemInfo {
    version: string;
    buildDate: string;
    os: string;
  }

  let systemInfo = $state<SystemInfo>({
    version: '',
    buildDate: '',
    os: '',
  });

  let copied = $state(false);

  async function fetchSystemInfo() {
    try {
      const response = await fetch(buildAppUrl('/api/v2/health'));
      if (response.ok) {
        const data = await response.json();
        systemInfo.version = data.version || '';
        systemInfo.buildDate = data.build_date || '';
        systemInfo.os = data.os || '';
      }
    } catch {
      // Non-critical
    }
  }

  function copySystemInfo() {
    const text = [
      `Version: ${systemInfo.version || 'unknown'}`,
      `Build: ${systemInfo.buildDate || 'unknown'}`,
      `OS: ${systemInfo.os || 'unknown'}`,
      `Browser: ${navigator.userAgent}`,
    ].join('\n');
    navigator.clipboard.writeText(text).then(() => {
      copied = true;
      setTimeout(() => {
        copied = false;
      }, 2000);
    });
  }

  onMount(() => {
    fetchSystemInfo();
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

  <!-- Step 3: Generate Support Dump -->
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

  <!-- Step 4: Open GitHub Issue -->
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
</div>
