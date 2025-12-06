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
  import { onMount } from 'svelte';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import SettingsTabs from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import type { TabDefinition } from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import { Info, CircleCheck, XCircle, Bell, Send } from '@lucide/svelte';
  import { t } from '$lib/i18n';

  let csrfToken = $derived(
    (document.querySelector('meta[name="csrf-token"]') as HTMLElement)?.getAttribute('content') ||
      ''
  );

  let templateConfig = $state<{
    title: string;
    message: string;
  } | null>(null);
  let loadingTemplate = $state(false);
  let savingTemplate = $state(false);
  let templateStatusMessage = $state('');
  let templateStatusType = $state<'info' | 'success' | 'error'>('info');

  let editedTitle = $state('');
  let editedMessage = $state('');

  let hasUnsavedChanges = $derived(
    templateConfig !== null &&
      (editedTitle !== templateConfig.title || editedMessage !== templateConfig.message)
  );

  let generating = $state(false);
  let statusMessage = $state('');
  let statusType = $state<'info' | 'success' | 'error'>('info');

  const templateFields = [
    { name: 'CommonName', description: 'Bird common name (e.g., "Northern Cardinal")' },
    { name: 'ScientificName', description: 'Scientific name (e.g., "Cardinalis cardinalis")' },
    { name: 'Confidence', description: 'Confidence value (0.0 to 1.0)' },
    { name: 'ConfidencePercent', description: 'Confidence as percentage (e.g., "99")' },
    { name: 'DetectionTime', description: 'Time of detection (e.g., "14:30:45")' },
    { name: 'DetectionDate', description: 'Date of detection (e.g., "2024-10-05")' },
    { name: 'Latitude', description: 'GPS latitude coordinate' },
    { name: 'Longitude', description: 'GPS longitude coordinate' },
    { name: 'Location', description: 'Formatted coordinates (e.g., "42.360100, -71.058900")' },
    { name: 'DetectionID', description: 'Detection ID number (e.g., "1234")' },
    {
      name: 'DetectionPath',
      description: 'Relative path to detection (e.g., "/ui/detections/1234")',
    },
    { name: 'DetectionURL', description: 'Full URL to detection in UI' },
    { name: 'ImageURL', description: 'Link to species image' },
    { name: 'DaysSinceFirstSeen', description: 'Number of days since first detected' },
  ];

  const defaultTemplate = {
    title: 'New Species: {{.CommonName}}',
    message:
      '{{.ImageURL}}\n\nFirst detection of {{.CommonName}} ({{.ScientificName}}) with {{.ConfidencePercent}}% confidence at {{.DetectionTime}}.\n\n{{.DetectionURL}}',
  };

  async function loadTemplateConfig() {
    loadingTemplate = true;
    try {
      const response = await fetch('/api/v2/settings/notification');
      if (response.ok) {
        const data = await response.json();
        if (data.templates?.newSpecies) {
          templateConfig = {
            title: data.templates.newSpecies.title ?? defaultTemplate.title,
            message: data.templates.newSpecies.message ?? defaultTemplate.message,
          };
          editedTitle = templateConfig.title;
          editedMessage = templateConfig.message;
        } else {
          templateConfig = { ...defaultTemplate };
          editedTitle = templateConfig.title;
          editedMessage = templateConfig.message;
        }
      }
    } catch {
      templateConfig = { ...defaultTemplate };
      editedTitle = templateConfig.title;
      editedMessage = templateConfig.message;
    } finally {
      loadingTemplate = false;
    }
  }

  async function saveTemplateConfig() {
    savingTemplate = true;
    templateStatusMessage = '';

    try {
      const headers = new Headers({
        'Content-Type': 'application/json',
      });

      if (csrfToken) {
        headers.set('X-CSRF-Token', csrfToken);
      }

      const response = await fetch('/api/v2/settings/notification', {
        method: 'PATCH',
        headers,
        credentials: 'same-origin',
        body: JSON.stringify({
          templates: {
            newSpecies: {
              title: editedTitle,
              message: editedMessage,
            },
          },
        }),
      });

      if (!response.ok) {
        throw new Error(`Failed to save: ${response.status} ${response.statusText}`);
      }

      if (templateConfig) {
        templateConfig.title = editedTitle;
        templateConfig.message = editedMessage;
      }

      templateStatusMessage = t('settings.notifications.templates.saveSuccess');
      templateStatusType = 'success';

      setTimeout(() => {
        templateStatusMessage = '';
      }, 3000);
    } catch (error) {
      templateStatusMessage = t('settings.notifications.templates.saveError', {
        message: (error as Error).message,
      });
      templateStatusType = 'error';

      setTimeout(() => {
        templateStatusMessage = '';
      }, 5000);
    } finally {
      savingTemplate = false;
    }
  }

  function resetTemplates() {
    const confirmReset = window.confirm(t('settings.notifications.templates.resetConfirm'));
    if (!confirmReset) {
      return;
    }

    editedTitle = defaultTemplate.title;
    editedMessage = defaultTemplate.message;
  }

  async function sendTestNewSpeciesNotification() {
    // Check for unsaved changes
    if (hasUnsavedChanges) {
      const confirmTest = window.confirm(
        t('settings.notifications.templates.unsavedChangesWarning')
      );
      if (!confirmTest) {
        return;
      }
    }

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

  onMount(() => {
    loadTemplateConfig();
  });

  // Tab state
  let activeTab = $state('templates');

  // Tab definitions
  let tabs = $derived<TabDefinition[]>([
    {
      id: 'templates',
      label: t('settings.notifications.templates.title'),
      icon: Bell,
      content: templatesTabContent,
      hasChanges: hasUnsavedChanges,
    },
    {
      id: 'push',
      label: t('settings.notifications.push.title'),
      icon: Send,
      content: pushTabContent,
      hasChanges: false,
    },
  ]);
</script>

<!-- Templates Tab Content -->
{#snippet templatesTabContent()}
  <div class="space-y-4">
    <SettingsSection
      title={t('settings.notifications.templates.title')}
      description={t('settings.notifications.templates.description')}
      defaultOpen={true}
    >
      <div class="space-y-4">
        {#if loadingTemplate}
          <div class="flex justify-center py-4">
            <span class="loading loading-spinner loading-md"></span>
          </div>
        {:else if templateConfig}
          <div class="card bg-base-200">
            <div class="card-body">
              <h3 class="card-title text-base">
                {t('settings.notifications.templates.newSpeciesTitle')}
              </h3>

              <div class="space-y-4">
                <div class="form-control">
                  <label for="template-title" class="label">
                    <span class="label-text font-semibold"
                      >{t('settings.notifications.templates.titleLabel')}</span
                    >
                  </label>
                  <input
                    id="template-title"
                    type="text"
                    bind:value={editedTitle}
                    class="input w-full font-mono text-sm"
                    placeholder={t('settings.notifications.templates.titlePlaceholder')}
                  />
                </div>

                <div class="form-control">
                  <label for="template-message" class="label">
                    <span class="label-text font-semibold"
                      >{t('settings.notifications.templates.messageLabel')}</span
                    >
                  </label>
                  <textarea
                    id="template-message"
                    bind:value={editedMessage}
                    class="textarea w-full font-mono text-sm"
                    rows="6"
                    placeholder={t('settings.notifications.templates.messagePlaceholder')}
                  ></textarea>
                </div>

                {#if templateStatusMessage}
                  <div
                    class="alert py-2 px-3 text-sm"
                    class:alert-success={templateStatusType === 'success'}
                    class:alert-error={templateStatusType === 'error'}
                    role="alert"
                    aria-live="assertive"
                  >
                    <div class="shrink-0">
                      {#if templateStatusType === 'success'}
                        <CircleCheck class="size-4" />
                      {:else if templateStatusType === 'error'}
                        <XCircle class="size-4" />
                      {/if}
                    </div>
                    <span>{templateStatusMessage}</span>
                  </div>
                {/if}

                {#if statusMessage}
                  <div
                    class="alert py-2 px-3 text-sm"
                    class:alert-info={statusType === 'info'}
                    class:alert-success={statusType === 'success'}
                    class:alert-error={statusType === 'error'}
                    role="status"
                    aria-live="polite"
                  >
                    <div class="shrink-0">
                      {#if statusType === 'info'}
                        <Info class="size-4" />
                      {:else if statusType === 'success'}
                        <CircleCheck class="size-4" />
                      {:else if statusType === 'error'}
                        <XCircle class="size-4" />
                      {/if}
                    </div>
                    <span>{statusMessage}</span>
                  </div>
                {/if}

                <div class="flex gap-2 justify-end">
                  <button
                    onclick={resetTemplates}
                    class="btn btn-ghost btn-sm"
                    disabled={savingTemplate || generating}
                  >
                    {t('settings.notifications.templates.resetButton')}
                  </button>
                  <button
                    onclick={saveTemplateConfig}
                    class="btn btn-sm"
                    class:btn-primary={hasUnsavedChanges}
                    class:btn-ghost={!hasUnsavedChanges}
                    disabled={savingTemplate || generating || !hasUnsavedChanges}
                  >
                    {#if savingTemplate}
                      <span class="loading loading-spinner loading-xs"></span>
                      <span>{t('settings.notifications.templates.savingButton')}</span>
                    {:else}
                      <span
                        >{hasUnsavedChanges
                          ? t('settings.notifications.templates.saveButtonUnsaved')
                          : t('settings.notifications.templates.saveButton')}</span
                      >
                    {/if}
                  </button>
                  <button
                    onclick={sendTestNewSpeciesNotification}
                    disabled={generating || savingTemplate}
                    class="btn btn-secondary btn-sm"
                    title={hasUnsavedChanges
                      ? t('settings.notifications.templates.testWithUnsavedChanges')
                      : t('settings.notifications.templates.testNormal')}
                  >
                    {#if generating}
                      <span class="loading loading-spinner loading-xs"></span>
                      <span>{t('settings.notifications.templates.sendingButton')}</span>
                    {:else}
                      <span class="flex items-center gap-1">
                        <Bell class="size-4" />
                        <span>{t('settings.notifications.templates.testButton')}</span>
                      </span>
                    {/if}
                  </button>
                </div>
              </div>
            </div>
          </div>

          <div class="card bg-base-200">
            <div class="card-body">
              <h3 class="card-title text-base">
                {t('settings.notifications.templates.availableVariables')}
              </h3>
              <p class="text-sm text-base-content/80 mb-3">
                {t('settings.notifications.templates.variablesDescription')}
                <code class="bg-base-300 px-1 rounded-sm"
                  >&#123;&#123;.VariableName&#125;&#125;</code
                >
              </p>

              <div class="grid grid-cols-1 md:grid-cols-2 gap-x-4 gap-y-2 text-xs">
                {#each templateFields as field (field.name)}
                  <div class="flex items-baseline gap-2">
                    <code class="font-mono text-primary shrink-0"
                      >&#123;&#123;.{field.name}&#125;&#125;</code
                    >
                    <span class="text-base-content/70">{field.description}</span>
                  </div>
                {/each}
              </div>

              <!-- Privacy Note - Collapsible -->
              <details class="mt-4 text-xs">
                <summary
                  class="cursor-pointer text-base-content/60 hover:text-base-content/80 flex items-center gap-1"
                >
                  <Info class="size-3.5" />
                  {t('settings.notifications.privacy.title')}
                </summary>
                <div class="mt-2 pl-5 text-base-content/60 space-y-1">
                  <p>{t('settings.notifications.privacy.description')}</p>
                  <p>{t('settings.notifications.privacy.recommendation')}</p>
                </div>
              </details>
            </div>
          </div>
        {/if}
      </div>
    </SettingsSection>
  </div>
{/snippet}

<!-- Push Settings Tab Content -->
{#snippet pushTabContent()}
  <SettingsSection
    title={t('settings.notifications.push.title')}
    description={t('settings.notifications.push.description')}
    defaultOpen={true}
  >
    <div class="text-center py-8 text-base-content/60">
      <Send class="size-12 mx-auto mb-4 opacity-50" />
      <p class="text-sm">{t('settings.notifications.push.comingSoon')}</p>
      <p class="text-xs mt-2">{t('settings.notifications.push.comingSoonDescription')}</p>
    </div>
  </SettingsSection>
{/snippet}

<main class="settings-page-content" aria-label="Notifications settings configuration">
  <SettingsTabs {tabs} bind:activeTab />
</main>
