<script lang="ts">
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
  import LoadingSpinner from '$lib/desktop/components/ui/LoadingSpinner.svelte';
  import Button from '$lib/desktop/components/ui/Button.svelte';
  import WizardProgressBar from './WizardProgressBar.svelte';
  import WizardContentRenderer from './WizardContentRenderer.svelte';
  import { wizardState } from './wizardState.svelte';
  import { t } from '$lib/i18n';
  import { ChevronLeft, ChevronRight, Check, RefreshCw, RotateCw } from '@lucide/svelte';
  import { tick, untrack } from 'svelte';
  import type { Component } from 'svelte';
  import type { WizardStepProps } from './types';
  import { generateId } from '$lib/utils/uuid';
  import { loggers } from '$lib/utils/logger';

  const logger = loggers.ui;

  const TITLE_ID = generateId('wizard-title');
  const NEXT_REASON_ID = generateId('wizard-next-reason');
  const ALERT_ID = generateId('wizard-alert');
  const SAVING_STATUS_ID = generateId('wizard-saving-status');
  const LEAVE_TITLE_ID = generateId('wizard-leave-title');
  const LEAVE_DESC_ID = generateId('wizard-leave-desc');

  // Dialog widths (Modal sizes): most steps are one column; a step with a map or a
  // long list sets size 'wide' in the registry
  const DEFAULT_STEP_SIZE = '2xl' as const;
  const WIDE_STEP_SIZE = '4xl' as const;

  // The step box takes its content height. The floor keeps the loading and failed
  // states, and short steps, from collapsing the dialog to a strip and from moving
  // Next far between steps. @container lets a step lay out by the dialog's width,
  // not the viewport's.
  const STEP_BODY_CLASS = '@container flex min-h-[20rem] flex-col focus:outline-none';

  let contentRef = $state<HTMLDivElement>();
  let loadedComponent = $state<Component<WizardStepProps> | null>(null);
  // Index of the step the rendered component was loaded for
  let loadedIndex = $state(-1);
  // True only while the step's chunk is being imported. stepStatus stays 'loading'
  // a little longer: through the tick() after the component mounts, so the step
  // can report its validity before it is marked ready. The spinner must give way
  // to the component during that tick, so it cannot key off stepStatus.
  let isLoadingStep = $state(false);
  let retryNonce = $state(0);
  // Index of the step whose chunk was retried, or -1. Chromium caches a failed
  // dynamic import for the life of the page, so when the retried import fails
  // too, only a page reload can fetch the chunk again.
  let retriedIndex = $state(-1);
  let reloadButtonRef = $state<HTMLButtonElement>();
  let backButtonRef = $state<HTMLButtonElement>();
  let primaryButtonRef = $state<HTMLButtonElement>();
  let leaveConfirmOpen = $state(false);
  // A save still running when setup closes keeps running, but some of its changes may
  // not be kept; the confirmation says so while isSaving, and follows it while open.
  // Both keys are literal so the i18n usage scanner sees them.
  let leaveMessage = $derived(
    wizardState.isSaving ? t('wizard.leaveConfirm.messageSaving') : t('wizard.leaveConfirm.message')
  );
  let importGeneration = 0;
  // retryNonce as of the last import, to tell a Retry from a step change
  let lastRetryNonce = 0;

  // Load component when step changes (for ComponentStep types).
  // The generation counter prevents stale imports from overwriting
  // the current step if the user navigates before an import resolves.
  // The step is marked ready only after its component is mounted and has had a
  // chance to report its validity; until then Next stays disabled.
  $effect(() => {
    const step = wizardState.currentStep;
    const index = wizardState.currentStepIndex;
    const isRetry = retryNonce !== lastRetryNonce; // Retry re-runs the import
    lastRetryNonce = retryNonce;
    const gen = ++importGeneration;
    // A step change (or a relaunch) offers Retry again
    if (!isRetry) retriedIndex = -1;
    if (step?.type === 'component') {
      // A step move already starts as 'loading'; this also covers Retry
      untrack(() => wizardState.setStepStatus('loading', index));
      isLoadingStep = true;
      loadedComponent = null;
      step.component().then(
        async mod => {
          if (gen !== importGeneration) return;
          loadedComponent = mod.default;
          loadedIndex = index;
          isLoadingStep = false;
          // tick() flushes the step's mount effects, so its first validity report
          // lands before the step is marked ready.
          await tick();
          if (gen !== importGeneration) return;
          wizardState.setStepStatus('ready', index);
        },
        async err => {
          if (gen !== importGeneration) return;
          logger.error('Wizard step failed to load', err);
          loadedComponent = null;
          isLoadingStep = false;
          // Next stays blocked, but Back still works on a failed step
          wizardState.setStepStatus('failed', index);
          // Retry parked focus on the content box; hand it to Reload page
          if (retriedIndex === index && document.activeElement === contentRef) {
            await tick();
            if (gen === importGeneration) reloadButtonRef?.focus();
          }
        }
      );
    } else {
      loadedComponent = null;
      isLoadingStep = false;
      if (step) {
        // Content steps have no validation
        untrack(() => {
          wizardState.setStepValid(true, index);
          wizardState.setStepStatus('ready', index);
        });
      }
    }
  });

  // Close the confirmation if the wizard closes for another reason
  $effect(() => {
    if (!wizardState.isActive) leaveConfirmOpen = false;
  });

  // The Retry button leaves the DOM while the import re-runs; park focus on the
  // content box so keyboard focus stays inside the dialog.
  function retryLoad() {
    contentRef?.focus();
    retriedIndex = wizardState.currentStepIndex;
    retryNonce++;
  }

  // Retry has already failed once on this step, so only a reload can help. The
  // wizard opens again after the reload, and earlier steps are already saved.
  let retryExhausted = $derived(
    wizardState.stepStatus === 'failed' && retriedIndex === wizardState.currentStepIndex
  );

  // Focus falls to <body> when Back unmounts (it is not rendered on the first step) and
  // when a click took no focus, outside the dialog's focus trap. Move it to Back when that
  // button remains, otherwise to Next, unless the user has already moved it elsewhere.
  async function goBack() {
    await wizardState.back();
    await tick();
    const focusLost = document.activeElement === null || document.activeElement === document.body;
    if (!focusLost) return;
    // Back stays mounted from step 3 on; from step 2 it unmounts, so Next takes the focus
    (backButtonRef?.isConnected ? backButtonRef : primaryButtonRef)?.focus();
  }

  function reloadPage() {
    window.location.reload();
  }

  // Escape and X ask before closing the onboarding, since leaving dismisses it
  // permanently. The changelog flow just closes.
  function requestLeave() {
    if (wizardState.flow === 'onboarding') {
      leaveConfirmOpen = true;
    } else {
      wizardState.skip();
    }
  }

  // Close the confirmation first and let it restore focus into the wizard, then
  // close the wizard, so the wizard's own focus restore runs last and focus goes
  // back to where it was before the wizard opened.
  async function confirmLeave() {
    leaveConfirmOpen = false;
    await tick();
    if (wizardState.isActive) wizardState.skip();
  }

  // Why Next is blocked, by priority; empty when it is not blocked. While saving,
  // Next itself reads Saving, and a failed load is explained by the alert, so the
  // footer stays empty for both.
  let nextReason = $derived.by(() => {
    if (wizardState.canAdvance) return '';
    if (wizardState.isSaving) return '';
    if (wizardState.stepStatus === 'failed') return '';
    if (wizardState.stepStatus === 'loading') return t('wizard.status.loadingStep');
    return t(wizardState.stepBlockedReason ?? 'wizard.reasons.completeStep');
  });

  let alertText = $derived.by(() => {
    if (wizardState.stepError) return t(wizardState.stepError);
    if (retryExhausted) return t('wizard.errors.stepLoadFailedReload');
    if (wizardState.stepStatus === 'failed') return t('wizard.errors.stepLoadFailed');
    return '';
  });

  // The element that explains why Next is blocked: the footer reason, or the
  // alert when the step failed to load. While saving, Next's own label says so.
  let nextDescribedBy = $derived.by(() => {
    if (wizardState.canAdvance) return undefined;
    if (nextReason) return NEXT_REASON_ID;
    if (alertText) return ALERT_ID;
    return undefined;
  });

  // Back is blocked while saving (explained by the saving status) or while the
  // step loads (explained by the footer reason)
  let backDescribedBy = $derived.by(() => {
    if (wizardState.canGoBack) return undefined;
    if (wizardState.isSaving) return SAVING_STATUS_ID;
    if (nextReason) return NEXT_REASON_ID;
    return undefined;
  });

  let stepSize = $derived(
    wizardState.currentStep?.type === 'component' && wizardState.currentStep.size === 'wide'
      ? WIDE_STEP_SIZE
      : DEFAULT_STEP_SIZE
  );

  // Resolve step title: use i18n key if available, fall back to plain string
  let stepTitle = $derived.by(() => {
    const step = wizardState.currentStep;
    if (!step) return '';
    if (step.type === 'component') return t(step.titleKey);
    if (step.titleKey) return t(step.titleKey);
    return step.title ?? '';
  });
