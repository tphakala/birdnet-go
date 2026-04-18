import { fetchWithCSRF } from './api';
import { t } from '$lib/i18n';
import { toastActions } from '$lib/stores/toast';
import { loggers } from './logger';

const logger = loggers.ui;

/**
 * POST the given verification status to the review endpoint and fire a toast
 * with the outcome. Returns `true` on success, `false` on failure.
 *
 * Callers are responsible for updating their local detection state and
 * triggering any refetch when `true` is returned.
 */
export async function setDetectionVerification(
  detectionId: number,
  verified: 'correct' | 'false_positive'
): Promise<boolean> {
  try {
    await fetchWithCSRF(`/api/v2/detections/${detectionId}/review`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ verified }),
    });
    toastActions.success(
      verified === 'correct'
        ? t('search.review.markedCorrect')
        : t('search.review.markedFalsePositive')
    );
    return true;
  } catch (err) {
    toastActions.error(t('search.review.failed'));
    logger.error('Error setting verification status:', err);
    return false;
  }
}
