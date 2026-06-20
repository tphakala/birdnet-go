<script lang="ts">
  import { onMount } from 'svelte';
  import { Pencil, Trash2, Save, X } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { api, ApiError } from '$lib/utils/api';
  import { isAuthenticated } from '$lib/utils/auth';
  import { toastActions } from '$lib/stores/toast';
  import { loggers } from '$lib/utils/logger';
  import { formatDate } from '$lib/utils/formatters';
  import type { SpeciesNoteData } from '$lib/types/species';

  const logger = loggers.ui;

  // Mirrors datastore.SpeciesNoteMaxLength; used for the client-side soft guard.
  const NOTE_MAX_LENGTH = 10_000;
  const HTTP_BAD_REQUEST = 400;

  interface Props {
    scientificName: string;
    className?: string;
    [key: string]: unknown;
  }

  let { scientificName, className = '' }: Props = $props();

  const uid = $props.id();

  let notes = $state<SpeciesNoteData[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);

  let draft = $state('');
  let saving = $state(false);

  let editingId = $state<number | null>(null);
  let editDraft = $state('');
  let confirmingDeleteId = $state<number | null>(null);

  let canSave = $derived(draft.trim().length > 0 && !saving);

  function notesUrl(): string {
    return `/api/v2/species/${encodeURIComponent(scientificName)}/notes`;
  }

  async function load(): Promise<void> {
    loading = true;
    error = null;
    try {
      notes = (await api.get<SpeciesNoteData[]>(notesUrl())) ?? [];
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      logger.error('Failed to load species notes', e, { component: 'SpeciesNotes' });
    } finally {
      loading = false;
    }
  }

  function handleWriteError(e: unknown, fallbackKey: string): void {
    if (e instanceof ApiError && e.status === HTTP_BAD_REQUEST) {
      toastActions.error(t('analytics.species.notes.tooLong', { max: NOTE_MAX_LENGTH }));
    } else {
      toastActions.error(t(fallbackKey));
    }
    logger.error('Species note write failed', e, { component: 'SpeciesNotes' });
  }

  async function addNote(): Promise<void> {
    const entry = draft.trim();
    if (entry.length === 0 || saving) return;
    saving = true;
    try {
      const created = await api.post<SpeciesNoteData>(notesUrl(), { entry });
      notes = [created, ...notes];
      draft = '';
    } catch (e) {
      handleWriteError(e, 'analytics.species.notes.saveFailed');
    } finally {
      saving = false;
    }
  }

  function startEdit(note: SpeciesNoteData): void {
    editingId = note.id;
    editDraft = note.entry;
  }

  function cancelEdit(): void {
    editingId = null;
    editDraft = '';
  }

  async function saveEdit(id: number): Promise<void> {
    const entry = editDraft.trim();
    if (entry.length === 0 || saving) return;
    saving = true;
    try {
      await api.put(`/api/v2/species/notes/${id}`, { entry });
      // Optimistically update the entry text only; the server-authoritative
      // updated_at refreshes on the next load (avoids a UTC toISOString date).
      notes = notes.map(n => (n.id === id ? { ...n, entry } : n));
      cancelEdit();
    } catch (e) {
      handleWriteError(e, 'analytics.species.notes.saveFailed');
    } finally {
      saving = false;
    }
  }

  async function deleteNote(id: number): Promise<void> {
    confirmingDeleteId = null;
    try {
      await api.delete(`/api/v2/species/notes/${id}`);
      notes = notes.filter(n => n.id !== id);
    } catch (e) {
      toastActions.error(t('analytics.species.notes.deleteFailed'));
      logger.error('Failed to delete species note', e, { component: 'SpeciesNotes' });
    }
  }

  onMount(load);
</script>

