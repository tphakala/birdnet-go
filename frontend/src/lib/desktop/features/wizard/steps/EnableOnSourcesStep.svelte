<!--
  Enable On Sources Step

  Purpose: The first recommended step after a model install. Installing a model
  does not add it to the audio sources that list their models, so this step
  offers to turn the new model on for every sound card and stream, or for
  chosen ones. Nothing is saved until Done; closing without Done starts no save.

  States: the model list loading (skeleton) or failed to load (reason, Retry);
  the model not listed, still loading or failed to load (explained, no
  switches); no audio source configured (link to the audio settings); every
  source already runs the model (said so, Done only); otherwise one switch per
  source plus an all sources switch.

  Rows: a source that already runs the model is shown on and cannot be changed
  here, including one that follows the default models (its empty list is not
  rewritten). A source that follows the defaults without running the model gets
  the defaults written out plus the model when it is turned on, as the source
  editors do. The sample rate badges are the ones the source editors show
  (ModelToggleList); the bat on a stream warning has its own wording, since the
  wizard has no stream test.

  @component
-->
<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { AlertTriangle, Info, Mic, Radio } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import { generateId } from '$lib/utils/uuid';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { SETTINGS_ROUTES, SYSTEM_ROUTES } from '$lib/utils/settingsRoutes';
  import { DEFAULT_SAMPLE_RATE } from '$lib/utils/audio/sampleRate';
  import {
    defaultModelSelection,
    effectiveSourceModels,
    sourceModelsWith,
  } from '$lib/utils/defaultModelSelection';
  import { handleAppLinkClick } from '$lib/stores/navigation.svelte';
  import { toastActions } from '$lib/stores/toast';
  import {
    settingsActions,
    settingsStore,
    type AudioSourceConfig,
    type StreamConfig,
  } from '$lib/stores/settings';
  import {
    getAvailableModels,
    modelsFailed,
    modelsLoaded,
    refreshModels,
    watchModels,
  } from '$lib/stores/models.svelte';
  import {
    acousticModelAvailability,
    subscribeAcousticModels,
  } from '$lib/stores/acousticModels.svelte';
  import ToggleField from '$lib/desktop/components/forms/ToggleField.svelte';
  import SampleRateBadge from '$lib/desktop/components/forms/SampleRateBadge.svelte';
  import Button from '$lib/desktop/components/ui/Button.svelte';
  import Skeleton from '$lib/desktop/components/ui/Skeleton.svelte';
  import SkeletonStatus from '$lib/desktop/components/ui/SkeletonStatus.svelte';
  import SettingsNote from '$lib/desktop/features/settings/components/SettingsNote.svelte';
  import { wizardState } from '../wizardState.svelte';
  import type { WizardStepProps } from '../types';
  import { sourcesOf, streamsOf } from './audioSourceChoice';

  let { onValidChange, registerLeaveHandler }: WizardStepProps = $props();

  /** Number of skeleton rows shown while the model list loads. */
  const SKELETON_ROWS = 3;
  const SOUND_CARDS_HEADING_ID = generateId('enable-sound-cards');
  const STREAMS_HEADING_ID = generateId('enable-streams');
  const ROW_ID = generateId('enable-source');
  const LIST_CLASS =
    'divide-y divide-[var(--border-100)] rounded-lg border border-[var(--border-200)] bg-[var(--color-base-100)]';
  const LINK_CLASS =
    'mt-2 inline-block text-sm underline hover:decoration-2 text-[var(--text-primary)]';

  // The flow is launched for one model and closes before another can start
  const model = wizardState.installedModel;
  const modelName = model?.name ?? '';

  type SourceKind = 'soundcard' | 'stream';

  interface SourceRow {
    /** Identity of the source, used to find it again in the settings at save time. */
    key: string;
    kind: SourceKind;
    name: string;
    sampleRate: number;
    /** True when the source already analyzes with the model. */
    runs: boolean;
    followsDefaults: boolean;
  }

  // Sound cards are told apart by name and device, streams by name and URL. The
  // URL is used only as an identity and never shown, since it may hold credentials.
  const cardKey = (source: AudioSourceConfig) => `soundcard\n${source.name}\n${source.device}`;
  const streamKey = (stream: StreamConfig) => `stream\n${stream.name}\n${stream.url}`;

  // What the server holds, not the form copy: arrays are sent back whole
  let realtime = $derived($settingsStore.originalData?.realtime);
  let sources = $derived(sourcesOf(realtime?.audio));
  let streams = $derived(streamsOf(realtime?.rtsp));

  let models = $derived(getAvailableModels());
  let listFailed = $derived(modelsFailed());
  let listLoading = $derived(!modelsLoaded() && !listFailed);
  let listed = $derived(
    model?.registryId ? models.find(m => m.registryId === model.registryId) : undefined
  );
  let defaults = $derived(defaultModelSelection(acousticModelAvailability(), models));

  function rowFor(
    key: string,
    kind: SourceKind,
    name: string,
    selected: readonly string[],
    sampleRate: number
  ): SourceRow {
    const alias = listed?.id ?? '';
    return {
      key,
      kind,
      name,
      sampleRate,
      runs: effectiveSourceModels(selected, defaults).includes(alias),
      followsDefaults: selected.length === 0,
    };
  }

  let cardRows = $derived(
    sources.map(source =>
      rowFor(
        cardKey(source),
        'soundcard',
        source.name,
        source.models ?? [],
        source.sampleRate || DEFAULT_SAMPLE_RATE
      )
    )
  );
  // A stream's rate is known only from a stream test, so the editors assume the default
  let streamRows = $derived(
    streams.map(stream =>
      rowFor(streamKey(stream), 'stream', stream.name, stream.models ?? [], DEFAULT_SAMPLE_RATE)
    )
  );
  let toggleable = $derived([...cardRows, ...streamRows].filter(row => !row.runs));

  let chosen = $state<string[]>([]);
  let allChosen = $derived(toggleable.length > 0 && toggleable.every(r => chosen.includes(r.key)));

  // A source that follows the defaults is saved as the defaults plus the model, so
  // it waits until the defaults are known; with them unknown the save would drop
  // the default models the source really runs
  let defaultsPending = $derived(
    acousticModelAvailability().kind === 'unknown' &&
      [...cardRows, ...streamRows].some(row => row.followsDefaults)
  );

  type View = 'loading' | 'listFailed' | 'notListed' | 'notLoaded' | 'noSources' | 'sources';
  let view = $derived.by((): View => {
    if (listLoading) return 'loading';
    if (listFailed) return 'listFailed';
    if (defaultsPending) return 'loading';
    if (!listed) return 'notListed';
    if (listed.state === 'failed' || listed.state === 'pending') return 'notLoaded';
    if (cardRows.length === 0 && streamRows.length === 0) return 'noSources';
    return 'sources';
  });

  let showBatStreamWarning = $derived(
    listed?.category === 'bat' && streamRows.some(row => row.runs || chosen.includes(row.key))
  );

  function setChosen(key: string, on: boolean) {
    chosen = on ? [...chosen, key] : chosen.filter(existing => existing !== key);
  }

  function setAll(on: boolean) {
    chosen = on ? toggleable.map(row => row.key) : [];
  }

  function rowDescription(row: SourceRow): string {
    if (row.runs && row.followsDefaults)
      return t('wizard.steps.enableOnSources.followsDefaultsRuns', { name: modelName });
    if (row.runs) return t('wizard.steps.enableOnSources.alreadyOn', { name: modelName });
    if (row.followsDefaults)
      return t('wizard.steps.enableOnSources.followsDefaultsAdd', { name: modelName });
    return '';
  }

  let stepRef = $state<HTMLDivElement>();

  // Retry leaves the DOM while the list reloads; park focus on the step so it
  // stays in the dialog
  function retryList() {
    stepRef?.focus();
    void refreshModels();
  }

  // An in-app navigation leaves the wizard behind, so it closes; a modified click
  // opens a new tab and keeps this page and the wizard as they are
  function followLink(event: MouseEvent) {
    handleAppLinkClick(event);
    if (event.defaultPrevented) wizardState.skip();
  }

  // Applies the model to the chosen entries of a stored list; null when none changes
  function withModel<T extends { models?: string[] }>(
    list: readonly T[],
    keyOf: (_item: T) => string,
    alias: string
  ): { list: T[]; changed: number } | null {
    let changed = 0;
    const next = list.map(item => {
      const selected = item.models ?? [];
      if (!chosen.includes(keyOf(item))) return item;
      if (effectiveSourceModels(selected, defaults).includes(alias)) return item;
      changed++;
      return { ...item, models: sourceModelsWith(selected, defaults, alias) };
    });
    return changed > 0 ? { list: next, changed } : null;
  }

  // Runs on Done. The payloads are built from the stored settings at this
  // moment (a successful save updates them), so a retry after a failed save
  // resends only what still differs. The sources a save turned on are reported
  // even when a later save fails or the wizard was closed meanwhile.
  async function commit(): Promise<void> {
    const alias = listed?.id;
    if (view !== 'sources' || alias === undefined || chosen.length === 0) return;

    let enabled = 0;
    try {
      const nextSources = withModel(sources, cardKey, alias);
      if (nextSources) {
        await settingsActions.saveSection('audio', { sources: nextSources.list });
        enabled += nextSources.changed;
      }
      // Not now during the sound card save closed the wizard: start no other save
      if (wizardState.installedModel !== model) return;
      const nextStreams = withModel(streams, streamKey, alias);
      if (nextStreams) {
        await settingsActions.saveSection('rtsp', { streams: nextStreams.list });
        enabled += nextStreams.changed;
      }
    } finally {
      if (enabled > 0) {
        toastActions.success(
          t('wizard.steps.enableOnSources.enabledToast', { name: modelName, count: enabled })
        );
      }
    }
  }

  onMount(() => {
    // Every state can be left with Done; a state without switches saves nothing
    untrack(() => onValidChange?.(true));
    const unregister = registerLeaveHandler?.(commit);
    const unwatchModels = watchModels();
    const unsubscribeAcoustic = subscribeAcousticModels();
    return () => {
      unregister?.();
      unwatchModels();
      unsubscribeAcoustic();
    };
  });
