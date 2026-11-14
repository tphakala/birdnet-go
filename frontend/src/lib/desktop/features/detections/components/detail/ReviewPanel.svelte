<!--
  ReviewPanel - the verdict on a detection.

  Three buttons: Correct and False positive record the verdict the moment one is
  pressed, and Wrong species opens the species dialog. A switch keeps (locks)
  the detection so cleanup never removes it; a kept detection cannot change its
  verdict or species.

  Layout rule: no control in this panel moves when the state changes, and no
  space is held empty for text that may never come. The one line of status text
  (why a control is blocked, saving, an error) is the card's subtitle, which is
  always there; an error is also sent as a toast, which shows it in full and
  announces it. The offer to ignore a species has a fixed slot at the end of the
  Keep row, shown only while it applies.
-->
<script lang="ts">
  import { ArrowLeftRight, Check, Lock, X } from '@lucide/svelte';
  import ToggleField from '$lib/desktop/components/forms/ToggleField.svelte';
  import Button from '$lib/desktop/components/ui/Button.svelte';
  import { t } from '$lib/i18n';
  import { toastActions } from '$lib/stores/toast';
  import { api } from '$lib/utils/api';
  import { loggers } from '$lib/utils/logger';
  import { generateId } from '$lib/utils/uuid';
  import DetailCard from './DetailCard.svelte';
  import type { DetectionDetailData } from './detailTypes';

  type Verdict = 'correct' | 'false_positive';

  interface Props {
    detection: DetectionDetailData;
    speciesName: string;
    /** False for visitors without review permission: the panel is then read-only. */
    canReview: boolean;
    /** Why the species cannot be changed now; empty when it can. */
    speciesBlockedReason: string;
    /** Takes the remaining height of the column the panel sits in. */
    grow?: boolean;
    /** Opens the species dialog. */
    onWrongSpecies: () => void;
    /** Called with the detection's id after a change to it was saved, so the page can reload it. */
    onChanged: (_detectionId: number) => void;
  }

  let {
    detection,
    speciesName,
    canReview,
    speciesBlockedReason,
    grow = false,
    onWrongSpecies,
    onChanged,
  }: Props = $props();

  const logger = loggers.ui;
  const statusId = generateId('review-status');

  let saving = $state<Verdict | 'lock' | 'ignore' | null>(null);
  let errorMessage = $state('');
  let ignored = $state(false);
  // The Keep state asked for, shown while it is saved and until the reloaded detection has it.
  let lockTarget = $state<boolean | null>(null);

  let verdictBlocked = $derived(!canReview || detection.locked);
  let speciesBlocked = $derived(speciesBlockedReason !== '');
  let canIgnore = $derived(
    canReview && detection.verified === 'false_positive' && !detection.locked
  );
  let keepValue = $derived(lockTarget ?? detection.locked);

  // The one line of status text, the most pressing reason first. The blocked reasons
  // come in the order the page puts them in speciesBlockedReason.
  let statusText = $derived.by(() => {
    if (errorMessage) return errorMessage;
    if (saving) return t('detections.detail.review.saving');
    return speciesBlockedReason || t('detections.detail.review.autosave');
  });

  // A different detection starts with a clean panel.
  $effect(() => {
    void detection.id;
    errorMessage = '';
    ignored = false;
    lockTarget = null;
  });

  // The reloaded detection has the Keep state that was asked for.
  $effect(() => {
    if (lockTarget !== null && detection.locked === lockTarget) lockTarget = null;
  });

  function fail(message: string) {
    errorMessage = message;
    toastActions.error(message);
  }

  async function setVerdict(verdict: Verdict) {
    if (verdictBlocked || saving !== null || detection.verified === verdict) return;
    // The detection the change is saved on, kept for when the save answers.
    const id = detection.id;
    saving = verdict;
    errorMessage = '';
    try {
      await api.post(`/api/v2/detections/${id}/review`, { verified: verdict });
      toastActions.success(
        verdict === 'correct'
          ? t('detections.detail.review.savedCorrect')
          : t('detections.detail.review.savedFalsePositive')
      );
      onChanged(id);
    } catch (error) {
      logger.error('Failed to save verdict', error, { action: 'review' });
      fail(t('common.review.errors.verificationFailed'));
    } finally {
      saving = null;
    }
  }

  // A refused change leaves the switch as it was: ToggleField shows the value it is given.
  async function setLocked(locked: boolean) {
    if (locked === keepValue || !canReview || saving !== null) return;
    const id = detection.id;
    saving = 'lock';
    errorMessage = '';
    lockTarget = locked;
    try {
      await api.post(`/api/v2/detections/${id}/lock`, { locked });
      onChanged(id);
    } catch (error) {
      logger.error('Failed to change lock', error, { action: 'lock' });
      lockTarget = null;
      fail(t('common.review.errors.lockStatusFailed'));
    } finally {
      saving = null;
    }
  }

  async function ignoreSpecies() {
    if (!canIgnore || saving !== null || ignored) return;
    saving = 'ignore';
    errorMessage = '';
    try {
      await api.post(`/api/v2/detections/${detection.id}/review`, {
        verified: 'false_positive',
        ignore_species: detection.commonName,
      });
      ignored = true;
      toastActions.success(t('detections.detail.review.ignoredSpecies', { species: speciesName }));
    } catch (error) {
      logger.error('Failed to ignore species', error, { action: 'ignore' });
      fail(t('common.review.errors.saveFailed'));
    } finally {
      saving = null;
    }
  }

  function wrongSpecies() {
    if (!speciesBlocked) onWrongSpecies();
  }
