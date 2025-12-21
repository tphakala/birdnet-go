<script lang="ts">
  import { cn } from '$lib/utils/cn.js';
  import { t } from '$lib/i18n';

  interface SecuritySettings {
    enabled: boolean;
    accessAllowed: boolean;
  }

  interface Props {
    className?: string;
    code: number | string;
    title: string;
    message?: string;
    stackTrace?: string;
    security?: SecuritySettings;
  }

  let {
    className = '',
    code,
    title,
    message = '',
    stackTrace = '',
    security = { enabled: false, accessAllowed: true },
  }: Props = $props();

  let showDetails = $derived(!security.enabled || security.accessAllowed);
  let hasStackTrace = $derived(stackTrace && stackTrace.trim().length > 0);
</script>

<svelte:head>
  <title>{code} - {title}</title>
</svelte:head>

<div class={cn('col-span-12 min-h-[60vh] flex items-center justify-center', className)}>
  <div class="text-center p-8 rounded-lg bg-base-100 shadow-lg max-w-4xl w-full">
    <h1 class="text-6xl font-bold text-base-content mb-4">{code}</h1>
    <h2 class="text-3xl font-semibold text-base-content opacity-70 mb-4">{title}</h2>

    <!-- Error details -->
    <div class="mt-8 text-left">
      {#if message}
        <h3 class="text-2xl font-semibold text-base-content mb-2">
          {t('error.generic.errorDetails')}
        </h3>
        <pre
          class="bg-base-200 p-4 rounded-sm overflow-x-auto text-sm text-base-content font-mono">{message}</pre>
      {/if}

      {#if hasStackTrace && showDetails}
        <h3 class="text-2xl font-semibold text-base-content mt-4 mb-2">
          {t('error.generic.stackTrace')}
        </h3>
        <pre
          class="bg-base-200 p-4 rounded-sm overflow-x-auto text-sm text-base-content font-mono">{stackTrace}</pre>
      {/if}
    </div>

    <!-- Link Buttons -->
    <div class="mt-8 space-x-4">
      <a
        href="/"
        class="btn btn-primary normal-case text-base font-semibold transition duration-300"
      >
        {t('common.goToDashboard')}
      </a>
      {#if showDetails}
        <a
          href="https://github.com/tphakala/birdnet-go/issues"
          class="btn btn-accent normal-case text-base font-semibold transition duration-300"
        >
          {t('common.reportIssue')}
        </a>
      {:else}
        <a
          href="/login"
          class="btn btn-secondary normal-case text-base font-semibold transition duration-300"
        >
          {t('common.loginToViewDetails')}
        </a>
      {/if}
    </div>
  </div>
</div>
