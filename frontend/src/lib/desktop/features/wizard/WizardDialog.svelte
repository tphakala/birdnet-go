<script lang="ts">
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
  import WizardProgressBar from './WizardProgressBar.svelte';
  import WizardContentRenderer from './WizardContentRenderer.svelte';
  import { wizardState } from './wizardState.svelte';
  import { t } from '$lib/i18n';
  import { ChevronLeft, ChevronRight, Check, RotateCw } from '@lucide/svelte';
  import { tick, untrack } from 'svelte';
  import type { Component } from 'svelte';
  import type { WizardStepProps } from './types';
  import { generateId } from '$lib/utils/uuid';
  import { loggers } from '$lib/utils/logger';

  const logger = loggers.ui;

  const NEXT_REASON_ID = generateId('wizard-next-reason');
  const LEAVE_TITLE_ID = generateId('wizard-leave-title');
  const LEAVE_DESC_ID = generateId('wizard-leave-desc');

  let modalRef = $state<Modal>();
  let loadedComponent = $state<Component<WizardStepProps> | null>(null);
  // Index of the step the rendered component was loaded for
  let loadedIndex = $state(-1);
  let isLoadingStep = $state(false);
  let loadFailed = $state(false);
  let retryNonce = $state(0);
  let leaveConfirmOpen = $state(false);
  let importGeneration = 0;

  // Load component when step changes (for ComponentStep types).
  // The generation counter prevents stale imports from overwriting
  // the current step if the user navigates before an import resolves.
  // The step is marked ready only after its component is mounted and has had a
  // chance to report its validity; until then Next stays disabled.
  $effect(() => {
    const step = wizardState.currentStep;
    const index = wizardState.currentStepIndex;
    void retryNonce; // Retry re-runs the import
    const gen = ++importGeneration;
    loadFailed = false;
    if (step?.type === 'component') {
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
          wizardState.markStepReady(index);
          modalRef?.refreshFocusTrap();
        },
        err => {
          if (gen !== importGeneration) return;
          logger.error('Wizard step failed to load', err);
          loadedComponent = null;
          isLoadingStep = false;
          loadFailed = true;
        }
      );
    } else {
      loadedComponent = null;
      isLoadingStep = false;
      if (step) {
        // Content steps have no validation
        untrack(() => {
          wizardState.setStepValid(true, index);
          wizardState.markStepReady(index);
        });
      }
      // Refresh focus trap for ContentStep transitions too
      tick().then(() => modalRef?.refreshFocusTrap());
    }
  });

  // Close the confirmation if the wizard closes for another reason
  $effect(() => {
    if (!wizardState.isActive) leaveConfirmOpen = false;
  });

  function handleNext() {
    if (!wizardState.canAdvance) return;
    void (wizardState.isLastStep ? wizardState.complete() : wizardState.next());
  }

  function handleBack() {
    if (wizardState.isSaving) return;
    void wizardState.back();
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

  function confirmLeave() {
    leaveConfirmOpen = false;
    wizardState.skip();
  }

  // Why Next is blocked, by priority; empty when it is not blocked
  let nextReason = $derived.by(() => {
    if (wizardState.canAdvance) return '';
    if (wizardState.isSaving) return t('wizard.status.saving');
    if (loadFailed) return t('wizard.errors.stepLoadFailed');
    if (!wizardState.isStepReady) return t('wizard.status.loadingStep');
    return t('wizard.reasons.completeStep');
  });

  let alertText = $derived.by(() => {
    if (wizardState.stepError) return t(wizardState.stepError);
    if (loadFailed) return t('wizard.errors.stepLoadFailed');
    return '';
  });

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
  bind:this={modalRef}
  isOpen={wizardState.isActive}
  size="2xl"
  className="w-full"
  showCloseButton={true}
  closeOnBackdrop={false}
  closeOnEsc={!leaveConfirmOpen}
  onClose={requestLeave}
>
  {#snippet header()}
    <div class="flex items-center justify-between">
      <h3 id="modal-title" class="text-lg font-bold">{stepTitle}</h3>
      <WizardProgressBar
        currentStep={wizardState.currentStepIndex}
        totalSteps={wizardState.totalSteps}
        flow={wizardState.flow ?? 'onboarding'}
      />
    </div>
  {/snippet}

  {#snippet children()}
    <div
      class="h-[33rem] rounded-lg border border-[var(--border-200)] bg-[var(--color-base-200)]/30 px-4 py-3"
    >
      {#if isLoadingStep}
        <div class="flex h-full items-center justify-center" role="status">
          <span
            class="inline-block size-6 animate-spin rounded-full border-2 border-[var(--color-base-300)] border-t-[var(--color-primary)]"
          ></span>
          <span class="sr-only">{t('common.loading')}</span>
        </div>
      {:else if loadFailed}
        <div class="flex h-full items-center justify-center">
          <button
            type="button"
            class="inline-flex items-center gap-1.5 rounded-[var(--radius-field)] border border-[var(--border-200)] bg-transparent px-4 py-2 text-sm font-medium text-[var(--color-base-content)] transition-colors hover:bg-[var(--hover-overlay)]"
            onclick={() => retryNonce++}
          >
            <RotateCw class="size-4" />
            {t('common.retry')}
          </button>
        </div>
      {:else if wizardState.currentStep?.type === 'content'}
        <WizardContentRenderer step={wizardState.currentStep} />
      {:else if loadedComponent}
        {@const StepComponent = loadedComponent}
        <StepComponent
          onValidChange={valid => wizardState.setStepValid(valid, loadedIndex)}
          registerLeaveHandler={wizardState.registerLeaveHandler}
        />
      {/if}
    </div>
    <p role="alert" class="mt-2 min-h-5 text-sm text-[var(--color-error)]">{alertText}</p>
    <span role="status" class="sr-only"
      >{wizardState.isSaving ? t('wizard.status.saving') : ''}</span
    >
  {/snippet}

  {#snippet footer()}
    <div class="flex w-full flex-col gap-2">
      {#if nextReason}
        <p
          id={NEXT_REASON_ID}
          class="text-right text-sm text-[var(--color-base-content)] opacity-70"
        >
          {nextReason}
        </p>
      {/if}
      <div class="flex w-full items-center justify-between">
        <button
          type="button"
          class="inline-flex items-center gap-1.5 rounded-[var(--radius-field)] px-3 py-1.5 text-sm font-medium text-[var(--color-base-content)] opacity-70 transition-colors hover:bg-[var(--hover-overlay)] hover:opacity-100"
          onclick={() => wizardState.skip()}
        >
          {t('wizard.skip')}
        </button>
        <div class="flex items-center gap-2">
          {#if !wizardState.isFirstStep}
            <button
              type="button"
              class="inline-flex items-center gap-1.5 rounded-[var(--radius-field)] border border-[var(--border-200)] bg-transparent px-4 py-2 text-sm font-medium text-[var(--color-base-content)] transition-colors hover:bg-[var(--hover-overlay)] aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
              onclick={handleBack}
              aria-disabled={wizardState.isSaving ? 'true' : undefined}
              aria-describedby={wizardState.isSaving ? NEXT_REASON_ID : undefined}
            >
              <ChevronLeft class="size-4" />
              {t('wizard.back')}
            </button>
          {/if}
          <button
            type="button"
            class="inline-flex items-center gap-1.5 rounded-[var(--radius-field)] border border-[var(--color-primary)] bg-[var(--color-primary)] px-4 py-2 text-sm font-medium text-[var(--color-primary-content)] transition-colors hover:bg-[var(--color-primary-hover)] aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
            onclick={handleNext}
            aria-disabled={!wizardState.canAdvance ? 'true' : undefined}
            aria-describedby={!wizardState.canAdvance ? NEXT_REASON_ID : undefined}
          >
            {#if wizardState.isSaving}
              <span
                class="inline-block size-4 animate-spin rounded-full border-2 border-[var(--color-primary-content)]/40 border-t-[var(--color-primary-content)]"
                aria-hidden="true"
              ></span>
              {t('wizard.status.saving')}
            {:else if wizardState.isLastStep}
              <Check class="size-4" />
              {t('wizard.done')}
            {:else}
              {t('wizard.next')}
              <ChevronRight class="size-4" />
            {/if}
          </button>
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
    confirmVariant="primary"
    onClose={() => (leaveConfirmOpen = false)}
    onConfirm={confirmLeave}
  >
    {#snippet header()}
      <h3 id={LEAVE_TITLE_ID} class="mb-2 text-lg font-bold">{t('wizard.leaveConfirm.title')}</h3>
      <p id={LEAVE_DESC_ID} class="text-sm">{t('wizard.leaveConfirm.message')}</p>
    {/snippet}
  </Modal>
{/if}
