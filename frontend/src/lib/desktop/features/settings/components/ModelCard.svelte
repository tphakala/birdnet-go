<!--
  ModelCard.svelte

  One model gallery card, for the Installed and the Available tab. An <article> whose five
  regions (header, description, notices, details, footer) sit on the rows of ModelCardGrid
  through subgrid, so the regions line up across a row of cards whatever each card holds.

  Props:
  - entry: The catalog entry shown
  - mode: 'installed' describes the installed variant and offers Optimize, Reinstall and
    Remove; 'available' describes the recommended variant and offers Install
  - regionNames: Region slug to localized region name
  - busy: The action running on this card, or null. On an installed card 'install' is the
    variant swap (Optimize)
  - actionInFlight: True while any gallery action runs on any card
  - pausedReasonId: id of the visible status line saying why actions are paused
  - progress: Download progress of this card's running install, reinstall or swap
  - optimizeAvailable: Installed mode: a better variant exists for this host
  - onAction: Called with the action and the entry when an action button is used
  - className: Classes for the article

  Behaviour:
  - No action button is natively disabled, so Tab always reaches it. While its own action runs,
    while another action runs, and for Install on an incompatible entry it is aria-disabled,
    ignores clicks and points aria-describedby at the visible reason (pausedReasonId, or the
    incompatibility notice).
  - A button's accessible name is "{label} {name}", with the running label while its action
    runs, so it always contains the visible text.
  - Must be a direct child of ModelCardGrid's grid: the card spans five of its rows.
-->
<script lang="ts" module>
  export type ModelCardMode = 'installed' | 'available';
  export type ModelCardAction = 'install' | 'reinstall' | 'remove';
</script>