<section class={`species-notes ${className}`} aria-label={t('analytics.species.notes.title')}>
  <h3 class="text-base font-semibold mb-2">{t('analytics.species.notes.title')}</h3>

  {#if $isAuthenticated}
    <div class="mb-3">
      <label for={`${uid}-draft`} class="sr-only">{t('analytics.species.notes.title')}</label>
      <textarea
        id={`${uid}-draft`}
        class="textarea textarea-bordered w-full text-sm"
        rows="2"
        maxlength={NOTE_MAX_LENGTH}
        placeholder={t('analytics.species.notes.placeholder')}
        bind:value={draft}></textarea>
      <div class="mt-1 flex justify-end">
        <button
          type="button"
          class="btn btn-primary btn-sm"
          disabled={!canSave}
          title={!canSave ? t('analytics.species.notes.saveDisabledReason') : undefined}
          aria-describedby={!canSave ? `${uid}-save-help` : undefined}
          onclick={addNote}
        >
          <Save class="h-4 w-4" />
          {t('analytics.species.notes.save')}
        </button>
      </div>
      {#if !canSave}
        <p id={`${uid}-save-help`} class="sr-only">
          {t('analytics.species.notes.saveDisabledReason')}
        </p>
      {/if}
    </div>
  {/if}

  {#if loading}
    <div role="status" aria-live="polite" class="text-sm text-base-content/70 py-2">
      {t('analytics.species.guide.loading')}
    </div>
  {:else if error}
    <div role="alert" class="p-3 rounded-lg bg-error/10 text-error text-sm">{error}</div>
  {:else if notes.length === 0}
    <p class="text-sm text-base-content/70">{t('analytics.species.notes.empty')}</p>
  {:else}
    <ul class="space-y-2">
      {#each notes as note (note.id)}
        <li class="rounded-md border border-base-300 p-2">
          {#if editingId === note.id}
            <label for={`${uid}-edit-${note.id}`} class="sr-only">
              {t('analytics.species.notes.editLabel')}
            </label>
            <textarea
              id={`${uid}-edit-${note.id}`}
              class="textarea textarea-bordered w-full text-sm"
              rows="2"
              maxlength={NOTE_MAX_LENGTH}
              bind:value={editDraft}></textarea>
            <div class="mt-1 flex justify-end gap-2">
              <button
                type="button"
                class="btn btn-ghost btn-sm"
                onclick={cancelEdit}
                aria-label={t('common.close')}
              >
                <X class="h-4 w-4" />
              </button>
              <button
                type="button"
                class="btn btn-primary btn-sm"
                disabled={editDraft.trim().length === 0 || saving}
                onclick={() => saveEdit(note.id)}
              >
                <Save class="h-4 w-4" />
                {t('analytics.species.notes.save')}
              </button>
            </div>
          {:else}
            <p class="text-sm whitespace-pre-line">{note.entry}</p>
            <div class="mt-1 flex items-center justify-between gap-2">
              <span class="text-xs text-base-content/50">{formatDate(note.updated_at)}</span>
              {#if $isAuthenticated}
                {#if confirmingDeleteId === note.id}
                  <div class="flex items-center gap-2">
                    <span class="text-xs text-base-content/70">
                      {t('analytics.species.notes.deleteConfirm')}
                    </span>
                    <button
                      type="button"
                      class="btn btn-error btn-xs"
                      onclick={() => deleteNote(note.id)}
                    >
                      {t('common.delete')}
                    </button>
                    <button
                      type="button"
                      class="btn btn-ghost btn-xs"
                      onclick={() => (confirmingDeleteId = null)}
                    >
                      {t('common.cancel')}
                    </button>
                  </div>
                {:else}
                  <div class="flex gap-1">
                    <button
                      type="button"
                      class="btn btn-ghost btn-xs"
                      aria-label={t('analytics.species.notes.editLabel')}
                      onclick={() => startEdit(note)}
                    >
                      <Pencil class="h-3.5 w-3.5" />
                    </button>
                    <button
                      type="button"
                      class="btn btn-ghost btn-xs text-error"
                      aria-label={t('analytics.species.notes.deleteConfirm')}
                      onclick={() => (confirmingDeleteId = note.id)}
                    >
                      <Trash2 class="h-3.5 w-3.5" />
                    </button>
                  </div>
                {/if}
              {/if}
            </div>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</section>
