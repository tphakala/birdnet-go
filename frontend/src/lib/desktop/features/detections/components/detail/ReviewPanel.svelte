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
  import { untrack } from 'svelte';
  import { ArrowLeftRight, Check, Lock, X } from '@lucide/svelte';
  import ToggleField from '$lib/desktop/components/forms/ToggleField.svelte';
  import Button from '$lib/desktop/components/ui/Button.svelte';
  import Kbd from '$lib/desktop/components/ui/Kbd.svelte';
  import { t } from '$lib/i18n';
  import { toastActions } from '$lib/stores/toast';
  import { api } from '$lib/utils/api';
  import { loggers } from '$lib/utils/logger';
  import { generateId } from '$lib/utils/uuid';
  import type { ShortcutHint } from '$lib/utils/keyboard';
  import DetailCard from './DetailCard.svelte';
  import type {
    DetectionDetailData,
    ReviewControls,
    ReviewShortcutAction,
    SavedChange,
  } from './detailTypes';

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
    /**
     * Called after a change was saved, so the page can reload the detection. The page may
     * answer with a promise that settles when that reload does: false when it failed.
     */
    onChanged: (_change: SavedChange) => Promise<boolean> | void;
    /**
     * Opt-in: called once with the controls a keyboard shortcut can use, and again when this
     * callback changes. Returns the function that detaches them; the panel calls it on
     * unmount and before attaching to a new callback.
     */
    onControls?: (_controls: ReviewControls) => () => void;
    /**
     * Opt-in: the shortcut key of each verdict button. A button with a hint shows the key as
     * a badge and names it in `aria-keyshortcuts`; without one it shows neither.
     */
    shortcutHints?: ReviewShortcutHints;
  }

  /** Key hints for the three verdict buttons and the Keep switch. */
  interface ReviewShortcutHints {
    correct?: ShortcutHint | null;
    falsePositive?: ShortcutHint | null;
    wrongSpecies?: ShortcutHint | null;
    keep?: ShortcutHint | null;
  }

  let {
    detection,
    speciesName,
    canReview,
    speciesBlockedReason,
    grow = false,
    onWrongSpecies,
    onChanged,
    onControls,
    shortcutHints = {},
  }: Props = $props();

  const logger = loggers.ui;
  const statusId = generateId('review-status');

  let saving = $state<Verdict | 'lock' | 'ignore' | null>(null);
  let errorMessage = $state('');
  // The species ignored from this panel; a species change on the same detection names
  // another species, which is not ignored.
  let ignoredSpecies = $state<string | null>(null);
  let ignored = $derived(ignoredSpecies === detection.scientificName);
  // The Keep state asked for, shown while it is saved and while the page reloads the
  // detection; afterwards the switch shows the state the page holds.
  let lockTarget = $state<boolean | null>(null);

  let verdictBlocked = $derived(!canReview || detection.locked);
  // A save still out blocks every control, which then points at the status line saying so.
  let busy = $derived(saving !== null);
  let verdictUnavailable = $derived(verdictBlocked || busy);
  let speciesBlocked = $derived(speciesBlockedReason !== '');
  // A species change waits for a save still out: the status line then says it is saving.
  let wrongSpeciesBlocked = $derived(speciesBlocked || busy);
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

  // The detection was reloaded (after a later save went through): an earlier failure's
  // message no longer describes it. Only the reload is tracked, not the message.
  $effect(() => {
    void detection;
    errorMessage = '';
  });

  function fail(message: string) {
    errorMessage = message;
    toastActions.error(message);
  }

  async function setVerdict(verdict: Verdict) {
    if (verdictBlocked || saving !== null || detection.verified === verdict) return;
    // The detection the change is saved on, kept for when the save answers.
    const id = detection.id;
    const scientificName = detection.scientificName;
    saving = verdict;
    errorMessage = '';
    try {
      await api.post(`/api/v2/detections/${id}/review`, { verified: verdict });
    } catch (error) {
      logger.error('Failed to save verdict', error, { action: 'review' });
      saving = null;
      fail(t('common.review.errors.verificationFailed'));
      return;
    }
    toastActions.success(
      verdict === 'correct'
        ? t('detections.detail.review.savedCorrect')
        : t('detections.detail.review.savedFalsePositive')
    );
    // The panel stays busy until the page's reload settles, so it never offers a choice
    // against the detection as it was before the save.
    try {
      const reloaded = await onChanged({ detectionId: id, scientificName, speciesStats: true });
      if (reloaded === false) fail(t('detections.detail.review.reloadFailed'));
    } finally {
      saving = null;
    }
  }

  // A refused change leaves the switch as it was: ToggleField shows the value it is given.
  async function setLocked(locked: boolean) {
    if (locked === keepValue || !canReview || saving !== null) return;
    const id = detection.id;
    const scientificName = detection.scientificName;
    saving = 'lock';
    errorMessage = '';
    lockTarget = locked;
    try {
      await api.post(`/api/v2/detections/${id}/lock`, { locked });
    } catch (error) {
      logger.error('Failed to change lock', error, { action: 'lock' });
      lockTarget = null;
      saving = null;
      fail(t('common.review.errors.lockStatusFailed'));
      return;
    }
    // Saved. The switch keeps the asked state until the page's reload settles, then shows
    // what the page holds: the rest of the panel and the page read that, not the request.
    try {
      const reloaded = await onChanged({ detectionId: id, scientificName, speciesStats: false });
      if (reloaded === false) fail(t('detections.detail.review.reloadFailed'));
    } finally {
      lockTarget = null;
      saving = null;
    }
  }

  async function ignoreSpecies() {
    if (!canIgnore || saving !== null || ignored) return;
    const id = detection.id;
    const species = detection.scientificName;
    saving = 'ignore';
    errorMessage = '';
    try {
      await api.post(`/api/v2/detections/${id}/review`, {
        verified: 'false_positive',
        // The server keeps the exclude list by scientific name (it resolves a common name to
        // one); a common name can be empty or shared by two species.
        ignore_species: species,
      });
    } catch (error) {
      logger.error('Failed to ignore species', error, { action: 'ignore' });
      saving = null;
      fail(t('common.review.errors.saveFailed'));
      return;
    }
    ignoredSpecies = species;
    toastActions.success(t('detections.detail.review.ignoredSpecies', { species: speciesName }));
    // Saving the review again moves its last-saved time, which the activity log shows. The
    // panel stays busy until the page's reload settles.
    try {
      const reloaded = await onChanged({
        detectionId: id,
        scientificName: species,
        speciesStats: false,
      });
      if (reloaded === false) fail(t('detections.detail.review.reloadFailed'));
    } finally {
      saving = null;
    }
  }

  function wrongSpecies() {
    if (!wrongSpeciesBlocked) onWrongSpecies();
  }

  let verdictGroup = $state<HTMLDivElement | null>(null);

  // What a keyboard shortcut can ask of the panel. The methods read live state when called.
  // Reasons come in the order statusText gives them: a save out first, then the rest.
  const controls: ReviewControls = {
    blockedReason: (action: ReviewShortcutAction): string => {
      if (!canReview) return t('detections.detail.review.loginRequired');
      if (busy) return t('detections.detail.review.saving');
      switch (action) {
        case 'correct':
        case 'falsePositive':
          return detection.locked ? t('detections.detail.review.lockedHelp') : '';
        case 'wrongSpecies':
          return speciesBlockedReason;
        case 'keep':
          // A kept detection can be released, so a lock never blocks Keep
          return '';
      }
    },
    run: (action: ReviewShortcutAction) => {
      switch (action) {
        case 'correct':
          void setVerdict('correct');
          break;
        case 'falsePositive':
          void setVerdict('false_positive');
          break;
        case 'wrongSpecies':
          wrongSpecies();
          break;
        case 'keep':
          void setLocked(!keepValue);
          break;
      }
    },
    focus: () => verdictGroup?.focus(),
  };

  $effect(() => {
    const attach = onControls;
    return attach ? untrack(() => attach(controls)) : undefined;
  });
