<script lang="ts">
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import {
    settingsStore,
    settingsActions,
    supportSettings,
    type SupportSettings,
  } from '$lib/stores/settings';
  import { hasSettingsChanged } from '$lib/utils/settingsChanges';
  import { actionIcons, alertIconsSvg, systemIcons, mediaIcons } from '$lib/utils/icons'; // Centralized icons - see icons.ts

  let settings = $derived(
    $supportSettings ||
      ({
        sentry: {
          enabled: false,
          dsn: '',
          environment: 'production',
          includePrivateInfo: false,
        },
        telemetry: {
          enabled: true,
          includeSystemInfo: true,
          includeAudioInfo: false,
        },
      } as SupportSettings)
  );

  let store = $derived($settingsStore);

  // Track changes for each section separately
  let sentryHasChanges = $derived(
    hasSettingsChanged((store.originalData as any)?.sentry, (store.formData as any)?.sentry)
  );

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
    userMessage: '',
    uploadToSentry: true,
  });

  // System ID - fetch from API
  let systemId = $state<string>('');

  // Fetch system ID on component mount
  $effect(() => {
    fetch('/api/v2/settings/systemid')
      .then(response => response.json())
      .then(data => {
        systemId = data.systemID || '';
      })
      .catch(error => {
        console.error('Failed to fetch system ID:', error);
        systemId = 'Error loading system ID';
      });
  });

  // Sentry update handlers
  function updateSentryEnabled(enabled: boolean) {
    settingsActions.updateSection('sentry', {
      ...settings.sentry!,
      enabled,
    });
  }

  // Copy system ID to clipboard
  async function copySystemId() {
    try {
      await navigator.clipboard.writeText(systemId);
      // Could add temporary success feedback here
    } catch (error) {
      console.error('Failed to copy system ID:', error);
    }
  }

  // Support dump generation
  async function generateSupportDump() {
    generating = true;
    statusMessage = '';
    statusType = 'info';
    progressPercent = 0;

    updateStatus('Preparing support dump...', 'info', 10);

    try {
      const response = await fetch('/api/v2/support/generate', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        credentials: 'same-origin',
        body: JSON.stringify({
          include_logs: supportDump.includeLogs,
          include_config: supportDump.includeConfig,
          include_system_info: supportDump.includeSystemInfo,
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
          updateStatus('Support dump successfully uploaded to developers!', 'success', 100);
          if (data.dump_id) {
            statusMessage += ` Reference ID: ${data.dump_id}`;
          }
        } else if (data.download_url) {
          updateStatus('Support dump generated successfully! Downloading...', 'success', 100);
          setTimeout(() => {
            window.location.href = data.download_url;
          }, 500);
        } else {
          updateStatus('Support dump generated successfully!', 'success', 100);
        }

        // Clear status after 10 seconds
        setTimeout(() => {
          statusMessage = '';
          statusType = 'info';
          progressPercent = 0;
        }, 10000);
      } else {
        updateStatus(
          'Failed to generate support dump: ' + (data.message || 'Unknown error'),
          'error',
          0
        );
      }
    } catch (error) {
      generating = false;
      updateStatus('Error: ' + (error as Error).message, 'error', 0);
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

{#if store.isLoading}
  <div class="flex items-center justify-center py-12">
    <div class="loading loading-spinner loading-lg"></div>
  </div>
{:else}
  <div class="space-y-4">
    <!-- Error Tracking & Telemetry Section -->
    <SettingsSection
      title="Error Tracking & Telemetry"
      description="Optional error tracking to help improve BirdNET-Go reliability and performance"
      defaultOpen={true}
      hasChanges={sentryHasChanges}
    >
      <div class="space-y-4">
        <!-- Privacy Notice -->
        <div class="mt-4 p-4 bg-base-200 rounded-lg shadow-sm">
          <div>
            <h3 class="font-bold">Privacy-First Error Tracking</h3>
            <div class="text-sm mt-1">
              <p>
                Error tracking is <strong>completely optional</strong> and requires your explicit consent.
                When enabled:
              </p>
              <ul class="list-disc list-inside mt-2 space-y-1">
                <li>Only essential error information is collected for debugging</li>
                <li>No personal data, audio recordings, or bird detection data is sent</li>
                <li>All data is filtered to remove sensitive information</li>
                <li>Telemetry data helps developers identify and fix issues in BirdNET-Go</li>
              </ul>
            </div>
          </div>
        </div>

        <!-- Enable Error Tracking -->
        <Checkbox
          bind:checked={settings.sentry!.enabled}
          label="Enable Error Tracking (Opt-in)"
          disabled={store.isLoading || store.isSaving}
          onchange={() => updateSentryEnabled(settings.sentry!.enabled)}
        />

        <!-- System ID Display -->
        <div class="form-control w-full mt-4">
          <label class="label" for="systemID">
            <span class="label-text">Your System ID</span>
          </label>
          <div class="join">
            <input
              type="text"
              id="systemID"
              value={systemId}
              class="input input-sm input-bordered join-item w-full font-mono text-base-content"
              readonly
            />
            <button type="button" class="btn btn-sm join-item" onclick={copySystemId}>
              <div class="h-5 w-5">
                {@html actionIcons.copy}
              </div>
              Copy
            </button>
          </div>
          <div class="label">
            <span class="label-text-alt text-base-content/60"
              >Include this ID when reporting issues if you want developers to identify your error
              reports</span
            >
          </div>
        </div>
      </div>
    </SettingsSection>

    <!-- Support & Diagnostics Section -->
    <SettingsSection
      title="Support & Diagnostics"
      description="Help developers fix your issues faster by providing essential diagnostic information"
      defaultOpen={false}
    >
      <div class="space-y-4">
        <!-- Support Dump Generation -->
        <div class="card bg-base-200">
          <div class="card-body">
            <h3 class="card-title text-lg">Generate Support Report</h3>

            <!-- Enhanced Description -->
            <div class="space-y-3 mb-4">
              <p class="text-sm text-base-content/80">
                Support reports are <strong>essential for troubleshooting</strong> and dramatically improve
                our ability to resolve issues you're experiencing. They provide developers with crucial
                context about your system configuration, recent application logs, and error patterns
                that would be impossible to diagnose otherwise.
              </p>

              <p class="text-sm text-base-content/80">
                Please <a
                  href="https://github.com/tphakala/birdnet-go/issues/new"
                  target="_blank"
                  class="link link-primary font-semibold">create a GitHub issue</a
                >
                describing your problem <strong>before or after</strong> generating this support report.
                This allows for proper tracking and communication about your issue.
              </p>

              <div class="bg-base-100 rounded-lg p-3 border border-base-300">
                <h4 class="font-semibold text-sm mb-2">What's included in the report:</h4>
                <ul class="text-xs space-y-1 text-base-content/70">
                  <li class="flex items-center gap-2">
                    <div class="h-4 w-4 text-success flex-shrink-0">
                      {@html alertIconsSvg.success}
                    </div>
                    <span
                      ><strong>Application logs</strong> - Recent errors and debug information</span
                    >
                  </li>
                  <li class="flex items-center gap-2">
                    <div class="h-4 w-4 text-success flex-shrink-0">
                      {@html alertIconsSvg.success}
                    </div>
                    <span
                      ><strong>Configuration</strong> - Your settings with sensitive data removed</span
                    >
                  </li>
                  <li class="flex items-center gap-2">
                    <div class="h-4 w-4 text-success flex-shrink-0">
                      {@html alertIconsSvg.success}
                    </div>
                    <span
                      ><strong>System information</strong> - OS version, memory, and runtime details</span
                    >
                  </li>
                  <li class="flex items-center gap-2">
                    <div class="h-4 w-4 text-error flex-shrink-0">
                      {@html alertIconsSvg.error}
                    </div>
                    <span
                      ><strong>NOT included</strong> - Audio files, bird detections, or personal data</span
                    >
                  </li>
                </ul>
              </div>
            </div>

            <!-- Options -->
            <div class="space-y-2">
              <Checkbox
                bind:checked={supportDump.includeLogs}
                label="Include recent logs"
                disabled={generating}
              />

              <Checkbox
                bind:checked={supportDump.includeConfig}
                label="Include configuration (sensitive data removed)"
                disabled={generating}
              />

              <Checkbox
                bind:checked={supportDump.includeSystemInfo}
                label="Include system information"
                disabled={generating}
              />

              <!-- User Message -->
              <div class="form-control mt-4">
                <label class="label" for="userMessage">
                  <span class="label-text">Describe the issue</span>
                </label>
                <textarea
                  id="userMessage"
                  bind:value={supportDump.userMessage}
                  class="textarea textarea-bordered textarea-sm h-24 text-base-content"
                  placeholder="Please describe the issue and include GitHub issue link if applicable (e.g., #123)"
                  rows="4"
                  disabled={generating}
                ></textarea>

                <!-- GitHub Issue Note -->
                <div class="label">
                  <span class="label-text-alt text-base-content/60">
                    💡 Tip: If you have a GitHub issue, please include the issue number (e.g., #123)
                    and mention your System ID <span class="font-mono text-xs">{systemId}</span> in the
                    GitHub issue to help developers link your support data.
                  </span>
                </div>
              </div>

              <!-- Upload Option (only shown if telemetry is enabled) -->
              {#if settings.sentry!.enabled}
                <div class="mt-4">
                  <Checkbox
                    bind:checked={supportDump.uploadToSentry}
                    label="Upload to developers (recommended)"
                    disabled={generating}
                  />
                  <div class="pl-6 mt-2 space-y-2">
                    <div class="text-xs text-base-content/60">
                      <p class="flex items-start gap-1">
                        {@html actionIcons.check}
                        Data is uploaded to
                        <a href="https://sentry.io" target="_blank" class="link link-primary"
                          >Sentry</a
                        > cloud service
                      </p>
                      <p class="flex items-start gap-1">
                        {@html systemIcons.globe}
                        Stored in EU data center (Frankfurt, Germany)
                      </p>
                      <p class="flex items-start gap-1">
                        {@html systemIcons.shield}
                        Privacy-compliant with sensitive data removed
                      </p>
                    </div>
                    <div class="text-xs text-warning/80 flex items-center gap-1">
                      {@html systemIcons.infoCircle}
                      Uncheck only if you prefer to handle the support file manually
                    </div>
                  </div>
                </div>
              {/if}
            </div>

            <!-- Status Message -->
            {#if statusMessage}
              <div class="mt-4">
                <div
                  class="alert"
                  class:alert-info={statusType === 'info'}
                  class:alert-success={statusType === 'success'}
                  class:alert-error={statusType === 'error'}
                >
                  {#if statusType === 'info'}
                    {@html alertIconsSvg.info}
                  {:else if statusType === 'success'}
                    {@html alertIconsSvg.success}
                  {:else if statusType === 'error'}
                    {@html alertIconsSvg.error}
                  {/if}
                  <span>{statusMessage}</span>
                </div>

                <!-- Progress Bar -->
                {#if generating && progressPercent > 0}
                  <div class="mt-2">
                    <div class="w-full bg-base-300 rounded-full h-2">
                      <div
                        class="bg-primary h-2 rounded-full transition-all duration-500"
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
                disabled={generating ||
                  (!supportDump.includeLogs &&
                    !supportDump.includeConfig &&
                    !supportDump.includeSystemInfo)}
                class="btn btn-primary"
                class:btn-disabled={generating ||
                  (!supportDump.includeLogs &&
                    !supportDump.includeConfig &&
                    !supportDump.includeSystemInfo)}
              >
                {#if !generating}
                  <span class="flex items-center gap-2">
                    {@html mediaIcons.download}
                    <span
                      >{supportDump.uploadToSentry
                        ? 'Generate & Upload'
                        : 'Generate & Download'}</span
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
{/if}
