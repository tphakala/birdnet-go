<!--
  Support Settings Page Component

  Purpose: Support dump generation and diagnostic features for BirdNET-Go.
  Note: Telemetry settings have been moved to the Main Settings General tab.

  Features:
  - Support dump generation with customizable options
  - Upload to Sentry or download locally options
  - User message inclusion for context

  Props: None - This is a page component that uses global settings stores

  Performance Optimizations:
  - Removed page-level loading spinner to prevent flickering
  - Cached CSRF token to avoid repeated DOM queries
  - API state management for system ID loading
  - Progress tracking for support dump generation

  @component
-->
<script lang="ts">
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import SettingsTabs from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import type { TabDefinition } from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import {
    Check,
    Globe,
    ShieldCheck,
    Info,
    Download,
    TriangleAlert,
    XCircle,
    CircleCheck,
    Wrench,
  } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';

  const logger = loggers.settings;

  // PERFORMANCE OPTIMIZATION: Cache CSRF token with $derived
  let csrfToken = $derived(
    (document.querySelector('meta[name="csrf-token"]') as HTMLElement)?.getAttribute('content') ||
      ''
  );

  // Tab state
  let activeTab = $state('diagnostics');

  // Tab definitions - only Diagnostics tab (Telemetry moved to Main Settings)
  let tabs = $derived<TabDefinition[]>([
    {
      id: 'diagnostics',
      label: t('settings.support.sections.diagnostics.title'),
      icon: Wrench,
      content: diagnosticsTabContent,
      hasChanges: false,
    },
  ]);

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

  // Derived state for generate button disabled logic
  let generateButtonDisabled = $derived(
    generating ||
      (!supportDump.includeLogs && !supportDump.includeConfig && !supportDump.includeSystemInfo) ||
      (supportDump.uploadToSentry && !supportDump.githubIssueNumber)
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

  // PERFORMANCE OPTIMIZATION: Derived system ID
  let systemId = $derived(systemIdState.data || '');

  // PERFORMANCE OPTIMIZATION: Load system ID with proper state management
  $effect(() => {
    loadSystemId();
  });

  async function loadSystemId() {
    systemIdState.loading = true;
    systemIdState.error = null;

    try {
      const headers = new Headers();
      if (csrfToken) {
        headers.set('X-CSRF-Token', csrfToken);
      }

      const response = await fetch('/api/v2/settings/systemid', {
        headers,
        credentials: 'same-origin',
      });

      if (!response.ok) {
        throw new Error(`Failed to load system ID: ${response.status}`);
      }

      const data = await response.json();
      systemIdState.data = data.systemID || '';
    } catch (error) {
      logger.error('Failed to fetch system ID:', error);
      systemIdState.error = t('settings.support.systemId.errorLoading');
      systemIdState.data = t('settings.support.systemId.errorLoading');
    } finally {
      systemIdState.loading = false;
    }
  }

  // Support dump generation
  async function generateSupportDump() {
    // Validate GitHub issue number if uploading
    if (supportDump.uploadToSentry && !supportDump.githubIssueNumber) {
      updateStatus(
        t('settings.support.supportReport.statusMessages.githubIssueRequired'),
        'error',
        0
      );
      return;
    }

    generating = true;
    statusMessage = '';
    statusType = 'info';
    progressPercent = 0;

    updateStatus(t('settings.support.supportReport.statusMessages.preparing'), 'info', 10);

    try {
      const headers = new Headers({
        'Content-Type': 'application/json',
      });

      if (csrfToken) {
        headers.set('X-CSRF-Token', csrfToken);
      }

      const response = await fetch('/api/v2/support/generate', {
        method: 'POST',
        headers,
        credentials: 'same-origin',
        body: JSON.stringify({
          include_logs: supportDump.includeLogs,
          include_config: supportDump.includeConfig,
          include_system_info: supportDump.includeSystemInfo,
          github_issue_number: supportDump.githubIssueNumber
            ? supportDump.githubIssueNumber.replace('#', '')
            : '',
          user_message: supportDump.userMessage,
          upload_to_sentry: supportDump.uploadToSentry,
        }),
      });

      if (!response.ok) {
        throw new Error(`Server error: ${response.status} ${response.statusText}`);
      }

      const data = await response.json();
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
          setTimeout(() => {
            window.location.href = data.download_url;
          }, 500);
        } else {
          updateStatus(
            t('settings.support.supportReport.statusMessages.generateSuccess'),
            'success',
            100
          );
        }

        // Clear status after 10 seconds
        setTimeout(() => {
          statusMessage = '';
          statusType = 'info';
          progressPercent = 0;
        }, 10000);
      } else {
        updateStatus(
          t('settings.support.supportReport.statusMessages.generateFailed', {
            message: data.message || 'Unknown error',
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

    // Simulate progress for long operations
    if (type === 'info' && percent < 90) {
      setTimeout(() => {
        if (generating && progressPercent < 90) {
          progressPercent = Math.min(progressPercent + 10, 90);
        }
      }, 1000);
    }
  }
</script>

{#snippet diagnosticsTabContent()}
  <div class="space-y-6">
    <!-- Support & Diagnostics Section -->
    <SettingsSection
      title={t('settings.support.sections.diagnostics.title')}
      description={t('settings.support.sections.diagnostics.description')}
      defaultOpen={true}
    >
      <div class="space-y-4">
        <!-- Support Dump Generation -->
        <div class="card bg-base-200">
          <div class="card-body">
            <h3 class="card-title text-lg">{t('settings.support.supportReport.title')}</h3>

            <!-- Enhanced Description -->
            <div class="space-y-3 mb-4">
              <p class="text-sm text-base-content opacity-80">
                {@html t('settings.support.supportReport.description.intro')}
              </p>

              <div class="alert alert-warning shadow-xs text-sm">
                <TriangleAlert class="size-5 shrink-0" />
                <div class="min-w-0">
                  <span class="font-semibold">
                    {t('settings.support.supportReport.githubRequired.title')}
                  </span>
                  <div class="mt-1">
                    {@html t('settings.support.supportReport.githubRequired.description')}
                  </div>
                </div>
              </div>

              <div class="bg-base-100 rounded-lg p-3 border border-base-300">
                <h4 class="font-semibold text-sm mb-2">
                  {t('settings.support.supportReport.whatsIncluded.title')}
                </h4>
                <ul class="text-xs space-y-1 text-base-content opacity-70">
                  <li class="flex items-center gap-2">
                    <CircleCheck class="size-4 text-success shrink-0" />
                    <span
                      >{@html t(
                        'settings.support.supportReport.whatsIncluded.applicationLogs'
                      )}</span
                    >
                  </li>
                  <li class="flex items-center gap-2">
                    <CircleCheck class="size-4 text-success shrink-0" />
                    <span
                      >{@html t('settings.support.supportReport.whatsIncluded.configuration')}</span
                    >
                  </li>
                  <li class="flex items-center gap-2">
                    <CircleCheck class="size-4 text-success shrink-0" />
                    <span>{@html t('settings.support.supportReport.whatsIncluded.systemInfo')}</span
                    >
                  </li>
                  <li class="flex items-center gap-2">
                    <XCircle class="size-4 text-error shrink-0" />
                    <span
                      >{@html t('settings.support.supportReport.whatsIncluded.notIncluded')}</span
                    >
                  </li>
                </ul>
              </div>
            </div>

            <!-- Options -->
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

              <!-- GitHub Issue Number (Required for Upload) -->
              {#if supportDump.uploadToSentry}
                <div class="form-control mt-4">
                  <label class="label" for="githubIssueNumber">
                    <span class="label-text">
                      {t('settings.support.supportReport.githubIssue.label')}
                      <span class="text-error">*</span>
                    </span>
                  </label>
                  <input
                    type="text"
                    id="githubIssueNumber"
                    bind:value={supportDump.githubIssueNumber}
                    class="input input-sm text-base-content"
                    class:input-error={supportDump.uploadToSentry && !supportDump.githubIssueNumber}
                    placeholder={t('settings.support.supportReport.githubIssue.placeholder')}
                    pattern="#?[0-9]+"
                    disabled={generating}
                  />
                  <span class="help-text mt-1">
                    {@html t('settings.support.supportReport.githubIssue.helper')}
                  </span>
                </div>
              {/if}

              <!-- User Message -->
              <div class="form-control mt-4">
                <label class="label" for="userMessage">
                  <span class="label-text"
                    >{t('settings.support.supportReport.userMessage.labelOptional')}</span
                  >
                </label>
                <textarea
                  id="userMessage"
                  bind:value={supportDump.userMessage}
                  class="textarea textarea-sm h-24 text-base-content"
                  placeholder={t('settings.support.supportReport.userMessage.placeholderOptional')}
                  rows="4"
                  disabled={generating}
                ></textarea>

                <!-- System ID Note -->
                <span class="help-text mt-1">
                  {t('settings.support.supportReport.userMessage.systemIdNote', { systemId })}
                </span>
              </div>

              <!-- Upload Option (always available) -->
              <div class="mt-4">
                <Checkbox
                  bind:checked={supportDump.uploadToSentry}
                  label={t('settings.support.supportReport.uploadOption.labelWithRequirement')}
                  disabled={generating}
                />
                <div class="pl-6 mt-2 space-y-2">
                  <div class="text-xs text-base-content opacity-60">
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
                  <div class="text-xs text-warning/80 flex items-center gap-1">
                    <Info class="size-4 shrink-0" />
                    {t('settings.support.supportReport.uploadOption.details.manualWarning')}
                  </div>
                </div>
              </div>
            </div>

            <!-- Status Message -->
            {#if statusMessage}
              <div class="mt-3 max-w-2xl">
                <div
                  class="alert py-2 px-3 text-sm"
                  class:alert-info={statusType === 'info'}
                  class:alert-success={statusType === 'success'}
                  class:alert-error={statusType === 'error'}
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

                <!-- Progress Bar -->
                {#if generating && progressPercent > 0}
                  <div class="mt-1">
                    <div class="w-full bg-base-300 rounded-full h-1.5">
                      <div
                        class="bg-primary h-1.5 rounded-full transition-all duration-500"
                        style:width="{progressPercent}%"
                      ></div>
                    </div>
                  </div>
                {/if}
              </div>
            {/if}

            <!-- Generate Button -->
            <div class="card-actions justify-end mt-6">
              <button
                onclick={generateSupportDump}
                disabled={generateButtonDisabled}
                class="btn btn-primary"
                class:btn-disabled={generateButtonDisabled}
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
                  <span class="loading loading-spinner loading-sm"></span>
                {/if}
              </button>
            </div>
          </div>
        </div>
      </div>
    </SettingsSection>
  </div>
{/snippet}

<!-- Main Content -->
<main class="settings-page-content" aria-label="Support and diagnostics settings configuration">
  <SettingsTabs {tabs} bind:activeTab />
</main>
