<!--
  AcousticModelBanner - Dashboard banner shown while no acoustic model is loaded.

  Watches the shared acoustic model store (one topology SSE per dashboard) and
  renders only for the two explicit no-model verdicts:
  - none_installed (N=0): a warning status with a link to the model gallery.
  - load_failed: an error alert with a link to the AI Models status page.
  "ok", the "" sentinel, null and unknown verdicts render nothing. There is no
  dismiss: the banner clears itself as soon as a model loads.

  @component
-->
<script lang="ts">
  import { TriangleAlert, CircleX } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { SETTINGS_ROUTES, SYSTEM_ROUTES } from '$lib/utils/settingsRoutes';
  import { handleAppLinkClick } from '$lib/stores/navigation.svelte';
  import { acousticModelsState, watchAcousticModels } from '$lib/stores/acousticModels.svelte';
  import { isNoAcousticModelState, type NoAcousticModelState } from '$lib/types/models';

  interface Props {
    class?: string;
  }

  let { class: className = '' }: Props = $props();

  $effect(() => watchAcousticModels());

  let noModelState = $derived.by((): NoAcousticModelState | null => {
    const state = acousticModelsState();
    return isNoAcousticModelState(state) ? state : null;
  });
</script>

{#if noModelState === 'none_installed'}
  <div
    role="status"
    class={cn(
      'flex flex-col gap-3 rounded-2xl border p-4 sm:flex-row sm:items-center',
      'border-[color-mix(in_srgb,var(--color-warning)_40%,transparent)] bg-[color-mix(in_srgb,var(--color-warning)_10%,var(--color-base-100))]',
      className
    )}
  >
    <TriangleAlert class="size-6 shrink-0 text-[var(--color-warning)]" aria-hidden="true" />
    <div class="min-w-0 flex-1">
      <p class="font-semibold text-[var(--color-base-content)]">
        {t('dashboard.acousticModels.noneTitle')}
      </p>
      <p class="text-sm text-[var(--color-base-content)]/80">
        {t('dashboard.acousticModels.noneMessage')}
      </p>
    </div>
    <a
      href={buildAppUrl(SETTINGS_ROUTES.analysisModels)}
      onclick={handleAppLinkClick}
      class="btn btn-sm btn-warning shrink-0 self-start sm:self-auto"
    >
      {t('dashboard.acousticModels.noneAction')}
    </a>
  </div>
{:else if noModelState === 'load_failed'}
  <div
    role="alert"
    class={cn(
      'flex flex-col gap-3 rounded-2xl border p-4 sm:flex-row sm:items-center',
      'border-[color-mix(in_srgb,var(--color-error)_40%,transparent)] bg-[color-mix(in_srgb,var(--color-error)_10%,var(--color-base-100))]',
      className
    )}
  >
    <CircleX class="size-6 shrink-0 text-[var(--color-error)]" aria-hidden="true" />
    <div class="min-w-0 flex-1">
      <p class="font-semibold text-[var(--color-base-content)]">
        {t('dashboard.acousticModels.loadFailedTitle')}
      </p>
      <p class="text-sm text-[var(--color-base-content)]/80">
        {t('dashboard.acousticModels.loadFailedMessage')}
      </p>
    </div>
    <a
      href={buildAppUrl(SYSTEM_ROUTES.inference)}
      onclick={handleAppLinkClick}
      class="btn btn-sm btn-error shrink-0 self-start sm:self-auto"
    >
      {t('dashboard.acousticModels.loadFailedAction')}
    </a>
  </div>
{/if}
