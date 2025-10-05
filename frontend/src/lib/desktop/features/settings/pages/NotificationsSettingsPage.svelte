<!--
  Notifications Settings Page Component
  
  Purpose: Configure notification testing and debugging features for BirdNET-Go
  including test notification generation for new species detections.
  
  Features:
  - Test new species notification generation
  - Notification system debugging and testing
  - API endpoint testing functionality
  
  Props: None - This is a page component that uses global settings stores
  
  Performance Optimizations:
  - Cached CSRF token to avoid repeated DOM queries
  - API state management for notification testing
  - Reactive change detection with $derived
  - Progress tracking for test notification generation
  
  @component
-->
<script lang="ts">
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import { alertIconsSvg, systemIcons } from '$lib/utils/icons'; // Centralized icons - see icons.ts
  import { t } from '$lib/i18n';

  // PERFORMANCE OPTIMIZATION: Cache CSRF token with $derived
  let csrfToken = $derived(
    (document.querySelector('meta[name="csrf-token"]') as HTMLElement)?.getAttribute('content') ||
      ''
  );

  // Test notification generation state
  let generating = $state(false);
  let statusMessage = $state('');
  let statusType = $state<'info' | 'success' | 'error'>('info');

  // Test new species notification
  async function sendTestNewSpeciesNotification() {
    generating = true;
    statusMessage = '';
    statusType = 'info';

    updateStatus(t('settings.notifications.testNotification.statusMessages.sending'), 'info');

    try {
      const headers = new Headers({
        'Content-Type': 'application/json',
      });

      if (csrfToken) {
        headers.set('X-CSRF-Token', csrfToken);
      }

      const response = await fetch('/api/v2/notifications/test/new-species', {
        method: 'POST',
        headers,
        credentials: 'same-origin',
      });

      if (!response.ok) {
        if (response.status === 503) {
          throw new Error(
            t('settings.notifications.testNotification.statusMessages.serviceUnavailable')
          );
        }
        throw new Error(`Server error: ${response.status} ${response.statusText}`);
      }

      const data = await response.json();
      generating = false;

      updateStatus(
        t('settings.notifications.testNotification.statusMessages.success', {
          species: data.title || 'Northern Cardinal',
        }),
        'success'
      );

      // Clear status after 5 seconds
      setTimeout(() => {
        statusMessage = '';
        statusType = 'info';
      }, 5000);
    } catch (error) {
      generating = false;
      updateStatus(
        t('settings.notifications.testNotification.statusMessages.error', {
          message: (error as Error).message,
        }),
        'error'
      );

      // Clear error after 10 seconds
      setTimeout(() => {
        statusMessage = '';
        statusType = 'info';
      }, 10000);
    }
  }

  function updateStatus(message: string, type: 'info' | 'success' | 'error') {
    statusMessage = message;
    statusType = type;
  }
</script>

