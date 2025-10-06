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
  import { alertIconsSvg, systemIcons } from '$lib/utils/icons';
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
    { name: 'DetectionURL', description: 'Link to detection in UI' },
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

      templateStatusMessage = 'Templates saved successfully!';
      templateStatusType = 'success';

      setTimeout(() => {
        templateStatusMessage = '';
      }, 3000);
    } catch (error) {
      templateStatusMessage = `Error: ${(error as Error).message}`;
      templateStatusType = 'error';

      setTimeout(() => {
        templateStatusMessage = '';
      }, 5000);
    } finally {
      savingTemplate = false;
    }
  }

  function resetTemplates() {
    const confirmReset = window.confirm(
      'Reset templates to defaults? This will discard your current changes and restore the default template values.'
    );
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
        'You have unsaved template changes. The test will use the currently saved templates, not your edits. Continue?'
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
</script>

<div class="space-y-4 settings-page-content">
  <SettingsSection
    title="Notification Templates"
    description="Customize notification messages for new species detections using template variables"
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
            <h3 class="card-title text-base">New Species Notification Template</h3>

            <div class="space-y-4">
              <div class="form-control">
                <label for="template-title" class="label">
                  <span class="label-text font-semibold">Title Template</span>
                </label>
                <input
                  id="template-title"
                  type="text"
                  bind:value={editedTitle}
                  class="input input-bordered w-full font-mono text-sm"
                  placeholder="e.g., New Species: &#123;&#123;.CommonName&#125;&#125;"
                />
              </div>

              <div class="form-control">
                <label for="template-message" class="label">
                  <span class="label-text font-semibold">Message Template</span>
                </label>
                <textarea
                  id="template-message"
                  bind:value={editedMessage}
                  class="textarea textarea-bordered w-full font-mono text-sm"
                  rows="5"
                  placeholder="e.g., First detection of &#123;&#123;.CommonName&#125;&#125; (&#123;&#123;.ScientificName&#125;&#125;) with &#123;&#123;.ConfidencePercent&#125;&#125;% confidence"
                ></textarea>
              </div>

              {#if templateStatusMessage}
                <div
                  class="alert py-2 px-3 text-sm"
                  class:alert-success={templateStatusType === 'success'}
                  class:alert-error={templateStatusType === 'error'}
                >
                  <div class="h-4 w-4 flex-shrink-0">
                    {#if templateStatusType === 'success'}
                      {@html alertIconsSvg.success}
                    {:else if templateStatusType === 'error'}
                      {@html alertIconsSvg.error}
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
                  <span>{statusMessage}</span>
                </div>
              {/if}

              <div class="flex gap-2 justify-end">
                <button
                  onclick={resetTemplates}
                  class="btn btn-ghost btn-sm"
                  disabled={savingTemplate || generating}
                >
                  Reset
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
                    <span>Saving...</span>
                  {:else}
                    <span>Save Templates{hasUnsavedChanges ? ' *' : ''}</span>
                  {/if}
                </button>
                <button
                  onclick={sendTestNewSpeciesNotification}
                  disabled={generating || savingTemplate}
                  class="btn btn-secondary btn-sm"
                  title={hasUnsavedChanges
                    ? 'Warning: You have unsaved changes. Test will use saved templates.'
                    : 'Send a test notification'}
                >
                  {#if generating}
                    <span class="loading loading-spinner loading-xs"></span>
                    <span>Sending...</span>
                  {:else}
                    <span class="flex items-center gap-1">
                      {@html systemIcons.bell}
                      <span>Test</span>
                    </span>
                  {/if}
                </button>
              </div>
            </div>
          </div>
        </div>

        <div class="card bg-base-200">
          <div class="card-body">
            <h3 class="card-title text-base">Available Template Variables</h3>
            <p class="text-sm text-base-content/80 mb-3">
              Use these variables in your templates with the syntax <code
                class="bg-base-300 px-1 rounded">&#123;&#123;.VariableName&#125;&#125;</code
              >
            </p>

            <div class="grid grid-cols-1 md:grid-cols-2 gap-x-4 gap-y-2 text-xs">
              {#each templateFields as field}
                <div class="flex items-baseline gap-2">
                  <code class="font-mono text-primary shrink-0"
                    >&#123;&#123;.{field.name}&#125;&#125;</code
                  >
                  <span class="text-base-content/70">{field.description}</span>
                </div>
              {/each}
            </div>
          </div>
        </div>
      {/if}
    </div>
  </SettingsSection>
</div>