<script lang="ts">
  import type { LucideIcon } from '@lucide/svelte';
  import {
    BrainCircuit,
    Check,
    Download,
    ExternalLink,
    Globe,
    Loader2,
    Radar,
    RefreshCw,
    Shield,
    ShieldAlert,
    Sparkles,
    Trash2,
    TriangleAlert,
    XCircle,
  } from '@lucide/svelte';
  import Button from '$lib/desktop/components/ui/Button.svelte';
  import ExpandableText from '$lib/desktop/components/ui/ExpandableText.svelte';
  import ProgressBar from '$lib/desktop/components/ui/ProgressBar.svelte';
  import StatusPill from '$lib/desktop/components/ui/StatusPill.svelte';
  import type { CatalogEntry, CatalogVariant, DownloadProgress } from '$lib/types/models';
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import { formatBytes, formatNumber } from '$lib/utils/formatters';
  import { generateId } from '$lib/utils/uuid';
  import {
    CHANNEL_PREVIEW,
    translateReason,
    variantHardwareLabel,
  } from '$lib/utils/variantSelection';

  import logoBirdnet from '$lib/assets/logos/logo-birdnet.png';
  import logoGoogle from '$lib/assets/logos/logo-google.png';
  import logoJyu from '$lib/assets/logos/logo-jyu.jpeg';

  interface Props {
    entry: CatalogEntry;
    mode: ModelCardMode;
    /** Region slug to localized name, as ModelVariantPicker takes it. */
    regionNames: ReadonlyMap<string, string>;
    /** The action running on this card, or null. On an installed card 'install' is the variant swap (Optimize). */
    busy?: ModelCardAction | null;
    /** True while any gallery action runs on any card. */
    actionInFlight?: boolean;
    /** id of the visible status line that explains why actions are paused. */
    pausedReasonId?: string;
    /** Download progress of this card's running install, reinstall or swap. */
    progress?: DownloadProgress | null;
    /** Installed mode: a better variant exists for this host. */
    optimizeAvailable?: boolean;
    // eslint-disable-next-line no-unused-vars -- the param names document the callback contract
    onAction: (action: ModelCardAction, entry: CatalogEntry) => void;
    className?: string;
  }

  let {
    entry,
    mode,
    regionNames,
    busy = null,
    actionInFlight = false,
    pausedReasonId,
    progress = null,
    optimizeAvailable = false,
    onAction,
    className,
  }: Props = $props();

  const MODEL_LOGOS: Record<string, string> = {
    birdnet: logoBirdnet,
    perch: logoGoogle,
    bsg: logoJyu,
  };

  /** Schemes an upstream link may use; anything else is shown as plain text. */
  const LINK_PROTOCOLS = new Set(['http:', 'https:']);

  /** Lines of description shown before Show more. */
  const DESCRIPTION_LINES = 3;

  const PERCENT = 100;

  const titleId = generateId('model-card-title');
  const incompatibleNoticeId = generateId('model-card-incompatible');

  function getModelLogo(id: string): string | null {
    for (const [prefix, logo] of Object.entries(MODEL_LOGOS)) {
      if (id.startsWith(prefix)) return logo;
    }
    return null;
  }

  /** The URL when it is an absolute http or https URL, else null. */
  function safeLinkUrl(url: string | undefined): string | null {
    if (!url) return null;
    try {
      return LINK_PROTOCOLS.has(new URL(url).protocol) ? url : null;
    } catch {
      return null;
    }
  }

  // Render an entry-level incompatibility code (e.g. "backend.onnx_unavailable") through the
  // same i18n reason path the variant picker uses, falling back to a generic localized line
  // when the code is absent or has no translation, so a structured code never surfaces as a
  // raw dotted string. The fallback is cause-neutral on purpose: it is only reached when the
  // code is missing or unmapped, which is exactly when the specific cause is not known.
  // Entry-level codes carry no interpolation args (unlike variant reasons).
  function entryIncompatibleText(code: string | undefined): string {
    const fallback = t('analysis.gallery.entryIncompatible');
    return code ? translateReason(code, undefined, fallback) : fallback;
  }

  // The variant a card describes: the installed variant on the Installed tab, the
  // host-recommended (else default) variant on the Available tab. Null for a flat
  // (variant-less) entry.
  function displayVariantFor(item: CatalogEntry, tab: ModelCardMode): CatalogVariant | null {
    const variants = item.variants ?? [];
    if (variants.length === 0) return null;
    if (tab === 'installed') {
      return variants.find(v => v.id === item.installedVariantId) ?? null;
    }
    return (
      variants.find(v => v.id === item.recommendedVariantId) ??
      variants.find(v => v.default) ??
      null
    );
  }

  // The variant's localized region name, or "Global" when the variant is global.
  function displayRegionName(variant: CatalogVariant | null): string {
    if (!variant?.region) return t('analysis.gallery.regionGlobal');
    return regionNames.get(variant.region) ?? variant.region;
  }

  function progressPercent(p: DownloadProgress): number {
    if (p.totalBytes <= 0) return 0;
    return Math.min(PERCENT, Math.round((p.downloadedBytes / p.totalBytes) * PERCENT));
  }

  function statusLabel(status: DownloadProgress['status']): string {
    switch (status) {
      case 'downloading':
        return t('analysis.gallery.progress.downloading');
      case 'verifying':
        return t('analysis.gallery.progress.verifying');
      case 'loading':
        return t('analysis.gallery.progress.loading');
      case 'complete':
        return t('analysis.gallery.progress.complete');
      case 'failed':
        return t('analysis.gallery.progress.failed');
      default:
        return '';
    }
  }

  const logo = $derived(getModelLogo(entry.id));
  const upstreamUrl = $derived(safeLinkUrl(entry.upstreamUrl));
  const isPreview = $derived(entry.channel === CHANNEL_PREVIEW);
  const hasVariants = $derived((entry.variants ?? []).length > 0);
  const shownVariant = $derived(displayVariantFor(entry, mode));
  const speciesCount = $derived(shownVariant?.speciesCount ?? entry.speciesCount);
  // A caller that passes only `busy` still pauses the card's other buttons
  const inFlight = $derived(actionInFlight || busy !== null);
  // Available cards dim the logo of an entry this host cannot run; the text keeps full contrast
  const unavailable = $derived(mode === 'available' && !entry.compatible);
  const showPreviewNotice = $derived(mode === 'available' && isPreview);
  const hasNotices = $derived(showPreviewNotice || !entry.compatible || progress !== null);
  const showGeomodel = $derived(entry.hasGeomodel && entry.category !== 'geomodel');
  const showOptimize = $derived(mode === 'installed' && optimizeAvailable);

  interface ActionSpec {
    action: ModelCardAction;
    variant: 'primary' | 'default' | 'error';
    icon: LucideIcon;
    label: string;
    runningLabel: string;
    /** Another action runs: blocked, described by the paused reason. */
    paused: boolean;
    /** This action runs: blocked, described by the paused reason. */
    running: boolean;
    /** The action can never run here: blocked, described by the notice at describedBy. */
    unavailable?: boolean;
    /** id of the visible text that explains an unavailable action. */
    describedBy?: string;
  }

  const installAction = $derived<ActionSpec>({
    action: 'install',
    variant: 'primary',
    icon: Download,
    label: t('analysis.gallery.install'),
    runningLabel: t('analysis.gallery.installing'),
    // Incompatible entries stay blocked for good, explained by the notice, never by the paused reason
    paused: inFlight && busy !== 'install' && entry.compatible,
    running: busy === 'install',
    unavailable: !entry.compatible,
    describedBy: entry.compatible ? undefined : incompatibleNoticeId,
  });

  const optimizeAction = $derived<ActionSpec>({
    action: 'install',
    variant: 'primary',
    icon: Sparkles,
    label: t('analysis.gallery.optimize.swap'),
    runningLabel: t('analysis.gallery.optimize.applying'),
    paused: inFlight && busy !== 'install',
    running: busy === 'install',
  });

  const reinstallAction = $derived<ActionSpec>({
    action: 'reinstall',
    variant: 'default',
    icon: RefreshCw,
    label: t('analysis.gallery.reinstall'),
    runningLabel: t('analysis.gallery.reinstalling'),
    paused: inFlight && busy !== 'reinstall',
    running: busy === 'reinstall',
  });

  const removeAction = $derived<ActionSpec>({
    action: 'remove',
    variant: 'error',
    icon: Trash2,
    label: t('analysis.gallery.remove'),
    runningLabel: t('analysis.gallery.removing'),
    paused: inFlight && busy !== 'remove',
    running: busy === 'remove',
  });
