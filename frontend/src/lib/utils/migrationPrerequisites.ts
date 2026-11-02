import type { PrerequisiteCheck } from '$lib/types/migration';

/** True for a critical check that failed or errored, the kind that blocks the migration. */
export function isBlockingCheck(check: PrerequisiteCheck): boolean {
  return check.severity === 'critical' && (check.status === 'failed' || check.status === 'error');
}
