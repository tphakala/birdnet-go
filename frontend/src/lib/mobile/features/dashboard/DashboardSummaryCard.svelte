<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { t } from '$lib/i18n';

  interface Props {
    totalDetections: number;
    uniqueSpecies: number;
    topSpecies: string | null;
    className?: string;
  }

  let { totalDetections, uniqueSpecies, topSpecies, className = '' }: Props = $props();
</script>

<div class={cn('card bg-base-100 shadow-sm', className)}>
  <div class="card-body p-4">
    <h2 class="card-title text-base">{t('dashboard.dailySummary.title')}</h2>

    <div class="grid grid-cols-2 gap-4 mt-2">
      <div class="text-center">
        <div class="text-3xl font-bold text-primary">{totalDetections}</div>
        <div class="text-xs text-base-content/60">
          {t('dashboard.dailySummary.columns.detections')}
        </div>
      </div>

      <div class="text-center">
        <div class="text-3xl font-bold text-secondary">{uniqueSpecies}</div>
        <div class="text-xs text-base-content/60">
          {t('dashboard.dailySummary.columns.species')}
        </div>
      </div>
    </div>

    {#if topSpecies}
      <div class="mt-3 pt-3 border-t border-base-200 text-center">
        <div class="text-xs text-base-content/60">Top Species</div>
        <div class="font-medium mt-1">{topSpecies}</div>
      </div>
    {/if}
  </div>
</div>
