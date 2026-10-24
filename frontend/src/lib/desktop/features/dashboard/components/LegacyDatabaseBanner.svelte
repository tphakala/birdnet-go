<!--
  LegacyDatabaseBanner - Dashboard banner shown while the install still runs
  the legacy database schema, prompting the user to run the migration.

  Reads the legacy notice kind from the migration status endpoint once on
  every mount (no polling) and renders for the four kinds that need a notice:
  - not_started, in_progress and restart_required: a warning status.
  - needs_attention (migration paused or failed): an error alert.
  Each links to System > Database. "none", unknown kinds, a missing field and
  a failed fetch (503 on installs without migration support, network errors)
  render nothing. Guests never fetch: the endpoint needs authentication. For a
  signed-in user whose session expired, the shared API client redirects to the
  login page on a 401.

  Dismissal lasts for the browser session (sessionStorage) and is remembered
  per kind: the banner stays hidden while the fetched kind equals the
  dismissed one and returns when the kind changes (for example a dismissed
  not_started warning followed by a failed run), like the bell notice. It
  returns in the next session until the migration completes. Dismissing from
  the keyboard returns focus to the control it came from.

  @component
-->
<script lang="ts">
  import { untrack } from 'svelte';
  import { TriangleAlert, CircleX, Info, X } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import { api } from '$lib/utils/api';
  import { createFocusReturn } from '$lib/utils/focusReturn';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { SYSTEM_ROUTES } from '$lib/utils/settingsRoutes';
  import { handleAppLinkClick } from '$lib/stores/navigation.svelte';
  import { isGuestMode } from '$lib/stores/appState.svelte';
  import type { LegacyNoticeKind, MigrationStatus } from '$lib/types/migration';

  interface Props {
    class?: string;
  }

  let { class: className = '' }: Props = $props();

  const MIGRATION_STATUS_ENDPOINT = '/api/v2/system/database/migration/status';
  const DISMISS_KEY = 'birdnet.legacyDbBannerDismissed';

  interface Variant {
    tone: 'warning' | 'error';
    icon: typeof TriangleAlert;
    titleKey: string;
    messageKey: string;
  }

  const VARIANTS = new Map<LegacyNoticeKind, Variant>([
    [
      'not_started',
      {
        tone: 'warning',
        icon: TriangleAlert,
        titleKey: 'notifications.content.migration.legacyNotStartedTitle',
        messageKey: 'notifications.content.migration.legacyNotStartedMessage',
      },
    ],
    [
      'in_progress',
      {
        tone: 'warning',
        icon: Info,
        titleKey: 'notifications.content.migration.legacyInProgressTitle',
        messageKey: 'notifications.content.migration.legacyInProgressMessage',
      },
    ],
    [
      'needs_attention',
      {
        tone: 'error',
        icon: CircleX,
        titleKey: 'notifications.content.migration.legacyNeedsAttentionTitle',
        messageKey: 'notifications.content.migration.legacyNeedsAttentionMessage',
      },
    ],
    [
      'restart_required',
      {
        tone: 'warning',
        icon: TriangleAlert,
        titleKey: 'notifications.content.migration.legacyRestartRequiredTitle',
        messageKey: 'notifications.content.migration.legacyRestartRequiredMessage',
      },
    ],
  ]);

  // Session-scoped dismissal of one notice kind, guarded so a private-window or
  // blocked sessionStorage never throws (see frontend/AGENTS.md). A value that
  // is not a kind, such as the flag an earlier build stored, never matches.
  function readDismissedKind(): string | null {
    try {
      return sessionStorage.getItem(DISMISS_KEY);
    } catch {
      return null;
    }
  }

  let dismissedKind = $state(readDismissedKind());
  let kind = $state<LegacyNoticeKind | undefined>();
  // $state.raw: the variant holds an icon component, which must not be proxied.
  let variant = $state.raw<Variant | undefined>();
  let isError = $derived(variant?.tone === 'error');

  const { track: trackFocus, restore: restoreFocus } = createFocusReturn();

  function dismiss(event: MouseEvent) {
    // Before the banner is hidden: dismissing removes the focused button.
    restoreFocus(event);
    if (!kind) return;
    dismissedKind = kind;
    try {
      sessionStorage.setItem(DISMISS_KEY, kind);
    } catch {
      /* sessionStorage unavailable: dismissal is in-memory only for this mount */
    }
  }

  $effect(() => {
    if (isGuestMode()) return;
    let active = true;
    // untrack: api.get reads the CSRF token synchronously, and a token refresh
    // elsewhere must not re-run this effect and fetch the status again.
    untrack(() => api.get<MigrationStatus>(MIGRATION_STATUS_ENDPOINT))
      .then(status => {
        // Unknown kinds and 'none' have no variant, so nothing renders.
        if (!active) return;
        const next = status.legacy_notice ?? 'none';
        variant = VARIANTS.get(next);
        kind = variant ? next : undefined;
      })
      .catch(() => {
        /* No banner when the status is unavailable (503, network, a suppressed 401 redirect). */
      });
    return () => {
      active = false;
    };
  });
</script>

{#if variant && kind !== dismissedKind}
  {@const Icon = variant.icon}
  <div
    role={isError ? 'alert' : 'status'}
    use:trackFocus
    class={cn(
      'flex flex-col gap-3 rounded-2xl border p-4 sm:flex-row sm:items-center',
      isError
        ? 'border-[color-mix(in_srgb,var(--color-error)_40%,transparent)] bg-[color-mix(in_srgb,var(--color-error)_10%,var(--color-base-100))]'
        : 'border-[color-mix(in_srgb,var(--color-warning)_40%,transparent)] bg-[color-mix(in_srgb,var(--color-warning)_10%,var(--color-base-100))]',
      className
    )}
  >
    <Icon
      class={cn(
        'size-6 shrink-0',
        isError ? 'text-[var(--color-error)]' : 'text-[var(--color-warning)]'
      )}
      aria-hidden="true"
    />
    <div class="min-w-0 flex-1">
      <p class="font-semibold text-[var(--color-base-content)]">
        {t(variant.titleKey)}
      </p>
      <p class="text-sm text-[var(--color-base-content)]/80">
        {t(variant.messageKey)}
      </p>
    </div>
    <a
      href={buildAppUrl(SYSTEM_ROUTES.database)}
      onclick={handleAppLinkClick}
      class={cn(
        'btn btn-sm shrink-0 self-start sm:self-auto',
        isError ? 'btn-error' : 'btn-warning'
      )}
    >
      {t('dashboard.legacyDatabase.action')}
    </a>
    <button
      type="button"
      class="inline-flex shrink-0 items-center justify-center self-start rounded-md bg-transparent p-1.5 text-[var(--color-base-content)] transition-colors hover:bg-black/5 sm:self-auto dark:hover:bg-white/5"
      onclick={dismiss}
      aria-label={t('common.aria.dismissAlert')}
    >
      <X class="size-4" aria-hidden="true" />
    </button>
  </div>
{/if}
