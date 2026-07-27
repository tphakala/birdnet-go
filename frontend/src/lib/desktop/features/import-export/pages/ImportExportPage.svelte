<script lang="ts">
  import { t } from '$lib/i18n';
  import Card from '$lib/desktop/components/ui/Card.svelte';
  import Button from '$lib/desktop/components/ui/Button.svelte';
  import Badge from '$lib/desktop/components/ui/Badge.svelte';
  import { Import, Upload, FileDown, ExternalLink } from '@lucide/svelte';
  import { appState } from '$lib/stores/appState.svelte';
  import BirdNetPiImportWizard from '../components/BirdNetPiImportWizard.svelte';
  import ImportActivityCard from '../components/ImportActivityCard.svelte';

  let wizardOpen = $state(false);
  // Bumped when the wizard closes or starts a job so the activity card
  // refetches import status.
  let activityRefresh = $state(0);

  function openWizard() {
    wizardOpen = true;
  }

  function refreshActivity() {
    activityRefresh += 1;
  }

  function closeWizard() {
    wizardOpen = false;
    refreshActivity();
  }
</script>

<div class="grid grid-cols-1 lg:grid-cols-3 gap-4 items-start">
  <!-- Activity panel first in DOM so it is visible without scrolling on
       stacked (sub-lg) layouts while an import runs; on lg it moves to the
       right column. -->
  <section class="lg:order-last" aria-labelledby="import-activity-heading">
    <ImportActivityCard refreshSignal={activityRefresh} onOpenWizard={openWizard} />
  </section>

  <Card padding={false} className="lg:col-span-2">
    <section aria-labelledby="import-sources-heading">
      <div class="px-6 pt-5 pb-1">
        <h3
          id="import-sources-heading"
          class="text-xs font-semibold uppercase tracking-wider text-[var(--color-base-content)]/60"
        >
          {t('system.importExport.import.sectionTitle')}
        </h3>
      </div>
      <ul class="divide-y divide-[var(--color-base-200)]">
        <!-- BirdNET-Pi import -->
        <li class="px-6 py-4 flex items-start gap-4">
          <Import class="size-5 text-[var(--color-primary)] mt-0.5 shrink-0" aria-hidden="true" />
          <div class="min-w-0 flex-1">
            <div class="flex items-center gap-2 flex-wrap">
              <h4 class="text-sm font-semibold text-[var(--color-base-content)]">
                {t('system.importExport.birdnetPi.cardTitle')}
              </h4>
              <Badge variant="warning" size="sm" text={t('system.importExport.experimental')} />
            </div>
            <p class="text-sm text-[var(--color-base-content)]/70 mt-0.5">
              {t('system.importExport.birdnetPi.cardDescription')}
            </p>
            <p class="text-xs text-[var(--color-base-content)]/60 mt-1.5">
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
            </p>
          </div>
          <Button variant="primary" size="sm" onclick={openWizard} className="shrink-0">
            <Import class="size-4" />
            {t('system.importExport.birdnetPi.startButton')}
          </Button>
        </li>
        <!-- birds.db upload (planned) -->
        <li class="px-6 py-4 flex items-start gap-4">
          <Upload
            class="size-5 text-[var(--color-base-content)]/40 mt-0.5 shrink-0"
            aria-hidden="true"
          />
          <div class="min-w-0 flex-1">
            <div class="flex items-center gap-2 flex-wrap">
              <h4 class="text-sm font-semibold text-[var(--color-base-content)]">
                {t('system.importExport.birdsDbUpload.cardTitle')}
              </h4>
              <Badge variant="neutral" size="sm" text={t('system.importExport.comingSoon')} />
            </div>
            <p class="text-sm text-[var(--color-base-content)]/60 mt-0.5">
              {t('system.importExport.birdsDbUpload.cardDescription')}
            </p>
          </div>
        </li>
      </ul>
    </section>

    <section
      aria-labelledby="export-sources-heading"
      class="border-t border-[var(--color-base-200)] pb-1"
    >
      <div class="px-6 pt-5 pb-1">
        <h3
          id="export-sources-heading"
          class="text-xs font-semibold uppercase tracking-wider text-[var(--color-base-content)]/60"
        >
          {t('system.importExport.export.sectionTitle')}
        </h3>
      </div>
      <ul>
        <!-- Detections export (planned) -->
        <li class="px-6 py-4 flex items-start gap-4">
          <FileDown
            class="size-5 text-[var(--color-base-content)]/40 mt-0.5 shrink-0"
            aria-hidden="true"
          />
          <div class="min-w-0 flex-1">
            <div class="flex items-center gap-2 flex-wrap">
              <h4 class="text-sm font-semibold text-[var(--color-base-content)]">
                {t('system.importExport.export.cardTitle')}
              </h4>
              <Badge variant="neutral" size="sm" text={t('system.importExport.comingSoon')} />
            </div>
            <p class="text-sm text-[var(--color-base-content)]/60 mt-0.5">
              {t('system.importExport.export.cardDescription')}
            </p>
          </div>
        </li>
      </ul>
    </section>
  </Card>
</div>

{#if wizardOpen}
  <BirdNetPiImportWizard onClose={closeWizard} onImportStarted={refreshActivity} />
{/if}
