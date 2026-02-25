<script lang="ts">
  import { Database, MapPin, HardDrive, FileText, Clock, Shield } from '@lucide/svelte';
  import { formatBytesCompact, formatNumber } from '$lib/utils/formatters';
  import { t } from '$lib/i18n';
  import type { DatabaseStats } from '$lib/types/migration';

  interface Props {
    stats: DatabaseStats | null;
    lastBackupDate?: string;
    integrityStatus?: string;
  }

  let { stats, lastBackupDate, integrityStatus = 'OK' }: Props = $props();
</script>

<div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-4 shadow-sm">
  <h3
    class="text-xs font-semibold uppercase tracking-wider mb-3 text-slate-400 dark:text-slate-500"
  >
    {t('system.database.migration.details.title')}
  </h3>
  <div class="space-y-2.5">
    <div class="flex items-center gap-3">
      <Database class="w-3.5 h-3.5 flex-shrink-0 text-slate-400 dark:text-slate-500" />
      <span class="text-sm">{stats ? stats.type.toUpperCase() : 'SQLite'}</span>
    </div>
    <div class="flex items-center gap-3">
      <MapPin class="w-3.5 h-3.5 flex-shrink-0 text-slate-400 dark:text-slate-500" />
      <span class="text-sm font-mono truncate" title={stats?.location ?? ''}
        >{stats?.location ?? '—'}</span
      >
    </div>
    <div class="flex items-center gap-3">
      <HardDrive class="w-3.5 h-3.5 flex-shrink-0 text-slate-400 dark:text-slate-500" />
      <span class="text-sm tabular-nums">{stats ? formatBytesCompact(stats.size_bytes) : '—'}</span>
    </div>
    <div class="flex items-center gap-3">
      <FileText class="w-3.5 h-3.5 flex-shrink-0 text-slate-400 dark:text-slate-500" />
      <span class="text-sm tabular-nums"
        >{stats
          ? t('system.database.migration.details.detections', {
              count: formatNumber(stats.total_detections),
            })
          : '—'}</span
      >
    </div>
    <div class="flex items-center gap-3">
      <Clock class="w-3.5 h-3.5 flex-shrink-0 text-slate-400 dark:text-slate-500" />
      <span class="text-sm"
        >{lastBackupDate
          ? t('system.database.migration.details.lastBackup', { date: lastBackupDate })
          : t('system.database.migration.details.noBackups')}</span
      >
    </div>
    <div class="flex items-center gap-3">
      <Shield class="w-3.5 h-3.5 flex-shrink-0 text-slate-400 dark:text-slate-500" />
      <span class="text-sm"
        >{t('system.database.migration.details.integrity', { status: integrityStatus })}</span
      >
    </div>
  </div>
</div>
