<script lang="ts">
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import ErrorAlert from '$lib/desktop/components/ui/ErrorAlert.svelte';
  import { Check, Globe, ShieldCheck, Info, Download, XCircle, CircleCheck } from '@lucide/svelte';
  import { onMount } from 'svelte';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { api } from '$lib/utils/api';

  import type { HTMLAttributes } from 'svelte/elements';

  interface Props extends HTMLAttributes<HTMLDivElement> {
    className?: string;
  }

  let { className = '', ...rest }: Props = $props();

  const logger = loggers.settings;

  const instanceId = Math.random().toString(36).slice(2, 10);
  const githubIssueInputId = `githubIssueNumber-${instanceId}`;
  const userMessageInputId = `userMessage-${instanceId}`;

  let timers: ReturnType<typeof setTimeout>[] = [];

  function clearTimers() {
    for (const timer of timers) {
      clearTimeout(timer);
    }
    timers = [];
  }

  function safeTimeout(fn: () => void, ms: number) {
    const id = setTimeout(fn, ms);
    timers.push(id);
    return id;
  }

  // Support dump generation state
  let generating = $state(false);
  let statusMessage = $state('');
  let statusType = $state<'info' | 'success' | 'error'>('info');
  let progressPercent = $state(0);

  // Support dump options
  let supportDump = $state({
    includeLogs: true,
    includeConfig: true,
    includeSystemInfo: true,
    githubIssueNumber: '',
    userMessage: '',
    uploadToSentry: true,
  });

  let normalizedGithubIssue = $derived(supportDump.githubIssueNumber.trim());
  let hasValidGithubIssue = $derived(/^#?\d+$/.test(normalizedGithubIssue));

  let generateButtonDisabled = $derived(
    generating ||
      (!supportDump.includeLogs && !supportDump.includeConfig && !supportDump.includeSystemInfo) ||
      (supportDump.uploadToSentry && !hasValidGithubIssue)
  );

  // API State Management
  interface ApiState<T> {
    loading: boolean;
    error: string | null;
    data: T;
  }

  // System ID API state
  let systemIdState = $state<ApiState<string>>({
    loading: true,
    error: null,
    data: '',
  });

  let systemId = $derived(systemIdState.data || '');

  onMount(() => {
    loadSystemId();
    return clearTimers;
  });

  async function loadSystemId() {
    systemIdState.loading = true;
    systemIdState.error = null;

    try {
      interface SystemIdResponse {
        systemID?: string;
      }
      const data = await api.get<SystemIdResponse>('/api/v2/settings/systemid');
      systemIdState.data = data.systemID || '';
    } catch (error) {
      logger.error('Failed to fetch system ID:', error);
      systemIdState.error = t('settings.support.systemId.errorLoading');
      systemIdState.data = t('settings.support.systemId.errorLoading');
    } finally {
      systemIdState.loading = false;
    }
  }

  async function generateSupportDump() {
    if (supportDump.uploadToSentry && !hasValidGithubIssue) {
      updateStatus(
        t('settings.support.supportReport.statusMessages.githubIssueRequired'),
        'error',
        0
      );
      return;
    }

    clearTimers();
    generating = true;
    statusMessage = '';
    statusType = 'info';
    progressPercent = 0;

    updateStatus(t('settings.support.supportReport.statusMessages.preparing'), 'info', 10);

    try {
      interface SupportDumpResponse {
        success?: boolean;
        uploaded_at?: string;
        dump_id?: string;
        download_url?: string;
        message?: string;
      }
      const data = await api.post<SupportDumpResponse>('/api/v2/support/generate', {
        include_logs: supportDump.includeLogs,
        include_config: supportDump.includeConfig,
        include_system_info: supportDump.includeSystemInfo,
        github_issue_number: hasValidGithubIssue ? normalizedGithubIssue.replace(/^#/, '') : '',
        user_message: supportDump.userMessage,
        upload_to_sentry: supportDump.uploadToSentry,
      });
      generating = false;

      if (data.success) {
        if (supportDump.uploadToSentry && data.uploaded_at) {
          if (data.dump_id) {
            updateStatus(
              t('settings.support.supportReport.statusMessages.uploadSuccessWithId', {
                dumpId: data.dump_id,
              }),
              'success',
              100
            );
          } else {
            updateStatus(
              t('settings.support.supportReport.statusMessages.uploadSuccess'),
              'success',
              100
            );
          }
        } else if (data.download_url) {
          updateStatus(
            t('settings.support.supportReport.statusMessages.downloadSuccess'),
            'success',
            100
          );
          const downloadUrl = data.download_url;
          safeTimeout(() => {
            window.location.href = downloadUrl;
          }, 500);
        } else {
          updateStatus(
            t('settings.support.supportReport.statusMessages.generateSuccess'),
            'success',
            100
          );
        }

        safeTimeout(() => {
          statusMessage = '';
          statusType = 'info';
          progressPercent = 0;
        }, 10000);
      } else {
        updateStatus(
          t('settings.support.supportReport.statusMessages.generateFailed', {
            message: data.message || t('common.errors.unknownError'),
          }),
          'error',
          0
        );
      }
    } catch (error) {
      generating = false;
      updateStatus(
        t('settings.support.supportReport.statusMessages.error', {
          message: (error as Error).message,
        }),
        'error',
        0
      );
    }
  }

  function updateStatus(message: string, type: 'info' | 'success' | 'error', percent: number) {
    statusMessage = message;
    statusType = type;
    progressPercent = percent;

    if (type === 'info' && percent < 90) {
      safeTimeout(() => {
        if (generating && progressPercent < 90) {
          progressPercent = Math.min(progressPercent + 10, 90);
        }
      }, 1000);
    }
  }
</script>

<div class={className} {...rest}>
  <div class="space-y-4">
    <div class="rounded-lg overflow-hidden bg-[var(--color-base-200)]">
      <div class="p-6">
        <h3 class="flex items-center gap-2 text-lg font-semibold">
          {t('settings.support.supportReport.title')}
        </h3>

        <div class="space-y-3 mb-4">
          <p class="text-sm text-[var(--color-base-content)] opacity-80">
            {@html t('settings.support.supportReport.description.intro')}
          </p>

          <ErrorAlert type="warning">
            {#snippet children()}
              <div class="min-w-0">
                <span class="font-semibold">
                  {t('settings.support.supportReport.githubRequired.title')}
                </span>
                <div class="mt-1">
                  {@html t('settings.support.supportReport.githubRequired.description')}
                </div>
              </div>
            {/snippet}
          </ErrorAlert>

          <div class="bg-[var(--color-base-100)] rounded-lg p-3 border border-[var(--border-200)]">
            <h4 class="font-semibold text-sm mb-2">
              {t('settings.support.supportReport.whatsIncluded.title')}
            </h4>
            <ul class="text-xs space-y-1 text-[var(--color-base-content)] opacity-70">
              <li class="flex items-center gap-2">
                <CircleCheck class="size-4 text-[var(--color-success)] shrink-0" />
                <span
                  >{@html t('settings.support.supportReport.whatsIncluded.applicationLogs')}</span
                >
              </li>
              <li class="flex items-center gap-2">
                <CircleCheck class="size-4 text-[var(--color-success)] shrink-0" />
                <span>{@html t('settings.support.supportReport.whatsIncluded.configuration')}</span>
              </li>
              <li class="flex items-center gap-2">
                <CircleCheck class="size-4 text-[var(--color-success)] shrink-0" />
                <span>{@html t('settings.support.supportReport.whatsIncluded.systemInfo')}</span>
              </li>
              <li class="flex items-center gap-2">
                <XCircle class="size-4 text-[var(--color-error)] shrink-0" />
                <span>{@html t('settings.support.supportReport.whatsIncluded.notIncluded')}</span>
              </li>
            </ul>
          </div>
        </div>

        <div class="space-y-2">
          <Checkbox
            bind:checked={supportDump.includeLogs}
            label={t('settings.support.diagnostics.includeRecentLogs')}
            disabled={generating}
          />

          <Checkbox
            bind:checked={supportDump.includeConfig}
            label={t('settings.support.diagnostics.includeConfiguration')}
            disabled={generating}
          />

          <Checkbox
            bind:checked={supportDump.includeSystemInfo}
            label={t('settings.support.diagnostics.includeSystemInfo')}
            disabled={generating}
          />

          {#if supportDump.uploadToSentry}
            <div class="mt-4">
              <label class="block py-1" for={githubIssueInputId}>
                <span class="text-sm text-[var(--color-base-content)]">
                  {t('settings.support.supportReport.githubIssue.label')}
                  <span class="text-[var(--color-error)]">*</span>
                </span>
              </label>
              <input
                type="text"
                id={githubIssueInputId}
                bind:value={supportDump.githubIssueNumber}
                class="block w-full px-3 py-1.5 text-sm bg-[var(--color-base-100)] text-[var(--color-base-content)] border rounded-md transition-all focus:outline-none focus:border-[var(--color-primary)] focus:ring-2 focus:ring-[var(--color-primary)]/10 disabled:opacity-50 disabled:cursor-not-allowed"
                class:border-[var(--color-error)]={supportDump.uploadToSentry &&
                  !supportDump.githubIssueNumber}
                class:border-[var(--border-200)]={!(
                  supportDump.uploadToSentry && !supportDump.githubIssueNumber
                )}
                placeholder={t('settings.support.supportReport.githubIssue.placeholder')}
                pattern="#?[0-9]+"
                disabled={generating}
              />
              <span class="text-xs text-[var(--color-base-content)] opacity-60 mt-1 block">
                {@html t('settings.support.supportReport.githubIssue.helper')}
              </span>
            </div>
          {/if}

          <div class="mt-4">
            <label class="block py-1" for={userMessageInputId}>
              <span class="text-sm text-[var(--color-base-content)]"
                >{t('settings.support.supportReport.userMessage.labelOptional')}</span
              >
            </label>
            <textarea
              id={userMessageInputId}
              bind:value={supportDump.userMessage}
              class="block w-full px-3 py-2 text-sm bg-[var(--color-base-100)] text-[var(--color-base-content)] border border-[var(--border-200)] rounded-md transition-all focus:outline-none focus:border-[var(--color-primary)] focus:ring-2 focus:ring-[var(--color-primary)]/10 disabled:opacity-50 disabled:cursor-not-allowed resize-y min-h-24"
              placeholder={t('settings.support.supportReport.userMessage.placeholderOptional')}
              rows="4"
              disabled={generating}
            ></textarea>

            <span class="text-xs text-[var(--color-base-content)] opacity-60 mt-1 block">
              {t('settings.support.supportReport.userMessage.systemIdNote', { systemId })}
            </span>
          </div>

          <div class="mt-4">
            <Checkbox
              bind:checked={supportDump.uploadToSentry}
              label={t('settings.support.supportReport.uploadOption.labelWithRequirement')}
              disabled={generating}
            />
            <div class="pl-6 mt-2 space-y-2">
              <div class="text-xs text-[var(--color-base-content)] opacity-60">
                <p class="flex items-start gap-1">
                  <Check class="size-4 shrink-0" />
                  {@html t('settings.support.supportReport.uploadOption.details.sentryUpload')}
                </p>
                <p class="flex items-start gap-1">
                  <Globe class="size-4 shrink-0" />
                  {t('settings.support.supportReport.uploadOption.details.euDataCenter')}
                </p>
                <p class="flex items-start gap-1">
                  <ShieldCheck class="size-4 shrink-0" />
                  {t('settings.support.supportReport.uploadOption.details.privacyCompliant')}
                </p>
              </div>
              <div class="text-xs text-[var(--color-warning)]/80 flex items-center gap-1">
                <Info class="size-4 shrink-0" />
                {t('settings.support.supportReport.uploadOption.details.manualWarning')}
              </div>
            </div>
          </div>
        </div>

        {#if statusMessage}
          <div class="mt-3 max-w-2xl">
            <div
              class="flex items-start gap-3 py-2 px-3 rounded-lg text-sm"
              class:bg-[color-mix(in_srgb,var(--color-info)_15%,transparent)]={statusType ===
                'info'}
              class:text-[var(--color-info)]={statusType === 'info'}
              class:bg-[color-mix(in_srgb,var(--color-success)_15%,transparent)]={statusType ===
                'success'}
              class:text-[var(--color-success)]={statusType === 'success'}
              class:bg-[color-mix(in_srgb,var(--color-error)_15%,transparent)]={statusType ===
                'error'}
              class:text-[var(--color-error)]={statusType === 'error'}
              role="status"
              aria-live="polite"
            >
              {#if statusType === 'info'}
                <Info class="size-4 shrink-0" />
              {:else if statusType === 'success'}
                <CircleCheck class="size-4 shrink-0" />
              {:else if statusType === 'error'}
                <XCircle class="size-4 shrink-0" />
              {/if}
              <span class="min-w-0 text-sm">{statusMessage}</span>
            </div>

            {#if generating && progressPercent > 0}
              <div class="mt-1">
                <div class="w-full bg-[var(--color-base-300)] rounded-full h-1.5">
                  <div
                    class="bg-[var(--color-primary)] h-1.5 rounded-full transition-all duration-500"
                    style:width="{progressPercent}%"
                  ></div>
                </div>
              </div>
            {/if}
          </div>
        {/if}

        <div class="flex flex-wrap items-center gap-2 justify-end mt-6">
          <button
            onclick={generateSupportDump}
            disabled={generateButtonDisabled}
            class="inline-flex items-center justify-center gap-2 px-4 py-2 text-sm font-medium rounded-md cursor-pointer transition-all bg-[var(--color-primary)] text-[var(--color-primary-content)] border border-[var(--color-primary)] hover:bg-[var(--color-primary-hover)] disabled:opacity-50 disabled:cursor-not-allowed focus-visible:outline-2 focus-visible:outline-[var(--color-primary)] focus-visible:outline-offset-2"
          >
            {#if !generating}
              <span class="flex items-center gap-2">
                <Download class="size-4" />
                <span
                  >{supportDump.uploadToSentry
                    ? t('settings.support.supportReport.generateButton.upload')
                    : t('settings.support.supportReport.generateButton.download')}</span
                >
              </span>
            {:else}
              <span
                class="inline-block w-4 h-4 border-2 border-[var(--color-primary-content)]/30 border-t-[var(--color-primary-content)] rounded-full animate-spin"
              ></span>
            {/if}
          </button>
        </div>
      </div>
    </div>
  </div>
</div>
