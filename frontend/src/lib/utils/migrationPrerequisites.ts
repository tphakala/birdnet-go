import { t } from '$lib/i18n';
import type { PrerequisiteCheck, PrerequisitesResponse } from '$lib/types/migration';

/** True for a critical check that failed or errored, the kind that blocks the migration. */
export function isBlockingCheck(check: PrerequisiteCheck): boolean {
  return check.severity === 'critical' && (check.status === 'failed' || check.status === 'error');
}

/** Milliseconds in a second, for the elapsed time the server reports in milliseconds. */
const MS_PER_SECOND = 1000;

/** Whole seconds in a server-reported duration in milliseconds. */
export function elapsedSeconds(ms: number | undefined): number {
  return Math.floor((ms ?? 0) / MS_PER_SECOND);
}

/** The message for a run that did not finish (time limit, shutdown, internal error), or null. */
export function failedRunMessage(prerequisites: PrerequisitesResponse | null): string | null {
  if (prerequisites?.status !== 'failed') return null;
  return t('system.database.migration.prerequisites.runFailed', {
    error: prerequisites.error ?? '',
  });
}