<div class="space-y-4 settings-page-content">
  <!-- Notification Testing Section -->
  <SettingsSection
    title={t('settings.notifications.sections.testing.title')}
    description={t('settings.notifications.sections.testing.description')}
    defaultOpen={true}
  >
    <div class="space-y-4">
      <!-- Test New Species Notification -->
      <div class="card bg-base-200">
        <div class="card-body">
          <h3 class="card-title text-lg">{t('settings.notifications.testNotification.title')}</h3>

          <!-- Description -->
          <div class="space-y-3 mb-4">
            <p class="text-sm text-base-content/80">
              {t('settings.notifications.testNotification.description')}
            </p>

            <div class="bg-base-100 rounded-lg p-3 border border-base-300">
              <h4 class="font-semibold text-sm mb-2">
                {t('settings.notifications.testNotification.whatHappens.title')}
              </h4>
              <ul class="text-xs space-y-1 text-base-content/70">
                <li class="flex items-center gap-2">
                  <div class="h-4 w-4 text-success flex-shrink-0">
                    {@html alertIconsSvg.success}
                  </div>
                  <span
                    >{t(
                      'settings.notifications.testNotification.whatHappens.createsNotification'
                    )}</span
                  >
                </li>
                <li class="flex items-center gap-2">
                  <div class="h-4 w-4 text-success flex-shrink-0">
                    {@html alertIconsSvg.success}
                  </div>
                  <span
                    >{t(
                      'settings.notifications.testNotification.whatHappens.appearsInStream'
                    )}</span
                  >
                </li>
                <li class="flex items-center gap-2">
                  <div class="h-4 w-4 text-success flex-shrink-0">
                    {@html alertIconsSvg.success}
                  </div>
                  <span
                    >{t(
                      'settings.notifications.testNotification.whatHappens.matchesRealDetection'
                    )}</span
                  >
                </li>
                <li class="flex items-center gap-2">
                  <div class="h-4 w-4 text-info flex-shrink-0">
                    {@html systemIcons.infoCircle}
                  </div>
                  <span
                    >{t('settings.notifications.testNotification.whatHappens.expires24Hours')}</span
                  >
                </li>
              </ul>
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
              >
                <div class="h-4 w-4 flex-shrink-0">
                  {#if statusType === 'info'}
                    {@html alertIconsSvg.info}
                  {:else if statusType === 'success'}
                    {@html alertIconsSvg.success}
                  {:else if statusType === 'error'}
                    {@html alertIconsSvg.error}
                  {/if}
                </div>
                <span class="min-w-0 text-sm">{statusMessage}</span>
              </div>
            </div>
          {/if}

          <!-- Send Test Notification Button -->
          <div class="card-actions justify-end mt-6">
            <button
              onclick={sendTestNewSpeciesNotification}
              disabled={generating}
              class="btn btn-primary"
              class:btn-disabled={generating}
            >
              {#if !generating}
                <span class="flex items-center gap-2">
                  {@html systemIcons.bell}
                  <span>{t('settings.notifications.testNotification.sendButton')}</span>
                </span>
              {:else}
                <span class="loading loading-spinner loading-sm"></span>
                <span>{t('settings.notifications.testNotification.sendingButton')}</span>
              {/if}
            </button>
          </div>
        </div>
      </div>
    </div>
  </SettingsSection>

  <!-- Notification System Info Section -->
  <SettingsSection
    title={t('settings.notifications.sections.info.title')}
    description={t('settings.notifications.sections.info.description')}
    defaultOpen={false}
  >
    <div class="space-y-4">
      <div class="alert alert-info text-sm">
        <div class="h-5 w-5 flex-shrink-0">{@html alertIconsSvg.info}</div>
        <div class="min-w-0">
          <div class="font-semibold mb-1">
            {t('settings.notifications.systemInfo.howToView.title')}
          </div>
          <div class="space-y-1 text-xs">
            <p>{t('settings.notifications.systemInfo.howToView.step1')}</p>
            <p>{t('settings.notifications.systemInfo.howToView.step2')}</p>
            <p>{t('settings.notifications.systemInfo.howToView.step3')}</p>
          </div>
        </div>
      </div>

      <div class="bg-base-200 rounded-lg p-4">
        <h4 class="font-semibold text-sm mb-3">
          {t('settings.notifications.systemInfo.technicalDetails.title')}
        </h4>
        <div class="grid grid-cols-1 md:grid-cols-2 gap-4 text-xs">
          <div>
            <div class="font-medium text-base-content/80 mb-1">
              {t('settings.notifications.systemInfo.technicalDetails.endpoint')}
            </div>
            <code class="text-primary bg-base-100 px-2 py-1 rounded"
              >POST /api/v2/notifications/test/new-species</code
            >
          </div>
          <div>
            <div class="font-medium text-base-content/80 mb-1">
              {t('settings.notifications.systemInfo.technicalDetails.type')}
            </div>
            <span class="bg-base-100 px-2 py-1 rounded">TypeDetection</span>
          </div>
          <div>
            <div class="font-medium text-base-content/80 mb-1">
              {t('settings.notifications.systemInfo.technicalDetails.priority')}
            </div>
            <span class="bg-base-100 px-2 py-1 rounded">PriorityHigh</span>
          </div>
          <div>
            <div class="font-medium text-base-content/80 mb-1">
              {t('settings.notifications.systemInfo.technicalDetails.expiry')}
            </div>
            <span class="bg-base-100 px-2 py-1 rounded">24 hours</span>
          </div>
        </div>
      </div>
    </div>
  </SettingsSection>
</div>
