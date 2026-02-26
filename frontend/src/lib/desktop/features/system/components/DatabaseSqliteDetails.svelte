<script lang="ts">
  import { t } from '$lib/i18n';
  import { Database, FileText, HardDrive, Shield, Clock } from '@lucide/svelte';
  import { formatBytesCompact, formatDateTime } from '$lib/utils/formatters';
  import type { SQLiteDetails } from '$lib/types/database';

  interface Props {
    details: SQLiteDetails;
  }

  let { details }: Props = $props();
</script>

<!-- Database Details -->
<div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm">
  <h3
    class="text-xs font-semibold uppercase tracking-wider mb-3 text-slate-400 dark:text-slate-500"
  >
    {t('system.database.dashboard.details.title')}
  </h3>
  <div class="space-y-2.5">
    <div class="flex items-center gap-3">
      <Database class="w-3.5 h-3.5 flex-shrink-0 text-slate-400 dark:text-slate-500" />
      <div class="flex justify-between flex-1 text-sm">
        <span class="text-slate-400 dark:text-slate-500"
          >{t('system.database.dashboard.details.engine')}</span
        >
        <span class="font-medium">{details.engine_version}</span>
      </div>
    </div>
    <div class="flex items-center gap-3">
      <FileText class="w-3.5 h-3.5 flex-shrink-0 text-slate-400 dark:text-slate-500" />
      <div class="flex justify-between flex-1 text-sm">
        <span class="text-slate-400 dark:text-slate-500"
          >{t('system.database.dashboard.details.journal')}</span
        >
        <span class="font-medium uppercase">{details.journal_mode}</span>
      </div>
    </div>
    <div class="flex items-center gap-3">
      <HardDrive class="w-3.5 h-3.5 flex-shrink-0 text-slate-400 dark:text-slate-500" />
      <div class="flex justify-between flex-1 text-sm">
        <span class="text-slate-400 dark:text-slate-500"
          >{t('system.database.dashboard.details.pageSize')}</span
        >
        <span class="font-mono tabular-nums font-medium"
          >{formatBytesCompact(details.page_size)}</span
        >
      </div>
    </div>
    <div class="flex items-center gap-3">
      <Shield class="w-3.5 h-3.5 flex-shrink-0 text-slate-400 dark:text-slate-500" />
      <div class="flex justify-between flex-1 text-sm">
        <span class="text-slate-400 dark:text-slate-500"
          >{t('system.database.dashboard.details.integrity')}</span
        >
        <span
          class="font-medium {details.integrity_check === 'ok'
            ? 'text-emerald-600 dark:text-emerald-400'
            : 'text-red-600 dark:text-red-400'}">{details.integrity_check.toUpperCase()}</span
        >
      </div>
    </div>
    <div class="flex items-center gap-3">
      <Clock class="w-3.5 h-3.5 flex-shrink-0 text-slate-400 dark:text-slate-500" />
      <div class="flex justify-between flex-1 text-sm">
        <span class="text-slate-400 dark:text-slate-500"
          >{t('system.database.dashboard.details.lastVacuum')}</span
        >
        <span class="text-xs font-medium"
          >{details.last_vacuum_at ? formatDateTime(details.last_vacuum_at) : '—'}</span
        >
      </div>
    </div>
  </div>
</div>