</script>

{#snippet actionButton(spec: ActionSpec)}
  {@const blocked = spec.paused || spec.running || spec.unavailable === true}
  <Button
    variant={spec.variant}
    size="sm"
    aria-disabled={blocked ? 'true' : undefined}
    aria-describedby={spec.unavailable ? spec.describedBy : blocked ? pausedReasonId : undefined}
    title={spec.paused ? t('analysis.gallery.actionInProgress') : undefined}
    aria-label="{spec.running ? spec.runningLabel : spec.label} {entry.name}"
    onclick={() => {
      if (blocked) return;
      onAction(spec.action, entry);
    }}
  >
    {#if spec.running}
      <Loader2 class="size-3.5 animate-spin motion-reduce:animate-none" aria-hidden="true" />
      {spec.runningLabel}
    {:else}
      <spec.icon class="size-3.5" aria-hidden="true" />
      {spec.label}
    {/if}
  </Button>
{/snippet}

{#snippet notice(
  tone: 'warning' | 'error',
  Icon: LucideIcon,
  text: string,
  role: 'note' | 'status',
  id: string | undefined
)}
  <div {id} class="model-card-notice" data-tone={tone} {role}>
    <Icon class="model-card-notice-icon size-4 shrink-0" aria-hidden="true" />
    <span>{text}</span>
  </div>
{/snippet}

<article class={cn('model-card', className)} aria-labelledby={titleId}>
  <header class="model-card-header">
    {#if logo}
      <img
        src={logo}
        alt=""
        class={cn('size-10 shrink-0 rounded-lg', unavailable && 'opacity-60')}
      />
    {:else}
      <div
        class={cn(
          'shrink-0 rounded-lg bg-[var(--color-primary)]/10 p-2 text-[var(--color-primary)]',
          unavailable && 'opacity-60'
        )}
        aria-hidden="true"
      >
        {#if entry.category === 'geomodel'}
          <Globe class="size-6" />
        {:else if entry.category === 'bat'}
          <Radar class="size-6" />
        {:else}
          <BrainCircuit class="size-6" />
        {/if}
      </div>
    {/if}
    <div class="min-w-0 flex-1">
      <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
        <h4 id={titleId} class="text-sm font-semibold text-[var(--color-base-content)]">
          {entry.name}
        </h4>
        {#if isPreview}
          <StatusPill
            variant="warning"
            size="sm"
            showDot={false}
            label={t('analysis.gallery.preview.badge')}
            className="whitespace-nowrap"
          />
        {/if}
      </div>
      {#if entry.buildLabel}
        <p class="mt-0.5 font-mono text-xs text-[var(--color-base-content)]/80">
          {t('analysis.gallery.preview.buildLabel', {
            version: entry.version,
            build: entry.buildLabel,
          })}
        </p>
      {/if}
      {#if upstreamUrl}
        <a
          href={upstreamUrl}
          target="_blank"
          rel="noopener noreferrer"
          class="model-card-author inline-block min-h-6 rounded-sm py-0.5 text-xs text-[var(--color-base-content)]/80 underline underline-offset-2 hover:text-[var(--color-base-content)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-primary)]"
        >
          {entry.author}
          <ExternalLink class="ml-0.5 inline size-3 align-text-bottom" aria-hidden="true" />
          <span class="sr-only">({t('common.aria.opensInNewTab')})</span>
        </a>
      {:else}
        <p class="model-card-author text-xs text-[var(--color-base-content)]/80">
          {entry.author}
        </p>
      {/if}
    </div>
  </header>

  <div class="model-card-description">
    <ExpandableText
      text={entry.description}
      subject={entry.name}
      lines={DESCRIPTION_LINES}
      textClassName="text-xs leading-relaxed text-[var(--color-base-content)]/80"
    />
  </div>

  {#if hasNotices}
    <div class="model-card-notices">
      {#if showPreviewNotice}
        {@render notice(
          'warning',
          TriangleAlert,
          t('analysis.gallery.preview.cardNotice'),
          'note',
          undefined
        )}
      {/if}
      {#if !entry.compatible}
        {#if mode === 'installed'}
          {@render notice(
            'error',
            XCircle,
            entryIncompatibleText(entry.incompatibleReason),
            'status',
            undefined
          )}
        {:else}
          {@render notice(
            'warning',
            TriangleAlert,
            entryIncompatibleText(entry.incompatibleReason),
            'status',
            incompatibleNoticeId
          )}
        {/if}
      {/if}
      {#if progress}
        {#if progress.status === 'complete'}
          <p class="flex items-center gap-2 text-sm font-medium text-[var(--text-success)]">
            <Check class="size-4 shrink-0" aria-hidden="true" />
            {busy === 'reinstall'
              ? t('analysis.gallery.reinstallComplete')
              : t('analysis.gallery.progress.complete')}
          </p>
        {:else}
          <div class="space-y-1.5">
            <ProgressBar
              size="sm"
              value={progressPercent(progress)}
              aria-label="{statusLabel(progress.status)} {entry.name}"
            />
            <div
              class="flex flex-wrap items-center justify-between gap-x-2 text-xs text-[var(--color-base-content)]/80"
            >
              <span>
                {statusLabel(progress.status)}
                {#if progress.status === 'downloading' && progress.totalFiles > 1}
                  ({progress.currentFile}/{progress.totalFiles})
                {/if}
              </span>
              {#if progress.status === 'downloading' && progress.totalBytes > 0}
                <span class="tabular-nums">
                  {formatBytes(progress.downloadedBytes)} / {formatBytes(progress.totalBytes)}
                </span>
              {/if}
            </div>
          </div>
        {/if}
      {/if}
    </div>
  {/if}

  <div class="model-card-details">
    <dl class="model-card-facts">
      {#if hasVariants}
        <dt>{t('analysis.gallery.regionLabel')}</dt>
        <dd>{displayRegionName(shownVariant)}</dd>
      {:else if entry.region}
        <dt>{t('analysis.gallery.regionLabel')}</dt>
        <dd>{entry.region}</dd>
      {/if}
      <dt>{t('analysis.gallery.speciesLabel')}</dt>
      <dd class="tabular-nums">
        {t('analysis.gallery.species', { count: formatNumber(speciesCount) })}
      </dd>
      {#if hasVariants}
        <dt>{t('analysis.gallery.hardwareLabel')}</dt>
        <dd>
          {#if shownVariant}
            <StatusPill
              variant="neutral"
              size="sm"
              showDot={false}
              label={variantHardwareLabel(shownVariant)}
              className="whitespace-nowrap"
            />
          {:else}
            -
          {/if}
        </dd>
      {/if}
      <dt>{t('analysis.gallery.license.license')}</dt>
      <dd class="flex flex-wrap items-center gap-x-2 gap-y-1">
        {#if entry.commercialUse}
          <StatusPill
            variant="success"
            size="sm"
            label={entry.license}
            className="whitespace-nowrap"
          >
            {#snippet leadingIcon()}<Shield class="size-3" />{/snippet}
          </StatusPill>
          <span>{t('analysis.gallery.license.commercialUseAllowed')}</span>
        {:else}
          <StatusPill
            variant="neutral"
            size="sm"
            label={entry.license}
            className="whitespace-nowrap"
          >
            {#snippet leadingIcon()}<ShieldAlert class="size-3" />{/snippet}
          </StatusPill>
          <span>{t('analysis.gallery.license.nonCommercialOnly')}</span>
        {/if}
      </dd>
    </dl>
    {#if showGeomodel}
      <div class="mt-2.5">
        <StatusPill variant="info" size="sm" label={t('analysis.gallery.geomodelBadge')}>
          {#snippet leadingIcon()}<Globe class="size-3" />{/snippet}
        </StatusPill>
      </div>
    {/if}
    {#if showOptimize}
      <p class="mt-2.5 flex items-start gap-1.5 text-xs text-[var(--color-base-content)]">
        <Sparkles class="mt-px size-3.5 shrink-0 text-[var(--text-info)]" aria-hidden="true" />
        {t('analysis.gallery.optimize.badgeTitle')}
      </p>
    {/if}
  </div>

  <footer class="model-card-footer">
    {#if mode === 'available'}
      {@render actionButton(installAction)}
    {:else}
      {#if showOptimize}
        {@render actionButton(optimizeAction)}
      {/if}
      {#if entry.permanent}
        <StatusPill
          variant="neutral"
          size="sm"
          showDot={false}
          label={t('analysis.gallery.builtIn')}
          className="whitespace-nowrap"
        />
      {:else}
        {@render actionButton(reinstallAction)}
        {@render actionButton(removeAction)}
      {/if}
    {/if}
  </footer>
</article>

<style>
  /* The card spans five rows of ModelCardGrid and shares them through subgrid, so each region
     starts at the same height as in the neighbouring cards. The repeat() line is the fallback
     for a browser without subgrid. No container-type, contain or overflow clipping here: a size
     container turns subgrid off on the same element. Row gaps are zero; regions space
     themselves with margins. */
  .model-card {
    display: grid;
    grid-row: span 5;
    grid-template-rows: repeat(5, auto);
    grid-template-rows: subgrid;
    min-width: 0;
    margin-block-end: 1rem;
    padding: 1rem;
    border: 1px solid var(--color-base-300);
    border-radius: 0.5rem;
    background-color: var(--color-base-200);
  }

  /* Every region has an explicit row, so a card without notices still lines up */
  .model-card-header {
    display: flex;
    grid-row: 1;
    gap: 0.75rem;
    align-items: flex-start;
    min-width: 0;
  }

  .model-card-description {
    grid-row: 2;
    min-width: 0;
    margin-block-start: 0.75rem;
  }

  .model-card-notices {
    display: flex;
    flex-direction: column;
    grid-row: 3;
    gap: 0.5rem;
    min-width: 0;
    margin-block-start: 0.75rem;
  }

  .model-card-details {
    grid-row: 4;
    min-width: 0;
    margin-block-start: 0.75rem;
    padding-block-start: 0.75rem;
    border-block-start: 1px solid var(--color-base-300);
  }

  .model-card-footer {
    display: flex;
    flex-wrap: wrap;
    grid-row: 5;
    gap: 0.5rem;
    align-items: center;
    justify-content: flex-end;
    margin-block-start: 1rem;
  }

  .model-card-author {
    margin-block-start: 0.125rem;
    overflow-wrap: anywhere;
  }

  .model-card-facts {
    display: grid;
    grid-template-columns: max-content minmax(0, 1fr);
    gap: 0.375rem 1rem;
    align-items: baseline;
    font-size: var(--text-xs);
    line-height: var(--text-xs--line-height);
  }

  .model-card-facts dt {
    color: color-mix(in srgb, var(--color-base-content) 80%, transparent);
  }

  .model-card-facts dd {
    min-width: 0;
    color: var(--color-base-content);
  }

  /* A tinted note with base-content text; the icon carries the tone */
  .model-card-notice {
    display: flex;
    gap: 0.5rem;
    align-items: flex-start;
    padding: 0.625rem 0.75rem;
    border-radius: 0.5rem;
    font-size: var(--text-xs);
    line-height: var(--text-xs--line-height);
    color: var(--color-base-content);
  }

  .model-card-notice[data-tone='warning'] {
    background-color: color-mix(in srgb, var(--color-warning) 10%, transparent);
  }

  .model-card-notice[data-tone='error'] {
    background-color: color-mix(in srgb, var(--color-error) 10%, transparent);
  }

  .model-card-notice[data-tone='warning'] :global(.model-card-notice-icon) {
    color: var(--text-warning);
  }

  .model-card-notice[data-tone='error'] :global(.model-card-notice-icon) {
    color: var(--text-error);
  }
</style>
