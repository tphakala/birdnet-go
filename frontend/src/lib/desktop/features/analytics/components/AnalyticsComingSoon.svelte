<script lang="ts">
  import type { Component } from 'svelte';
  import { t, type TranslationKey } from '$lib/i18n';
  import { analyticsControls } from '../registry/analyticsControls.svelte';

  interface Props {
    titleKey: TranslationKey;
    icon: Component;
    descriptionKey: TranslationKey;
    featureKeys: TranslationKey[];
  }

  let { titleKey, icon: Icon, descriptionKey, featureKeys }: Props = $props();

  // Apply any filter query carried in the URL we mounted on, then register the
  // popstate listener. Mirrors AnalyticsPageShell - see its comment for the
  // full rationale. Coming-soon views need no data fetches, so
  // unlike the shell we do not call ensureSpecies/ensureSources.
  $effect(() => {
    analyticsControls.syncFromUrl(); // honor filter query carried in the URL we mounted on
    return analyticsControls.init(); // register the ref-counted popstate listener; its cleanup is the teardown
  });
</script>

<section class="flex flex-col gap-4" aria-labelledby="analytics-page-title">
  <h1 id="analytics-page-title" class="text-2xl font-bold text-[var(--color-base-content)]">
    {t(titleKey)}
  </h1>

  <div
    class="flex flex-col items-center gap-4 rounded-xl border border-[var(--color-base-200)] bg-[var(--color-base-100)] p-8 text-center shadow-sm"
  >
    <Icon class="size-12 text-[var(--color-base-content)] opacity-40" aria-hidden="true" />

    <span class="badge badge-primary badge-sm">{t('analytics.comingSoon.badge')}</span>

    <p class="max-w-prose text-[var(--color-base-content)] opacity-70">{t(descriptionKey)}</p>

    <div class="flex w-full max-w-prose flex-col gap-2 text-left">
      <h2 class="text-lg font-semibold text-[var(--color-base-content)]">
        {t('analytics.comingSoon.plannedTitle')}
      </h2>
      <ul class="list-disc space-y-1 pl-5 text-[var(--color-base-content)] opacity-70">
        {#each featureKeys as featureKey (featureKey)}
          <li>{t(featureKey)}</li>
        {/each}
      </ul>
    </div>
  </div>
</section>