</script>

<DetailCard title={t('detections.detail.review.title')} padding="snug" {grow}>
  {#snippet header()}
    <!-- Not a live region: blocked reasons are read through aria-describedby on the
         buttons, and results and errors are announced by their toasts. -->
    <p
      id={statusId}
      class="dd-review-status"
      class:dd-review-status-error={Boolean(errorMessage)}
      title={statusText}
    >
      {statusText}
    </p>
  {/snippet}

  <p class="dd-review-question">
    {t('detections.detail.review.question', { species: speciesName })}
  </p>

  <div class="dd-verdicts" role="group" aria-label={t('detections.detail.review.title')}>
    <Button
      variant={detection.verified === 'correct' || saving === 'correct' ? 'success' : 'default'}
      size="md"
      aria-pressed={detection.verified === 'correct'}
      aria-disabled={verdictBlocked ? 'true' : undefined}
      aria-describedby={verdictBlocked ? statusId : undefined}
      onclick={() => setVerdict('correct')}
    >
      <Check class="size-4" />
      {t('detections.detail.review.correct')}
    </Button>
    <Button
      variant={detection.verified === 'false_positive' || saving === 'false_positive'
        ? 'error'
        : 'default'}
      size="md"
      aria-pressed={detection.verified === 'false_positive'}
      aria-disabled={verdictBlocked ? 'true' : undefined}
      aria-describedby={verdictBlocked ? statusId : undefined}
      onclick={() => setVerdict('false_positive')}
    >
      <X class="size-4" />
      {t('detections.detail.review.falsePositive')}
    </Button>
    <Button
      variant="default"
      size="md"
      aria-haspopup="dialog"
      aria-disabled={speciesBlocked ? 'true' : undefined}
      aria-describedby={speciesBlocked ? statusId : undefined}
      onclick={wrongSpecies}
    >
      <ArrowLeftRight class="size-4" />
      {t('detections.detail.review.wrongSpecies')}
    </Button>
  </div>

  {#if canReview}
    <div class="dd-review-keep">
      <div class="dd-review-keep-toggle">
        <ToggleField
          label={t('detections.detail.review.keep')}
          description={t('detections.detail.review.keepHelp')}
          icon={Lock}
          value={keepValue}
          onUpdate={setLocked}
        />
      </div>
      <!-- A fixed slot: the button takes its place whether or not it is shown, so
           the switch beside it does not move when a verdict changes. Hidden, it is
           also out of the tab order and the accessibility tree. -->
      <div class="dd-review-ignore" class:dd-review-ignore-hidden={!canIgnore}>
        <Button
          variant="default"
          size="sm"
          aria-disabled={ignored ? 'true' : undefined}
          title={t('common.review.form.ignoreSpeciesHelp')}
          onclick={ignoreSpecies}
        >
          {ignored
            ? t('detections.detail.review.ignoredShort')
            : t('detections.detail.review.ignoreShort')}
        </Button>
      </div>
    </div>
  {/if}
</DetailCard>

<style>
  /* One line, always present; a longer message is cut here and shown in full in its toast. */
  .dd-review-status {
    flex: 1 1 0;
    min-width: 0;
    overflow: hidden;
    font-size: 0.75rem;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--text-subtle);
  }

  .dd-review-status-error {
    color: var(--text-error);
  }

  .dd-review-question {
    font-size: 1rem;
    font-weight: 600;
  }

  .dd-verdicts {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    margin-top: 0.5rem;
  }

  .dd-review-keep {
    display: flex;
    align-items: center;
    gap: 1rem;
    margin-top: 0.75rem;
    padding-top: 0.625rem;
    border-top: 1px solid var(--border-100);
  }

  .dd-review-keep-toggle {
    flex: 1 1 0;
    min-width: 0;
  }

  .dd-review-ignore {
    flex: 0 0 auto;
  }

  .dd-review-ignore-hidden {
    visibility: hidden;
  }
</style>
