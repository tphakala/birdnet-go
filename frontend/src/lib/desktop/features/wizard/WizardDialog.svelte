<script lang="ts">
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
  import WizardProgressBar from './WizardProgressBar.svelte';
  import WizardContentRenderer from './WizardContentRenderer.svelte';
  import { wizardState } from './wizardState.svelte';
  import { t } from '$lib/i18n';
  import { ChevronLeft, ChevronRight, Check } from '@lucide/svelte';
  import { tick } from 'svelte';
  import type { Component } from 'svelte';
  import type { WizardStepProps } from './types';

  let modalRef = $state<Modal>();
  let loadedComponent = $state<Component<WizardStepProps> | null>(null);
  let isLoadingStep = $state(false);
  let importGeneration = 0;

  // Load component when step changes (for ComponentStep types).
  // The generation counter prevents stale imports from overwriting
  // the current step if the user navigates before an import resolves.
  $effect(() => {
    const step = wizardState.currentStep;
    const gen = ++importGeneration;
    if (step?.type === 'component') {
      isLoadingStep = true;
      loadedComponent = null;
      step
        .component()
        .then(async mod => {
          if (gen !== importGeneration) return;
          loadedComponent = mod.default;
          isLoadingStep = false;
          await tick();
          modalRef?.refreshFocusTrap();
        })
        .catch(() => {
          if (gen !== importGeneration) return;
          loadedComponent = null;
          isLoadingStep = false;
        });
    } else {
      loadedComponent = null;
      isLoadingStep = false;
      // Refresh focus trap for ContentStep transitions too
      tick().then(() => modalRef?.refreshFocusTrap());
    }
  });

  function handleStepValidChange(valid: boolean) {
    wizardState.setStepValid(valid);
  }

  function handleNext() {
    if (wizardState.isLastStep) {
      wizardState.complete();
    } else {
      wizardState.next();
    }
  }

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
  size="lg"
  closeOnBackdrop={false}
  onClose={() => wizardState.skip()}
>
  {#snippet header()}
    <div class="mb-4 flex items-center justify-between">
      <h3 id="modal-title" class="text-lg font-bold">{stepTitle}</h3>
      <WizardProgressBar
        currentStep={wizardState.currentStepIndex}
        totalSteps={wizardState.totalSteps}
        flow={wizardState.flow ?? 'onboarding'}
      />
    </div>
  {/snippet}

  {#snippet children()}
    <div class="min-h-[200px]">
      {#if isLoadingStep}
        <div class="flex items-center justify-center py-12" role="status">
          <span
            class="inline-block size-6 animate-spin rounded-full border-2 border-[var(--color-base-300)] border-t-[var(--color-primary)]"
          ></span>
          <span class="sr-only">{t('common.loading')}</span>
        </div>
      {:else if wizardState.currentStep?.type === 'content'}
        <WizardContentRenderer step={wizardState.currentStep} />
      {:else if loadedComponent}
        {@const StepComponent = loadedComponent}
        <StepComponent onValidChange={handleStepValidChange} />
      {/if}
    </div>
  {/snippet}

  {#snippet footer()}
    <div class="flex items-center justify-between">
      <button
        type="button"
        class="inline-flex items-center gap-1.5 rounded-[var(--radius-field)] px-3 py-1.5 text-sm font-medium text-[var(--color-base-content)] opacity-70 transition-opacity hover:opacity-100"
        onclick={() => wizardState.skip()}
      >
        {t('wizard.skip')}
      </button>
      <div class="flex items-center gap-2">
        {#if !wizardState.isFirstStep}
          <button
            type="button"
            class="inline-flex items-center gap-1.5 rounded-[var(--radius-field)] border border-[var(--border-200)] bg-transparent px-4 py-2 text-sm font-medium text-[var(--color-base-content)] transition-colors hover:bg-[var(--hover-overlay)]"
            onclick={() => wizardState.back()}
          >
            <ChevronLeft class="size-4" />
            {t('wizard.back')}
          </button>
        {/if}
        <button
          type="button"
          class="inline-flex items-center gap-1.5 rounded-[var(--radius-field)] border border-[var(--color-primary)] bg-[var(--color-primary)] px-4 py-2 text-sm font-medium text-[var(--color-primary-content)] transition-colors hover:bg-[var(--color-primary-hover)] disabled:cursor-not-allowed disabled:opacity-50"
          onclick={handleNext}
          disabled={!wizardState.isStepValid}
        >
          {#if wizardState.isLastStep}
            <Check class="size-4" />
            {t('wizard.done')}
          {:else}
            {t('wizard.next')}
            <ChevronRight class="size-4" />
          {/if}
        </button>
      </div>
    </div>
  {/snippet}
</Modal>