</script>

{#snippet sourceList(rows: SourceRow[], headingId: string, heading: string)}
  {#if rows.length > 0}
    <section aria-labelledby={headingId}>
      <h4 id={headingId} class="mb-2 text-sm font-semibold text-[var(--color-base-content)]">
        {heading}
      </h4>
      <ul class={LIST_CLASS}>
        {#each rows as row, index (row.key)}
          {@const badgesId = `${ROW_ID}-${row.kind}-${index}-badges`}
          <li class="px-3 py-2.5">
            <ToggleField
              label={row.name}
              icon={row.kind === 'soundcard' ? Mic : Radio}
              value={row.runs || chosen.includes(row.key)}
              disabled={row.runs}
              description={rowDescription(row) || undefined}
              describedBy={badgesId}
              onUpdate={on => setChosen(row.key, on)}
            >
              {#snippet badges()}
                <!-- The badge is not part of the switch's name, so the switch is described by it -->
                <span id={badgesId}>
                  <SampleRateBadge
                    sampleRate={row.sampleRate}
                    minRate={listed?.minSampleRate}
                    recommendedRate={listed?.recommendedSampleRate}
                  />
                </span>
              {/snippet}
            </ToggleField>
          </li>
        {/each}
      </ul>
    </section>
  {/if}
{/snippet}

<div bind:this={stepRef} tabindex="-1" class="space-y-5 focus:outline-none">
  {#if view === 'loading'}
    <div class="space-y-3">
      <Skeleton className="h-4 w-3/4" />
      <div class={cn(LIST_CLASS, 'space-y-0')}>
        {#each { length: SKELETON_ROWS } as _, row (row)}
          <div class="flex items-center justify-between gap-4 px-3 py-3">
            <Skeleton className="h-4 w-40" />
            <Skeleton className="h-5 w-10 rounded-full" />
          </div>
        {/each}
      </div>
      <SkeletonStatus label={t('settings.audio.models.loading')} class="text-sm" />
    </div>
  {:else if view === 'listFailed'}
    <SettingsNote className="mt-0">
      <p class="text-[var(--text-error)]">{t('settings.audio.models.listFailed')}</p>
      <div class="mt-3">
        <Button variant="default" size="md" onclick={retryList}>
          {t('common.retry')}
        </Button>
      </div>
    </SettingsNote>
  {:else if view === 'notListed'}
    <SettingsNote className="mt-0">
      <p>{t('wizard.steps.enableOnSources.notListed', { name: modelName })}</p>
    </SettingsNote>
  {:else if view === 'notLoaded'}
    <SettingsNote className="mt-0">
      {#if listed?.state === 'failed'}
        <p>{t('wizard.steps.enableOnSources.notLoadedFailed', { name: modelName })}</p>
        <a href={buildAppUrl(SYSTEM_ROUTES.inference)} onclick={followLink} class={LINK_CLASS}>
          {t('settings.audio.models.loadFailedLink')}
        </a>
      {:else}
        <p>{t('wizard.steps.enableOnSources.notLoadedPending', { name: modelName })}</p>
      {/if}
    </SettingsNote>
  {:else if view === 'noSources'}
    <SettingsNote className="mt-0">
      <p>{t('wizard.steps.enableOnSources.noSources', { name: modelName })}</p>
      <a href={buildAppUrl(SETTINGS_ROUTES.audio)} onclick={followLink} class={LINK_CLASS}>
        {t('wizard.steps.enableOnSources.openAudioSettings')}
      </a>
    </SettingsNote>
  {:else}
    <div class="space-y-1">
      <p class="text-sm text-[var(--color-base-content)]">
        {t('wizard.steps.enableOnSources.intro', { name: modelName })}
      </p>
      {#if toggleable.length > 0}
        <p class="text-sm text-[var(--color-base-content)]/80">
          {t('wizard.steps.enableOnSources.saveHint')}
        </p>
      {/if}
    </div>

    {#if toggleable.length === 0}
      <SettingsNote className="mt-0">
        {#snippet icon()}
          <Info class="mt-0.5 size-4 text-[var(--text-info)]" aria-hidden="true" />
        {/snippet}
        <p>{t('wizard.steps.enableOnSources.allRunning', { name: modelName })}</p>
      </SettingsNote>
    {:else if toggleable.length > 1}
      <div class={cn(LIST_CLASS, 'px-3 py-2.5')}>
        <ToggleField
          label={t('wizard.steps.enableOnSources.allSources')}
          value={allChosen}
          onUpdate={setAll}
        />
      </div>
    {/if}

    {@render sourceList(
      cardRows,
      SOUND_CARDS_HEADING_ID,
      t('wizard.steps.enableOnSources.soundCardsHeading')
    )}
    {@render sourceList(
      streamRows,
      STREAMS_HEADING_ID,
      t('wizard.steps.enableOnSources.streamsHeading')
    )}

    {#if showBatStreamWarning}
      <div
        class="flex items-start gap-2 rounded-lg border border-[color-mix(in_srgb,var(--color-warning)_30%,transparent)] bg-[color-mix(in_srgb,var(--color-warning)_10%,transparent)] p-2.5 text-xs leading-relaxed text-[var(--color-base-content)]"
      >
        <AlertTriangle
          class="mt-0.5 size-3.5 shrink-0 text-[var(--text-warning)]"
          aria-hidden="true"
        />
        <span>{t('wizard.steps.enableOnSources.batStreamWarning')}</span>
      </div>
    {/if}
  {/if}
</div>
