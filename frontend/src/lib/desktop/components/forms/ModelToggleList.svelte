<!--
  Model Toggle List Component

  Purpose: The classifier list of an audio source whose settings save as they
  change. One ToggleField per listed model; every change hands the complete new
  list of model aliases to the parent, which saves it.

  Row states:
  - Loaded: name, the live backend and precision badge, sample rate warnings
  - Pending or failed to load: a state badge; it can be turned off, not on
  - Last one selected: stays on, with the reason
  - Missing: a selected alias the server no longer lists, shown raw with a
    Remove button that works whatever the list holds (not while the whole list
    is disabled)
  - Following defaults: an empty list shows the default models as on; the first
    toggle writes an explicit list

  List states: first load (skeleton), fetch failed (reason, Retry, the saved
  aliases read-only), no model enabled (the gallery link), a model that failed
  to load. The family hints (Perch only, run both, bat on a stream) follow.

  Props:
  - models: Models the server lists for the source editors
  - selectedModels: The source's saved aliases; empty means the default models
  - defaultModels: Aliases the source runs while it follows the defaults
  - sourceName: Name of the source, for the accessible name of Remove
  - sourceSampleRate: Capture rate the sample rate warnings compare against
  - isStream: Shows the bat-on-a-stream warning
  - disabled: Blocks every change
  - loading: True while the first fetch runs
  - failed: True when the fetch failed and no list was loaded
  - availability: Classifier verdict for the empty and degraded explanations
  - labelledBy: Id of the heading that names the group
  - onChange: Receives the complete new list
  - onRetry: Refetches the list after a failure

  @component
-->
<script lang="ts">
  import { AlertTriangle, Info, Loader2, X } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import { generateId } from '$lib/utils/uuid';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { SETTINGS_ROUTES, SYSTEM_ROUTES } from '$lib/utils/settingsRoutes';
  import { handleAppLinkClick } from '$lib/stores/navigation.svelte';
  import type { AcousticModelAvailability } from '$lib/types/models';
  import type { BackendModel } from '$lib/stores/models.svelte';
  import { effectiveSourceModels, sourceModelsWith } from '$lib/utils/defaultModelSelection';
  import ToggleField from './ToggleField.svelte';
  import SampleRateBadge from './SampleRateBadge.svelte';
  import Skeleton from '$lib/desktop/components/ui/Skeleton.svelte';
  import SkeletonStatus from '$lib/desktop/components/ui/SkeletonStatus.svelte';

  interface Props {
    models: BackendModel[];
    selectedModels: string[];
    defaultModels?: string[];
    sourceName: string;
    sourceSampleRate?: number;
    isStream?: boolean;
    disabled?: boolean;
    loading?: boolean;
    failed?: boolean;
    availability?: AcousticModelAvailability;
    labelledBy?: string;
    onChange: (_models: string[]) => void;
    onRetry?: () => void;
  }

  let {
    models,
    selectedModels,
    defaultModels = [],
    sourceName,
    sourceSampleRate = 48000,
    isStream = false,
    disabled = false,
    loading = false,
    failed = false,
    availability = { kind: 'unknown' },
    labelledBy,
    onChange,
    onRetry,
  }: Props = $props();

  const listId = generateId('model-toggles');

  /** Number of skeleton rows shown while the list loads. */
  const SKELETON_ROWS = 2;

  // Badge look shared by every row badge; the colour comes from the state.
  const BADGE_CLASS = 'inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-xs font-medium';
  const NOTE_CLASS =
    'flex items-start gap-2 p-2.5 rounded-lg text-xs leading-relaxed text-[var(--color-base-content)]';
  const INFO_NOTE_CLASS =
    'bg-[color-mix(in_srgb,var(--color-info)_10%,transparent)] border border-[color-mix(in_srgb,var(--color-info)_30%,transparent)]';
  const WARNING_NOTE_CLASS =
    'bg-[color-mix(in_srgb,var(--color-warning)_10%,transparent)] border border-[color-mix(in_srgb,var(--color-warning)_30%,transparent)]';

  let followsDefaults = $derived(selectedModels.length === 0);
  // What the source runs: its own list, or the defaults while it has none.
  let effective = $derived(effectiveSourceModels(selectedModels, defaultModels));
  let listedIds = $derived(new Set(models.map(m => m.id)));
  let listedOn = $derived(effective.filter(id => listedIds.has(id)));
  // Only an explicit list can name a model the server no longer lists.
  let missing = $derived([...new Set(selectedModels)].filter(id => !listedIds.has(id)));

  let showSkeleton = $derived(loading && models.length === 0);
  let showFailed = $derived(failed && !showSkeleton);
  let noModelReason = $derived(availability.kind === 'none' ? availability.reason : null);
  let showNoneEnabled = $derived(
    !showSkeleton && !showFailed && noModelReason === 'none_installed'
  );
  let showLoadFailed = $derived(!showSkeleton && !showFailed && noModelReason === 'load_failed');
  let showNoneAvailable = $derived(
    !showSkeleton && !showFailed && noModelReason === null && models.length === 0
  );

  let birdnetIds = $derived(
    models.filter(m => m.id.startsWith('birdnet') && m.category === 'bird').map(m => m.id)
  );
  let perchIds = $derived(
    models.filter(m => m.id.startsWith('perch') && m.category === 'bird').map(m => m.id)
  );
  let hasBothFamilies = $derived(birdnetIds.length > 0 && perchIds.length > 0);
  let onHasBirdnet = $derived(effective.some(id => birdnetIds.includes(id)));
  let onHasPerch = $derived(effective.some(id => perchIds.includes(id)));
  let showPerchOnlyWarning = $derived(hasBothFamilies && onHasPerch && !onHasBirdnet);
  let showRecommendBoth = $derived(hasBothFamilies && onHasBirdnet && !onHasPerch);
  let showBatStreamWarning = $derived(
    isStream && effective.some(id => models.find(m => m.id === id)?.category === 'bat')
  );

  function runtimeBadge(model: BackendModel): string {
    return [model.backend, model.precision].filter(Boolean).join(' · ');
  }

  /** Why a row's switch is blocked, or undefined when it can change. */
  function blockedReason(model: BackendModel, isOn: boolean): string | undefined {
    const notLoaded = model.state === 'failed' || model.state === 'pending';
    if (!isOn && notLoaded) return t('settings.audio.models.notLoadedHelp');
    if (isOn && listedOn.length === 1) return t('settings.audio.models.lastSelectedHelp');
    return undefined;
  }

  function setModel(id: string, on: boolean) {
    const next = on
      ? sourceModelsWith(selectedModels, defaultModels, id)
      : effective.filter(existing => existing !== id);
    onChange(next);
  }

  let group = $state<HTMLDivElement>();

  // The removed row takes its focused button with it, so focus stays in the group.
  function removeMissing(id: string) {
    onChange(selectedModels.filter(existing => existing !== id));
    group?.focus();
  }
