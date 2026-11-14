<!--
  ActivityLog - what has happened to a detection, oldest first: the detection
  itself, the saved clip, review and lock changes, and notes. Reviewers can add
  a note at the end.

  The backend keeps the time of the first review and of the latest change, not
  every change in between, so the log shows those two moments.
-->
<script lang="ts">
  import { ArrowLeftRight, AudioLines, Check, Lock, MessageSquare, Radar, X } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { toastActions } from '$lib/stores/toast';
  import { api } from '$lib/utils/api';
  import { loggers } from '$lib/utils/logger';
  import { localizeSpeciesName } from '$lib/utils/speciesDisplay';
  import { generateId } from '$lib/utils/uuid';
  import DetailCard from './DetailCard.svelte';
  import { toPercent } from './detailFormat';
  import { wallClockDateTime, wallClockTime } from './detailTime';
  import type { DetectionDetailData } from './detailTypes';

  type EntryKind =
    'detected' | 'clip' | 'correct' | 'false_positive' | 'species' | 'locked' | 'note';

  interface Entry {
    key: string;
    kind: EntryKind;
    /** RFC3339 time used for ordering; empty when the time was not recorded. */
    at: string;
    title: string;
    detail?: string;
    body?: string;
  }

  interface Props {
    detection: DetectionDetailData;
    speciesName: string;
    canReview: boolean;
    onChanged: () => void;
  }

  let { detection, speciesName, canReview, onChanged }: Props = $props();

  const logger = loggers.ui;
  const ENTRY_ICONS: Record<EntryKind, typeof Radar> = {
    detected: Radar,
    clip: AudioLines,
    correct: Check,
    false_positive: X,
    species: ArrowLeftRight,
    locked: Lock,
    note: MessageSquare,
  };
  const noteId = generateId('detail-note');
  const noteHelpId = generateId('detail-note-help');

  let note = $state('');
  let saving = $state(false);
  let errorMessage = $state<string | null>(null);

  // The model's own species, when a reviewer changed it to another; empty otherwise.
  let originalSpeciesName = $derived.by(() => {
    const original = detection.originalSpecies;
    if (!original || original.scientificName === detection.scientificName) return '';
    return localizeSpeciesName(original.scientificName, original.commonName);
  });

  let entries = $derived.by(() => {
    const list: Entry[] = [];
    const detectedAt = detection.timestamp ?? '';
    const begin = wallClockTime(detection.beginTime, true);
    const end = wallClockTime(detection.endTime, true);

    // After a reviewer changed the species, the detection entry names what the model heard.
    list.push({
      key: 'detected',
      kind: 'detected',
      at: detectedAt,
      title: t('detections.detail.log.detected', {
        species: originalSpeciesName || speciesName,
        // A species change keeps the confidence: it is the model's own.
        confidence: toPercent(detection.confidence),
      }),
      detail: begin && end ? t('detections.detail.log.captureWindow', { begin, end }) : undefined,
    });

    if (detection.clipName) {
      list.push({
        key: 'clip',
        kind: 'clip',
        at: detection.endTime || detectedAt,
        title: detection.clipDenoised
          ? t('detections.detail.log.clipSavedDenoised')
          : t('detections.detail.log.clipSaved'),
        detail: detection.clipName,
      });
    }

    if (detection.verified !== 'unverified') {
      const statusLabel =
        detection.verified === 'correct'
          ? t('common.review.status.verifiedCorrect')
          : t('common.review.status.falsePositive');
      const { reviewedAt, reviewUpdatedAt } = detection;
      const changed = Boolean(reviewedAt && reviewUpdatedAt && reviewedAt !== reviewUpdatedAt);
      if (changed && reviewedAt) {
        list.push({
          key: 'review-first',
          kind: detection.verified,
          at: reviewedAt,
          title: t('detections.detail.log.firstReviewed'),
        });
      }
      list.push({
        key: 'review',
        kind: detection.verified,
        at: detection.reviewUpdatedAt ?? detection.reviewedAt ?? '',
        title: changed
          ? t('detections.detail.log.reviewChanged', { status: statusLabel })
          : t('detections.detail.log.reviewed', { status: statusLabel }),
      });
    }

    if (originalSpeciesName) {
      list.push({
        key: 'species',
        kind: 'species',
        at: detection.reviewUpdatedAt ?? detection.reviewedAt ?? '',
        title: t('detections.detail.log.speciesChanged', {
          from: originalSpeciesName,
          to: speciesName,
        }),
      });
    }

    if (detection.locked) {
      list.push({
        key: 'locked',
        kind: 'locked',
        at: detection.lockedAt ?? '',
        title: t('detections.detail.log.locked'),
      });
    }

    for (const comment of detection.comments ?? []) {
      const edited = comment.updatedAt && comment.updatedAt !== comment.createdAt;
      list.push({
        key: `note-${comment.id}`,
        kind: 'note',
        at: comment.createdAt,
        title: t('detections.detail.log.noteAdded'),
        detail: edited
          ? t('detections.detail.log.noteEdited', { time: wallClockDateTime(comment.updatedAt) })
          : undefined,
        body: comment.entry,
      });
    }

    // Entries without a recorded time keep their place after the timed ones.
    return list.sort((a, b) => {
      if (!a.at && !b.at) return 0;
      if (!a.at) return 1;
      if (!b.at) return -1;
      return Date.parse(a.at) - Date.parse(b.at);
    });
  });

  async function addNote() {
    const text = note.trim();
    if (!text || saving || detection.locked) return;
    saving = true;
    errorMessage = null;
    try {
      await api.post(`/api/v2/detections/${detection.id}/review`, { comment: text });
      note = '';
      toastActions.success(t('detections.detail.log.noteAdded'));
      onChanged();
    } catch (error) {
      logger.error('Failed to add note', error, { action: 'note' });
      errorMessage = t('common.review.errors.commentFailed');
    } finally {
      saving = false;
    }
  }