</script>

<Modal
  isOpen={wizardState.isActive}
  size={stepSize}
  className="w-full"
  scrollBody
  showCloseButton={true}
  closeOnBackdrop={false}
  closeOnEsc={!leaveConfirmOpen}
  onClose={requestLeave}
  aria-labelledby={TITLE_ID}
>
  {#snippet header()}
    <!-- pe-10 keeps the row clear of the close button, which sits in the panel corner -->
    <div class="flex items-center justify-between pe-10">
      <h3 id={TITLE_ID} class="text-lg font-bold">{stepTitle}</h3>
      <WizardProgressBar
        currentStep={wizardState.currentStepIndex}
        totalSteps={wizardState.totalSteps}
        flow={wizardState.flow ?? 'onboarding'}
      />
    </div>
  {/snippet}

  {#snippet children()}
    <div
      bind:this={contentRef}
      tabindex="-1"
      inert={wizardState.isSaving}
      aria-busy={wizardState.isSaving ? 'true' : undefined}
      class={STEP_BODY_CLASS}
    >
      {#if isLoadingStep}
        <LoadingSpinner
          size="md"
          label={t('common.loading')}
          class="flex flex-1 items-center justify-center"
        />
      {:else if wizardState.stepStatus === 'failed'}
        <div class="flex flex-1 items-center justify-center">
          {#if retryExhausted}
            <Button variant="default" size="md" bind:ref={reloadButtonRef} onclick={reloadPage}>
              <RefreshCw class="size-4" />
              {t('wizard.actions.reloadPage')}
            </Button>
          {:else}
            <Button variant="default" size="md" onclick={retryLoad}>
              <RotateCw class="size-4" />
              {t('common.retry')}
            </Button>
          {/if}
        </div>
      {:else if wizardState.currentStep?.type === 'content'}
        <WizardContentRenderer step={wizardState.currentStep} />
      {:else if loadedComponent}
        {@const StepComponent = loadedComponent}
        <StepComponent
          onValidChange={(valid, reason) => wizardState.setStepValid(valid, loadedIndex, reason)}
          registerLeaveHandler={wizardState.registerLeaveHandler}
        />
      {/if}
    </div>
    <span id={SAVING_STATUS_ID} role="status" class="sr-only"
      >{wizardState.isSaving ? t('wizard.status.saving') : ''}</span
    >
  {/snippet}

  {#snippet footer()}
    <div class="flex w-full flex-col gap-2">
      <!-- The alert sits in the footer, above the buttons, so scrolling the body never
         hides it. It stays in the DOM as a live region but takes no space while it
         is empty, so the dialog is no taller than it needs to be -->
      <p
        id={ALERT_ID}
        role="alert"
        class={alertText ? 'text-sm text-[var(--text-error)]' : 'sr-only'}
      >
        {alertText}
      </p>
      <!-- One row: the reason sits beside the buttons and wraps in full rather than
         being cut off, so a long translation can make the footer taller -->
      <div class="flex w-full items-center gap-3">
        <Button variant="ghost" size="md" className="shrink-0" onclick={() => wizardState.skip()}>
          {t('wizard.skip')}
        </Button>
        <p
          id={NEXT_REASON_ID}
          class="min-w-0 flex-1 text-right text-sm leading-tight text-[var(--color-base-content)] opacity-70"
          title={nextReason || undefined}
        >
          {nextReason}
        </p>
        <div class="flex shrink-0 items-center gap-2">
          {#if !wizardState.isFirstStep}
            <Button
              variant="default"
              size="md"
              bind:ref={backButtonRef}
              onclick={() => void goBack()}
              aria-disabled={!wizardState.canGoBack ? 'true' : undefined}
              aria-describedby={backDescribedBy}
            >
              <ChevronLeft class="size-4" />
              {t('wizard.back')}
            </Button>
          {/if}
          <Button
            variant="primary"
            size="md"
            bind:ref={primaryButtonRef}
            onclick={() => (wizardState.isLastStep ? wizardState.complete() : wizardState.next())}
            aria-disabled={!wizardState.canAdvance ? 'true' : undefined}
            aria-describedby={nextDescribedBy}
          >
            {#if wizardState.isSaving}
              <LoadingSpinner
                size="sm"
                color="text-[var(--color-primary-content)]"
                aria-hidden="true"
              />
              {t('wizard.status.saving')}
            {:else if wizardState.isLastStep}
              <Check class="size-4" />
              {t('wizard.done')}
            {:else}
              {t('wizard.next')}
              <ChevronRight class="size-4" />
            {/if}
          </Button>
        </div>
      </div>
    </div>
  {/snippet}
</Modal>

{#if leaveConfirmOpen}
  <Modal
    isOpen={true}
    type="confirm"
    size="sm"
    role="alertdialog"
    aria-labelledby={LEAVE_TITLE_ID}
    aria-describedby={LEAVE_DESC_ID}
    confirmLabel={t('wizard.leaveConfirm.leave')}
    cancelLabel={t('wizard.leaveConfirm.stay')}
    onClose={() => (leaveConfirmOpen = false)}
    onConfirm={confirmLeave}
  >
    {#snippet header()}
      <h3 id={LEAVE_TITLE_ID} class="mb-2 text-lg font-bold">{t('wizard.leaveConfirm.title')}</h3>
      <p id={LEAVE_DESC_ID} class="text-sm">{leaveMessage}</p>
    {/snippet}
  </Modal>
{/if}
