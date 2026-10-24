<!--
  LegacyDatabaseBanner - Dashboard banner shown while the install still runs
  the legacy database schema, prompting the user to run the migration.

  Reads the legacy notice kind from the migration status endpoint once on
  mount (no polling) and renders for the four kinds that need a notice:
  - not_started, in_progress and restart_required: a warning status.
  - needs_attention (migration paused or failed): an error alert.
  Each links to System > Database. "none", unknown kinds and a failed fetch
  (401, 503 on installs without migration support) render nothing. Guests
  never fetch: the endpoint needs authentication.

  Dismissal lasts for the browser session (sessionStorage), so the banner
  returns in the next session until the migration completes.

  @component
-->
<script lang="ts">
  import { TriangleAlert, CircleX, Info, X } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import { api } from '$lib/utils/api';
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
  const DISMISSED_VALUE = '1';

  type BannerKind = Exclude<LegacyNoticeKind, 'none'>;

  interface Variant {
    tone: 'warning' | 'error';
    role: 'status' | 'alert';
    icon: typeof TriangleAlert;
    titleKey: string;
    messageKey: string;
  }

  const VARIANTS = new Map<BannerKind, Variant>([
    [
      'not_started',
      {
        tone: 'warning',
        role: 'status',
        icon: TriangleAlert,
        titleKey: 'notifications.content.migration.legacyNotStartedTitle',
        messageKey: 'notifications.content.migration.legacyNotStartedMessage',
      },
    ],
    [
      'in_progress',
      {
        tone: 'warning',
        role: 'status',
        icon: Info,
        titleKey: 'notifications.content.migration.legacyInProgressTitle',
        messageKey: 'notifications.content.migration.legacyInProgressMessage',
      },
    ],
    [
      'needs_attention',
      {
        tone: 'error',
        role: 'alert',
        icon: CircleX,
        titleKey: 'notifications.content.migration.legacyNeedsAttentionTitle',
        messageKey: 'notifications.content.migration.legacyNeedsAttentionMessage',
      },
    ],
    [
      'restart_required',
      {
        tone: 'warning',
        role: 'status',
        icon: TriangleAlert,
        titleKey: 'notifications.content.migration.legacyRestartRequiredTitle',
        messageKey: 'notifications.content.migration.legacyRestartRequiredMessage',
      },
    ],
  ]);

  function isBannerKind(value: unknown): value is BannerKind {
    return [...VARIANTS.keys()].some(k => k === value);
  }

  // Session-scoped dismissal, guarded so a private-window or blocked
  // sessionStorage never throws (see frontend/AGENTS.md).
  function readDismissed(): boolean {
    try {
      return sessionStorage.getItem(DISMISS_KEY) === DISMISSED_VALUE;
    } catch {
      return false;
    }
  }

  let dismissed = $state(readDismissed());
  let kind = $state<BannerKind | null>(null);
  let variant = $derived(kind === null || dismissed ? undefined : VARIANTS.get(kind));

  function dismiss() {
    dismissed = true;
    try {
      sessionStorage.setItem(DISMISS_KEY, DISMISSED_VALUE);
    } catch {
      /* sessionStorage unavailable: dismissal is in-memory only for this mount */
    }
  }

  $effect(() => {
    if (dismissed || isGuestMode()) return;
    let active = true;
    api
      .get<MigrationStatus>(MIGRATION_STATUS_ENDPOINT)
      .then(status => {
        if (active && isBannerKind(status.legacy_notice)) kind = status.legacy_notice;
      })
      .catch(() => {
        /* No banner when the status is unavailable (401, 503, network). */
      });
    return () => {
      active = false;
    };
  });
</script>

{#if variant}
  {@const Icon = variant.icon}
  <div
    role={variant.role}
    class={cn(
      'flex flex-col gap-3 rounded-2xl border p-4 sm:flex-row sm:items-center',
      variant.tone === 'error'
        ? 'border-[color-mix(in_srgb,var(--color-error)_40%,transparent)] bg-[color-mix(in_srgb,var(--color-error)_10%,var(--color-base-100))]'
        : 'border-[color-mix(in_srgb,var(--color-warning)_40%,transparent)] bg-[color-mix(in_srgb,var(--color-warning)_10%,var(--color-base-100))]',
      className
    )}
  >
    <Icon
      class={cn(
        'size-6 shrink-0',
        variant.tone === 'error' ? 'text-[var(--color-error)]' : 'text-[var(--color-warning)]'
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
        variant.tone === 'error' ? 'btn-error' : 'btn-warning'
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
