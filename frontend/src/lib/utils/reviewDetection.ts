import { fetchWithCSRF } from './api';
import { t } from '$lib/i18n';
import { toastActions } from '$lib/stores/toast';
import { loggers } from './logger';
import type { Detection } from '$lib/types/detection.types';

const logger = loggers.ui;

/**
 * POST the given verification status to the review endpoint, update the local
 * detection object on success, and fire a success/error toast.
 *
 * Returns `true` on success, `false` on failure.
 */
export async function setDetectionVerification(
  detection: Detection,
  verified: 'correct' | 'false_positive'
): Promise<boolean> {
  try {
    await fetchWithCSRF(`/api/v2/detections/${detection.id}/review`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ verified }),
    });
    detection.verified = verified;
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
