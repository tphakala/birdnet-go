<script lang="ts">
  import { t } from '$lib/i18n';
  import Card from '$lib/desktop/components/ui/Card.svelte';
  import Button from '$lib/desktop/components/ui/Button.svelte';
  import Badge from '$lib/desktop/components/ui/Badge.svelte';
  import ErrorAlert from '$lib/desktop/components/ui/ErrorAlert.svelte';
  import { Import, ExternalLink } from '@lucide/svelte';
  import { appState } from '$lib/stores/appState.svelte';
  import BirdNetPiImportWizard from '../components/BirdNetPiImportWizard.svelte';

  let wizardOpen = $state(false);

  function openWizard() {
    wizardOpen = true;
  }

  function closeWizard() {
    wizardOpen = false;
  }
</script>

<div class="space-y-6">
  <!-- Import section -->
  <section aria-labelledby="import-section-heading">
    <h3
      id="import-section-heading"
      class="text-lg font-medium text-[var(--color-base-content)] mb-3"
    >
      {t('system.importExport.import.sectionTitle')}
    </h3>
    <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
      <!-- BirdNET-Pi import card -->
      <Card>
        {#snippet header()}
          <div class="flex items-center justify-between">
            <div class="flex items-center gap-2">
              <Import class="size-5 text-[var(--color-primary)]" />
              <h4 class="text-base font-semibold text-[var(--color-base-content)]">
                {t('system.importExport.birdnetPi.cardTitle')}
              </h4>
            </div>
            <Badge variant="warning" size="sm" text={t('system.importExport.experimental')} />
          </div>
        {/snippet}
        <p class="text-sm text-[var(--color-base-content)]/70 mb-4">
          {t('system.importExport.birdnetPi.cardDescription')}
        </p>
        <ErrorAlert type="warning" role="note" className="mb-4">
          {t('system.importExport.birdnetPi.experimentalNotice')}
          <a
            href={appState.projectLinks.newIssueUrl}
            target="_blank"
            rel="noopener noreferrer"
            aria-label={t('navigation.reportBugAriaLabel')}
            class="inline-flex items-center gap-1 font-medium underline text-[var(--color-primary)] hover:opacity-80"
          >
            {t('system.importExport.birdnetPi.reportBug')}
            <ExternalLink class="size-3" aria-hidden="true" />
          </a>
        </ErrorAlert>
        <Button variant="primary" onclick={openWizard}>
          <Import class="size-4" />
          {t('system.importExport.birdnetPi.startButton')}
        </Button>
      </Card>
    </div>
  </section>

  <!-- Export section (coming soon) -->
  <section aria-labelledby="export-section-heading">
    <h3
      id="export-section-heading"
      class="text-lg font-medium text-[var(--color-base-content)] mb-3"
    >
      {t('system.importExport.export.sectionTitle')}
    </h3>
    <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
      <Card>
        {#snippet header()}
          <div class="flex items-center justify-between">
            <h4 class="text-base font-semibold text-[var(--color-base-content)]/50">
              {t('system.importExport.export.cardTitle')}
            </h4>
            <Badge variant="neutral" size="sm" text={t('system.importExport.comingSoon')} />
          </div>
        {/snippet}
        <p class="text-sm text-[var(--color-base-content)]/50 mb-4">
          {t('system.importExport.export.cardDescription')}
        </p>
        <Button
          variant="default"
          disabled={true}
          title={t('system.importExport.export.disabledReason')}
          aria-describedby="export-disabled-reason"
        >
          {t('system.importExport.export.startButton')}
        </Button>
        <p id="export-disabled-reason" class="text-xs text-[var(--color-base-content)]/50 mt-2">
          {t('system.importExport.export.disabledReason')}
        </p>
      </Card>
    </div>
  </section>
</div>

{#if wizardOpen}
  <BirdNetPiImportWizard onClose={closeWizard} />
{/if}