</script>

<div
  bind:this={group}
  class="space-y-2 outline-none"
  role="group"
  aria-labelledby={labelledBy}
  tabindex="-1"
>
  {#if showSkeleton}
    <div class="space-y-3 py-1">
      {#each { length: SKELETON_ROWS } as _, row (row)}
        <div class="flex items-center justify-between gap-4">
          <Skeleton className="h-4 w-40" />
          <Skeleton className="h-5 w-10 rounded-full" />
        </div>
      {/each}
      <SkeletonStatus label={t('settings.audio.models.loading')} class="text-xs" />
    </div>
  {:else if showFailed}
    <div class={cn(NOTE_CLASS, WARNING_NOTE_CLASS, 'items-center')}>
      <AlertTriangle class="size-3.5 shrink-0 text-[var(--text-warning)]" aria-hidden="true" />
      <span class="flex-1">{t('settings.audio.models.listFailed')}</span>
      {#if onRetry}
        <button type="button" class="btn btn-ghost btn-xs" onclick={onRetry}>
          {t('common.retry')}
        </button>
      {/if}
    </div>
    {#if followsDefaults}
      <p class="help-text">{t('settings.audio.models.followsDefaults')}</p>
    {:else}
      <ul class="space-y-1 pl-1">
        {#each new Set(selectedModels) as alias (alias)}
          <li class="font-mono text-sm text-[var(--color-base-content)]">{alias}</li>
        {/each}
      </ul>
    {/if}
  {:else}
    {#if followsDefaults && listedOn.length > 0}
      <p class="help-text">{t('settings.audio.models.followsDefaults')}</p>
    {/if}

    <ul id={listId} class="divide-y divide-[var(--border-100)]">
      {#each models as model (model.id)}
        {@const isOn = effective.includes(model.id)}
        {@const reason = blockedReason(model, isOn)}
        {@const runtime = runtimeBadge(model)}
        {@const badgesId = `${listId}-${model.id}-badges`}
        <li class="py-2 first:pt-0 last:pb-0">
          <ToggleField
            label={model.name}
            value={isOn}
            size="sm"
            disabled={disabled || reason !== undefined}
            description={reason}
            describedBy={badgesId}
            onUpdate={on => setModel(model.id, on)}
          >
            {#snippet badges()}
              <span id={badgesId} class="flex flex-wrap items-center gap-1.5">
                {#if runtime}
                  <span
                    class={cn(BADGE_CLASS, 'bg-[var(--color-base-200)] text-muted font-normal')}
                  >
                    {runtime}
                  </span>
                {/if}
                {#if model.state === 'pending'}
                  <span
                    class={cn(BADGE_CLASS, 'bg-[var(--color-info)]/15 text-[var(--text-info)]')}
                  >
                    <Loader2
                      class="size-3 animate-spin motion-reduce:animate-none"
                      aria-hidden="true"
                    />
                    {t('settings.audio.models.state.pending')}
                  </span>
                {:else if model.state === 'failed'}
                  <span
                    class={cn(
                      BADGE_CLASS,
                      'bg-[var(--color-warning)]/15 text-[var(--text-warning)]'
                    )}
                  >
                    <AlertTriangle class="size-3" aria-hidden="true" />
                    {t('settings.audio.models.state.failed')}
                  </span>
                {/if}
                <SampleRateBadge
                  sampleRate={sourceSampleRate}
                  minRate={model.minSampleRate}
                  recommendedRate={model.recommendedSampleRate}
                />
              </span>
            {/snippet}
          </ToggleField>
          {#if model.state === 'failed'}
            <a
              href={buildAppUrl(SYSTEM_ROUTES.inference)}
              onclick={handleAppLinkClick}
              class="mt-1 inline-block text-xs underline text-[var(--text-primary)]"
            >
              {t('settings.audio.models.loadFailedLink')}
            </a>
          {/if}
        </li>
      {/each}

      {#each missing as alias (alias)}
        {@const helpId = `${listId}-missing-${alias}`}
        <li class="flex items-start justify-between gap-4 py-2 first:pt-0 last:pb-0">
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
              <span class="font-mono text-sm break-all text-[var(--color-base-content)]">
                {alias}
              </span>
              <span
                class={cn(BADGE_CLASS, 'bg-[var(--color-warning)]/15 text-[var(--text-warning)]')}
              >
                <AlertTriangle class="size-3" aria-hidden="true" />
                {t('settings.audio.models.state.missing')}
              </span>
            </div>
            <p id={helpId} class="help-text mt-1">{t('settings.audio.models.missingHelp')}</p>
          </div>
          <button
            type="button"
            class="btn btn-ghost btn-sm shrink-0 gap-1"
            aria-label={t('settings.audio.models.removeMissingAria', {
              model: alias,
              source: sourceName,
            })}
            aria-describedby={helpId}
            aria-disabled={disabled ? 'true' : undefined}
            onclick={() => {
              if (!disabled) removeMissing(alias);
            }}
          >
            <X class="size-4" aria-hidden="true" />
            {t('settings.audio.models.removeMissing')}
          </button>
        </li>
      {/each}
    </ul>

    {#if showNoneEnabled}
      <div class={cn(NOTE_CLASS, INFO_NOTE_CLASS)}>
        <Info class="size-3.5 shrink-0 mt-0.5 text-[var(--text-info)]" aria-hidden="true" />
        <span>
          <span class="font-medium">{t('settings.audio.models.noneEnabledTitle')}</span>
          {t('settings.audio.models.noneEnabledHelp')}
          <a
            href={buildAppUrl(SETTINGS_ROUTES.analysisModels)}
            onclick={handleAppLinkClick}
            class="underline text-[var(--text-primary)]"
          >
            {t('settings.audio.models.noneEnabledLink')}
          </a>
        </span>
      </div>
    {:else if showLoadFailed}
      <div class={cn(NOTE_CLASS, WARNING_NOTE_CLASS)}>
        <AlertTriangle
          class="size-3.5 shrink-0 mt-0.5 text-[var(--text-warning)]"
          aria-hidden="true"
        />
        <span>
          {t('settings.audio.models.loadFailedWarning')}
          <a
            href={buildAppUrl(SYSTEM_ROUTES.inference)}
            onclick={handleAppLinkClick}
            class="underline text-[var(--text-primary)]"
          >
            {t('settings.audio.models.loadFailedLink')}
          </a>
        </span>
      </div>
    {:else if showNoneAvailable}
      <div class={cn(NOTE_CLASS, INFO_NOTE_CLASS)}>
        <Info class="size-3.5 shrink-0 mt-0.5 text-[var(--text-info)]" aria-hidden="true" />
        <span>
          {t('settings.audio.models.noneAvailable')}
          <a
            href={buildAppUrl(SETTINGS_ROUTES.analysisModels)}
            onclick={handleAppLinkClick}
            class="underline text-[var(--text-primary)]"
          >
            {t('settings.audio.models.noneEnabledLink')}
          </a>
        </span>
      </div>
    {/if}

    {#if showPerchOnlyWarning}
      <div class={cn(NOTE_CLASS, WARNING_NOTE_CLASS)}>
        <AlertTriangle
          class="size-3.5 shrink-0 mt-0.5 text-[var(--text-warning)]"
          aria-hidden="true"
        />
        <span>{t('settings.audio.models.perchOnlyWarning')}</span>
      </div>
    {:else if showRecommendBoth}
      <div class={cn(NOTE_CLASS, INFO_NOTE_CLASS)}>
        <Info class="size-3.5 shrink-0 mt-0.5 text-[var(--text-info)]" aria-hidden="true" />
        <span>{t('settings.audio.models.recommendBoth')}</span>
      </div>
    {/if}

    {#if showBatStreamWarning}
      <div class={cn(NOTE_CLASS, WARNING_NOTE_CLASS)}>
        <AlertTriangle
          class="size-3.5 shrink-0 mt-0.5 text-[var(--text-warning)]"
          aria-hidden="true"
        />
        <span>{t('settings.audio.streams.batWarning')}</span>
      </div>
    {/if}
  {/if}
</div>
