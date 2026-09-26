<!--
  AcousticModelBanner - Dashboard banner shown while no acoustic model is
  analyzing audio.

  Watches the shared acoustic model store (one topology SSE per dashboard) and
  renders for the two explicit no-model verdicts and for loaded models that fail
  every analysis:
  - none_installed (N=0): a warning status with a link to the model gallery.
  - load_failed: an error alert with a link to the AI Models status page.
  - failing (a loaded model failed its last analyses in a row): an error alert
    naming the models, with a link to the AI Models status page.
  "ok" with no failing model, the "" sentinel, null and unknown verdicts render
  nothing. There is no dismiss: the banner clears itself as soon as a model
  loads or recovers (the server broadcasts both over the topology SSE).

  @component
-->
<script lang="ts">
  import { TriangleAlert, CircleX } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { SETTINGS_ROUTES, SYSTEM_ROUTES } from '$lib/utils/settingsRoutes';
  import { handleAppLinkClick } from '$lib/stores/navigation.svelte';
  import {
    acousticFailingModels,
    acousticModelsState,
    watchAcousticModels,
  } from '$lib/stores/acousticModels.svelte';
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

  // Only while models are loaded: a no-model verdict has its own variant above.
  let failingModels = $derived(noModelState === null ? acousticFailingModels() : []);
  let failingNames = $derived(failingModels.map(m => m.name).join(', '));
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
{:else if failingModels.length > 0}
  <div
    role="alert"
    data-testid="acoustic-model-failing-banner"
    class={cn(
      'flex flex-col gap-3 rounded-2xl border p-4 sm:flex-row sm:items-center',
      'border-[color-mix(in_srgb,var(--color-error)_40%,transparent)] bg-[color-mix(in_srgb,var(--color-error)_10%,var(--color-base-100))]',
      className
    )}
  >
    <CircleX class="size-6 shrink-0 text-[var(--color-error)]" aria-hidden="true" />
    <div class="min-w-0 flex-1">
      <p class="font-semibold text-[var(--color-base-content)]">
        {t('dashboard.acousticModels.failingTitle', { count: failingModels.length })}
      </p>
      <p class="text-sm text-[var(--color-base-content)]/80">
        {t('dashboard.acousticModels.failingMessage', {
          count: failingModels.length,
          models: failingNames,
        })}
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
