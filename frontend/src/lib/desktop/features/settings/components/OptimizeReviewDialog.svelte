<script lang="ts">
  // Review dialog for the model gallery "optimize" flow. Lists the within-model
  // offers (a faster or better-matched build of a model the user already has) with
  // a from -> to variant summary, hardware chips, and the recommendation reasons,
  // and lets the user apply one or all of them. Apply is entirely within-model, so
  // the license never changes (see the license note); the swap reuses the same
  // install/variant-swap flow as a normal install, so no new consent is needed.

  import type { OptimizeOffer } from '$lib/utils/variantSelection';
  import { variantLabel, variantHardwareLabel } from '$lib/utils/variantSelection';
  import { t } from '$lib/i18n';
  import { ArrowRight, Check, Loader2, Sparkles, TriangleAlert, X } from '@lucide/svelte';

  interface Props {
    /** Whether the dialog is open. The parent controls it; native closes call onClose. */
    open: boolean;
    offers: OptimizeOffer[];
    regionNames: ReadonlyMap<string, string>;
    /** True while any gallery action is in flight (blocks concurrent applies). */
    inFlight: boolean;
    /** Entry id currently being applied, or null. */
    applyingId: string | null;
    /** Entry ids applied in this session (marked done in the list). */
    appliedIds: Set<string>;
    /** Entry ids whose apply failed this session (marked failed, re-appliable). */
    failedIds: Set<string>;
    onApply: (_offer: OptimizeOffer) => void;
    onApplyAll: () => void;
    onClose: () => void;
  }

  let {
    open,
    offers,
    regionNames,
    inFlight,
    applyingId,
    appliedIds,
    failedIds,
    onApply,
    onApplyAll,
    onClose,
  }: Props = $props();

  // Element binding is a plain let (not $state): native <dialog> is driven
  // imperatively via showModal()/close(), never re-rendered from this reference.
  let dialogEl: HTMLDialogElement | null = null;

  // Id of the live status line that explains why Apply is blocked while another
  // gallery action runs. Apply/Apply-all reference it via aria-describedby and stay
  // tab-focusable (aria-disabled, not native disabled) so the reason is reachable by
  // keyboard and screen readers, and visible on touch devices where a title tooltip
  // never appears (see frontend/CLAUDE.md "No Ambiguous Disabled States").
  const IN_FLIGHT_STATUS_ID = 'optimize-inflight-status';

  // Reflect the `open` prop onto the native dialog. showModal()/close() are
  // idempotent, so re-running on unrelated prop changes is harmless.
  $effect(() => {
    if (open) dialogEl?.showModal();
    else dialogEl?.close();
  });

  // Offers still worth applying: an offer whose entry was already applied this
  // session drops out on the next catalog reload, but guard here too so a mid-batch
  // re-render never shows an applied row as still actionable.
  const pending = $derived(offers.filter(o => !appliedIds.has(o.entry.id)));
</script>

<dialog
  bind:this={dialogEl}
  onclose={() => {
    if (open) onClose();
  }}
  class="m-auto w-full max-w-lg rounded-xl border border-[var(--color-base-300)] bg-[var(--color-base-100)] p-0 shadow-xl backdrop:bg-black/50"
  aria-labelledby="optimize-dialog-title"
