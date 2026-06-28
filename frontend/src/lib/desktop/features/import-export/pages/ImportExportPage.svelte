<script lang="ts">
  import { t } from '$lib/i18n';
  import Card from '$lib/desktop/components/ui/Card.svelte';
  import Button from '$lib/desktop/components/ui/Button.svelte';
  import Badge from '$lib/desktop/components/ui/Badge.svelte';
  import { Import } from '@lucide/svelte';
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
            <Badge variant="success" size="sm" text={t('system.importExport.available')} />
          </div>
        {/snippet}
        <p class="text-sm text-[var(--color-base-content)]/70 mb-4">
          {t('system.importExport.birdnetPi.cardDescription')}
        </p>
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