</script>

<!-- The key is visual only: the button's name already says what it does, and the key is in
     aria-keyshortcuts -->
{#snippet keyBadge(hint: ShortcutHint | null | undefined)}
  {#if hint}
    <Kbd keys={hint.keys} size="sm" />
  {/if}
{/snippet}

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

  <div
    bind:this={verdictGroup}
    class="dd-verdicts"
    role="group"
    aria-label={t('detections.detail.review.title')}
    tabindex="-1"
  >
    <Button
      variant={detection.verified === 'correct' || saving === 'correct' ? 'success' : 'default'}
      size="md"
      aria-pressed={detection.verified === 'correct'}
      aria-disabled={verdictUnavailable ? 'true' : undefined}
      aria-describedby={verdictUnavailable ? statusId : undefined}
      aria-keyshortcuts={shortcutHints.correct?.aria}
      onclick={() => setVerdict('correct')}
    >
      <Check class="size-4" />
      {t('detections.detail.review.correct')}
      {@render keyBadge(shortcutHints.correct)}
    </Button>
    <Button
      variant={detection.verified === 'false_positive' || saving === 'false_positive'
        ? 'error'
        : 'default'}
      size="md"
      aria-pressed={detection.verified === 'false_positive'}
      aria-disabled={verdictUnavailable ? 'true' : undefined}
      aria-describedby={verdictUnavailable ? statusId : undefined}
      aria-keyshortcuts={shortcutHints.falsePositive?.aria}
      onclick={() => setVerdict('false_positive')}
    >
      <X class="size-4" />
      {t('detections.detail.review.falsePositive')}
      {@render keyBadge(shortcutHints.falsePositive)}
    </Button>
    <Button
      variant="default"
      size="md"
      aria-haspopup="dialog"
      aria-disabled={wrongSpeciesBlocked ? 'true' : undefined}
      aria-describedby={wrongSpeciesBlocked ? statusId : undefined}
      aria-keyshortcuts={shortcutHints.wrongSpecies?.aria}
      onclick={wrongSpecies}
    >
      <ArrowLeftRight class="size-4" />
      {t('detections.detail.review.wrongSpecies')}
      {@render keyBadge(shortcutHints.wrongSpecies)}
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
          disabled={busy}
          describedBy={busy ? statusId : undefined}
          ariaKeyshortcuts={shortcutHints.keep?.aria}
          onUpdate={setLocked}
        >
          {#snippet badges()}
            {@render keyBadge(shortcutHints.keep)}
          {/snippet}
        </ToggleField>
      </div>
      <!-- A fixed slot: the button takes its place whether or not it is shown, so
           the switch beside it does not move when a verdict changes. Hidden, it is
           also out of the tab order and the accessibility tree. -->
      <div class="dd-review-ignore" class:dd-review-ignore-hidden={!canIgnore}>
        <Button
          variant="default"
          size="sm"
          aria-disabled={ignored || busy ? 'true' : undefined}
          aria-describedby={busy ? statusId : undefined}
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

    /* The group takes focus from a shortcut: a ring that hugs the buttons, not the whole row */
    width: fit-content;
    max-width: 100%;
    border-radius: 0.5rem;
  }

  .dd-verdicts:focus-visible {
    outline: 2px solid var(--color-primary);
    outline-offset: 2px;
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