>
  <div class="p-6">
    <div class="flex items-start justify-between gap-3">
      <h3
        id="optimize-dialog-title"
        class="flex items-center gap-2 text-lg font-semibold text-[var(--color-base-content)]"
      >
        <Sparkles class="size-5 text-[var(--color-primary)]" aria-hidden="true" />
        {t('analysis.gallery.optimize.dialogTitle')}
      </h3>
      <button
        type="button"
        onclick={onClose}
        aria-label={t('common.close')}
        class="inline-flex items-center justify-center rounded-md p-1.5 bg-transparent hover:bg-black/5 dark:hover:bg-white/5 transition-colors"
      >
        <X class="size-4" />
      </button>
    </div>

    <p class="mt-2 text-sm text-[var(--color-base-content)]/70">
      {t('analysis.gallery.optimize.licenseNote')}
    </p>

    <!-- Live reason line: why Apply is blocked while another gallery action runs.
         Referenced by the Apply/Apply-all buttons' aria-describedby, so the reason
         reaches keyboard, screen-reader and touch users (no hover tooltip needed). -->
    {#if inFlight}
      <p
        id={IN_FLIGHT_STATUS_ID}
        role="status"
        class="mt-2 text-xs text-[var(--color-base-content)]/70"
      >
        {t('analysis.gallery.actionInProgress')}
      </p>
    {/if}

    {#if offers.length === 0}
      <p class="mt-4 text-sm text-[var(--color-base-content)]/80">
        {t('analysis.gallery.optimize.upToDate')}
      </p>
    {:else}
      <ul class="mt-4 space-y-3">
        {#each offers as offer (offer.entry.id)}
          {@const applying = applyingId === offer.entry.id}
          {@const applied = appliedIds.has(offer.entry.id)}
          {@const failed = failedIds.has(offer.entry.id)}
          {@const fromLabel = offer.from
            ? variantLabel(offer.from, regionNames)
            : t('analysis.gallery.optimize.installedBuild')}
          {@const toLabel = variantLabel(offer.to, regionNames)}
          <li
            class="rounded-lg border border-[var(--color-base-300)] bg-[var(--color-base-200)] p-3"
          >
            <div class="flex items-center justify-between gap-3">
              <div class="min-w-0 flex-1">
                <p class="truncate text-sm font-medium text-[var(--color-base-content)]">
                  {offer.entry.name}
                </p>
                <span class="sr-only">
                  {t('analysis.gallery.optimize.fromTo', {
                    from: fromLabel,
                    to: toLabel,
                  })}
                </span>
                <div
                  class="mt-1 flex flex-wrap items-center gap-1.5 text-xs text-[var(--color-base-content)]/80"
                  aria-hidden="true"
                >
                  <span>{fromLabel}</span>
                  {#if offer.from}
                    <span
                      class="inline-flex items-center gap-0.5 rounded-full bg-[var(--color-base-300)] px-1.5 py-0.5"
                    >
                      {variantHardwareLabel(offer.from)}
                    </span>
                  {/if}
                  <ArrowRight class="size-3.5 shrink-0" aria-hidden="true" />
                  <span class="font-medium text-[var(--color-base-content)]">{toLabel}</span>
                  <span
                    class="inline-flex items-center gap-0.5 rounded-full bg-[var(--color-primary)]/15 px-1.5 py-0.5 text-[var(--color-primary)]"
                  >
                    {variantHardwareLabel(offer.to)}
                  </span>
                </div>
                {#if offer.reasons.length > 0}
                  <p class="mt-1 text-xs text-[var(--color-base-content)]/70">
                    {offer.reasons.join(' · ')}
                  </p>
                {/if}
              </div>
              <div class="shrink-0">
                {#if applied}
                  <span
                    class="inline-flex items-center gap-1 text-xs font-medium text-[var(--color-success)]"
                  >
                    <Check class="size-3.5" />
                    {t('analysis.gallery.optimize.applied')}
                  </span>
                {:else if applying}
                  <span
                    class="inline-flex items-center gap-1 text-xs font-medium text-[var(--color-base-content)]/80"
                  >
                    <Loader2 class="size-3.5 animate-spin" />
                    {t('analysis.gallery.optimize.applying')}
                  </span>
                {:else}
                  <div class="flex flex-col items-end gap-1">
                    {#if failed}
                      <span
                        class="inline-flex items-center gap-1 text-xs font-medium text-[var(--color-error)]"
                      >
                        <TriangleAlert class="size-3.5" />
                        {t('analysis.gallery.optimize.applyFailed')}
                      </span>
                    {/if}
                    <button
                      type="button"
                      onclick={e => {
                        if (inFlight) {
                          e.preventDefault();
                          return;
                        }
                        onApply(offer);
                      }}
                      aria-disabled={inFlight ? 'true' : undefined}
                      aria-describedby={inFlight ? IN_FLIGHT_STATUS_ID : undefined}
                      title={inFlight ? t('analysis.gallery.actionInProgress') : undefined}
                      class="inline-flex items-center gap-1.5 rounded-md bg-[var(--color-primary)] px-3 py-1.5 text-xs font-medium text-[var(--color-primary-content)] transition-colors hover:bg-[var(--color-primary)]/80 aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
                    >
                      {failed ? t('analysis.gallery.retry') : t('analysis.gallery.optimize.apply')}
                    </button>
                  </div>
                {/if}
              </div>
            </div>
          </li>
        {/each}
      </ul>

      <!-- Plain-language help for the precision jargon (FP32/FP16/INT8) shown in the
           from -> to summary, mirroring the license dialog's disclosure. A native
           <details> is keyboard- and touch-accessible, unlike a hover tooltip. -->
      <details class="mt-3 text-xs text-[var(--color-base-content)]/70">
        <summary class="cursor-pointer hover:text-[var(--color-base-content)]">
          {t('analysis.gallery.variants.precisionInfo')}
        </summary>
        <p class="mt-1">{t('analysis.gallery.variants.precisionHelp')}</p>
      </details>
    {/if}

    <div class="mt-6 flex justify-end gap-3">
      <button
        type="button"
        onclick={onClose}
        class="rounded-lg border border-[var(--color-base-300)] px-4 py-2 text-sm font-medium text-[var(--color-base-content)] hover:bg-[var(--color-base-200)] transition-colors"
      >
        {t('common.close')}
      </button>
      {#if pending.length > 1}
        <button
          type="button"
          onclick={e => {
            if (inFlight) {
              e.preventDefault();
              return;
            }
            onApplyAll();
          }}
          aria-disabled={inFlight ? 'true' : undefined}
          aria-describedby={inFlight ? IN_FLIGHT_STATUS_ID : undefined}
          title={inFlight ? t('analysis.gallery.actionInProgress') : undefined}
          class="inline-flex items-center gap-2 rounded-lg bg-[var(--color-primary)] px-4 py-2 text-sm font-medium text-[var(--color-primary-content)] hover:bg-[var(--color-primary)]/80 transition-colors aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
        >
          <Sparkles class="size-4" />
          {t('analysis.gallery.optimize.applyAll')}
        </button>
      {/if}
    </div>
  </div>
</dialog>