</script>

<DetailCard title={t('detections.detail.log.title')} subtitle={t('detections.detail.log.subtitle')}>
  <ol class="dd-log">
    {#each entries as entry (entry.key)}
      {@const Icon = ENTRY_ICONS[entry.kind]}
      <li class="dd-log-entry">
        <span class="dd-log-icon dd-log-icon-{entry.kind}" aria-hidden="true">
          <Icon class="size-3.5" />
        </span>
        <div class="dd-log-content">
          <p class="dd-log-time">
            {entry.at ? wallClockDateTime(entry.at) : t('detections.detail.log.timeUnknown')}
          </p>
          <p class="dd-log-title">{entry.title}</p>
          {#if entry.detail}
            <p class="dd-log-detail">{entry.detail}</p>
          {/if}
          {#if entry.body}
            <p class="dd-log-body">{entry.body}</p>
          {/if}
        </div>
      </li>
    {/each}
  </ol>

  {#if canReview}
    <form
      class="dd-note-form"
      onsubmit={event => {
        event.preventDefault();
        void addNote();
      }}
    >
      <label class="dd-note-label" for={noteId}>{t('detections.detail.log.addNote')}</label>
      <textarea
        id={noteId}
        class="dd-note-input focus-ring"
        rows="2"
        bind:value={note}
        placeholder={t('common.review.form.commentPlaceholder')}
        readonly={detection.locked}
        aria-describedby={detection.locked ? noteHelpId : undefined}></textarea>
      {#if detection.locked}
        <p id={noteHelpId} class="dd-note-help">{t('detections.detail.log.lockedHelp')}</p>
      {/if}
      {#if errorMessage}
        <p class="dd-note-error" role="alert">{errorMessage}</p>
      {/if}
      <div class="dd-note-actions">
        <button
          type="submit"
          class="dd-note-submit focus-ring"
          aria-disabled={!note.trim() || saving || detection.locked ? 'true' : undefined}
        >
          {saving ? t('detections.detail.review.saving') : t('detections.detail.log.saveNote')}
        </button>
      </div>
    </form>
  {/if}
</DetailCard>

<style>
  .dd-log {
    position: relative;
    display: flex;
    flex-direction: column;
    gap: 0.875rem;
  }

  .dd-log::before {
    position: absolute;
    top: 0.75rem;
    bottom: 0.75rem;
    left: 0.6875rem;
    width: 1px;
    content: '';
    background-color: var(--border-200);
  }

  .dd-log-entry {
    position: relative;
    display: flex;
    gap: 0.75rem;
    min-width: 0;
  }

  .dd-log-icon {
    position: relative;
    display: inline-flex;
    flex: 0 0 auto;
    align-items: center;
    justify-content: center;
    width: 1.4375rem;
    height: 1.4375rem;
    border: 1px solid var(--border-200);
    border-radius: 50%;
    background-color: var(--color-base-100);
    color: var(--text-subtle);
  }

  .dd-log-icon-correct {
    color: var(--color-success-content);
    border-color: var(--color-success);
    background-color: var(--color-success);
  }

  .dd-log-icon-false_positive {
    color: var(--color-error-content);
    border-color: var(--color-error);
    background-color: var(--color-error);
  }

  .dd-log-content {
    min-width: 0;
  }

  .dd-log-time {
    font-size: 0.6875rem;
    font-variant-numeric: tabular-nums;
    color: var(--text-subtle);
  }

  .dd-log-title {
    font-size: 0.875rem;
  }

  .dd-log-detail {
    overflow: hidden;
    font-size: 0.75rem;
    text-overflow: ellipsis;
    color: var(--text-subtle);
  }

  .dd-log-body {
    margin-top: 0.375rem;
    padding: 0.5rem 0.75rem;
    font-size: 0.875rem;
    line-height: 1.5;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    border-radius: 0.5rem;
    background-color: var(--color-base-200);
  }

  .dd-note-form {
    margin-top: 1rem;
    padding-top: 0.875rem;
    border-top: 1px solid var(--border-100);
  }

  .dd-note-label {
    display: block;
    margin-bottom: 0.375rem;
    font-size: 0.8125rem;
    font-weight: 600;
  }

  .dd-note-input {
    display: block;
    width: 100%;
    padding: 0.5rem 0.75rem;
    font-size: 0.875rem;
    resize: vertical;
    border: 1px solid var(--border-200);
    border-radius: var(--radius-field);
    background-color: var(--color-base-100);
  }

  .dd-note-help {
    margin-top: 0.375rem;
    font-size: 0.75rem;
    color: var(--text-subtle);
  }

  .dd-note-error {
    margin-top: 0.375rem;
    font-size: 0.8125rem;
    color: var(--text-error);
  }

  .dd-note-actions {
    display: flex;
    justify-content: flex-end;
    margin-top: 0.5rem;
  }

  .dd-note-submit {
    padding: 0.4375rem 0.875rem;
    font-size: 0.8125rem;
    font-weight: 600;
    cursor: pointer;
    color: var(--color-primary-content);
    border-radius: var(--radius-field);
    background-color: var(--color-primary);
  }

  .dd-note-submit:hover:not([aria-disabled='true']) {
    background-color: var(--color-primary-hover);
  }

  /* Dimmed by colour, not opacity, so the focus ring keeps its contrast (as ui/Button). */
  .dd-note-submit[aria-disabled='true'] {
    cursor: not-allowed;
    background-color: color-mix(in srgb, var(--color-primary) 50%, transparent);
  }
</style>
