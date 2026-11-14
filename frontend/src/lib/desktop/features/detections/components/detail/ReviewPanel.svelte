<!--
  ReviewPanel - the verdict on a detection.

  Three buttons: Correct and False positive record the verdict the moment one is
  pressed, and Wrong species opens the species dialog. A switch keeps (locks)
  the detection so cleanup never removes it; a kept detection cannot change its
  verdict or species.

  Layout rule: no control in this panel moves when the state changes, and no
  space is held empty for text that may never come. The one line of status text
  (why a control is blocked, saving, an error) is the card's subtitle, which is
  always there. The offer to ignore a species has a fixed slot at the end of the
  Keep row, shown only while it applies.
-->
<script lang="ts">
  import { ArrowLeftRight, Check, Lock, X } from '@lucide/svelte';
  import ToggleField from '$lib/desktop/components/forms/ToggleField.svelte';
  import Button from '$lib/desktop/components/ui/Button.svelte';
  import { t } from '$lib/i18n';
  import { toastActions } from '$lib/stores/toast';
  import { fetchWithCSRF } from '$lib/utils/api';
  import { loggers } from '$lib/utils/logger';
  import { generateId } from '$lib/utils/uuid';
  import type { DetectionDetailData } from './detailTypes';

  type Verdict = 'correct' | 'false_positive';

  interface Props {
    detection: DetectionDetailData;
    speciesName: string;
    /** False for visitors without review permission: the panel is then read-only. */
    canReview: boolean;
    /**
     * Whether the server can change a detection's species (it needs the current
     * database format). Null while that is not known yet.
     */
    speciesChangeSupported: boolean | null;
    /** Opens the species dialog. */
    onWrongSpecies: () => void;
    /** Called after a change was saved, so the page can reload the detection. */
    onChanged: () => void;
  }

  let {
    detection,
    speciesName,
    canReview,
    speciesChangeSupported,
    onWrongSpecies,
    onChanged,
  }: Props = $props();

  const logger = loggers.ui;
  const headingId = generateId('review-heading');
  const statusId = generateId('review-status');

  let saving = $state<Verdict | 'lock' | 'ignore' | null>(null);
  let errorMessage = $state('');
  let ignored = $state(false);
  // Bumped to redraw the switch from the saved state after a change that did not go through
  // (the native checkbox has already flipped by the time the request is refused).
  let keepSwitchEpoch = $state(0);

  let verdictBlocked = $derived(!canReview || detection.locked);
  let speciesBlocked = $derived(verdictBlocked || speciesChangeSupported === false);
  let canIgnore = $derived(
    canReview && detection.verified === 'false_positive' && !detection.locked
  );

  // The one line of status text, the most pressing reason first.
  let statusText = $derived.by(() => {
    if (errorMessage) return errorMessage;
    if (saving) return t('detections.detail.review.saving');
    if (!canReview) return t('detections.detail.review.loginRequired');
    if (detection.locked) return t('detections.detail.review.lockedHelp');
    if (speciesChangeSupported === false) return t('detections.detail.review.legacyHelp');
    return t('detections.detail.review.autosave');
  });

  // A different detection starts with a clean panel.
  $effect(() => {
    void detection.id;
    errorMessage = '';
    ignored = false;
  });

  async function post(path: string, body: unknown): Promise<void> {
    await fetchWithCSRF(path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
  }

  async function setVerdict(verdict: Verdict) {
    if (verdictBlocked || saving !== null || detection.verified === verdict) return;
    saving = verdict;
    errorMessage = '';
    try {
      await post(`/api/v2/detections/${detection.id}/review`, { verified: verdict });
      toastActions.success(
        verdict === 'correct'
          ? t('detections.detail.review.savedCorrect')
          : t('detections.detail.review.savedFalsePositive')
      );
      onChanged();
    } catch (error) {
      logger.error('Failed to save verdict', error, { action: 'review' });
      errorMessage = t('common.review.errors.verificationFailed');
    } finally {
      saving = null;
    }
  }

  async function setLocked(locked: boolean) {
    if (locked === detection.locked) return;
    if (!canReview || saving !== null) {
      keepSwitchEpoch += 1;
      return;
    }
    saving = 'lock';
    errorMessage = '';
    try {
      await post(`/api/v2/detections/${detection.id}/lock`, { locked });
      onChanged();
    } catch (error) {
      logger.error('Failed to change lock', error, { action: 'lock' });
      errorMessage = t('common.review.errors.lockStatusFailed');
      keepSwitchEpoch += 1;
    } finally {
      saving = null;
    }
  }

  async function ignoreSpecies() {
    if (!canIgnore || saving !== null || ignored) return;
    saving = 'ignore';
    errorMessage = '';
    try {
      await post(`/api/v2/detections/${detection.id}/review`, {
        verified: 'false_positive',
        ignore_species: detection.commonName,
      });
      ignored = true;
      toastActions.success(t('detections.detail.review.ignoredSpecies', { species: speciesName }));
    } catch (error) {
      logger.error('Failed to ignore species', error, { action: 'ignore' });
      errorMessage = t('common.review.errors.saveFailed');
    } finally {
      saving = null;
    }
  }

  function wrongSpecies() {
    if (!speciesBlocked) onWrongSpecies();
  }
</script>

<section class="dd-review" aria-labelledby={headingId}>
  <header class="dd-review-header">
    <h2 id={headingId} class="dd-review-title">{t('detections.detail.review.title')}</h2>
    <p
      id={statusId}
      class="dd-review-status"
      class:dd-review-status-error={Boolean(errorMessage)}
      role={errorMessage ? 'alert' : 'status'}
      title={statusText}
    >
      {statusText}
    </p>
  </header>

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
        {#key keepSwitchEpoch}
          <ToggleField
            label={t('detections.detail.review.keep')}
            description={t('detections.detail.review.keepHelp')}
            icon={Lock}
            value={detection.locked}
            onUpdate={setLocked}
          />
        {/key}
      </div>
      <!-- A fixed slot: the button takes its place whether or not it is shown, so
           the switch beside it does not move when a verdict changes. -->
      <div class="dd-review-ignore" class:dd-review-ignore-hidden={!canIgnore}>
        <Button
          variant="default"
          size="sm"
          tabindex={canIgnore ? undefined : -1}
          aria-hidden={canIgnore ? undefined : 'true'}
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
</section>

<style>
  .dd-review {
    display: flex;
    flex-direction: column;
    min-width: 0;
    padding: 0.875rem 1.25rem 1rem;
    background-color: var(--color-base-100);
    border: 1px solid var(--border-100);
    border-radius: var(--radius-box);
  }

  .dd-review-header {
    display: flex;
    align-items: baseline;
    gap: 0.625rem;
    min-width: 0;
  }

  .dd-review-title {
    flex: 0 0 auto;
    font-size: 0.6875rem;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
  }

  /* One line, always present; a longer message is cut and kept in the title. */
  .dd-review-status {
    flex: 1 1 0;
    min-width: 0;
    overflow: hidden;
    font-size: 0.75rem;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
  }

  .dd-review-status-error {
    color: var(--text-error);
  }

  .dd-review-question {
    margin-top: 0.625rem;
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
