/**
 * useDetectionActions.svelte.ts
 *
 * Shared composable for detection card action handlers (review, delete, lock, ignore species).
 * Used by DetectionCardGrid (dashboard), DetectionsCardView (detections card view), and
 * DetectionsList (table rows + mobile cards) to eliminate duplicated modal + API logic.
 * Exclusion state is delegated to the shared excludedSpecies store via the
 * isSpeciesExcluded / onToggleExclusion options.
 *
 * Usage:
 *   const actions = useDetectionActions({ onRefresh, isSpeciesExcluded, onToggleExclusion });
 *   // Use actions.handleReview, actions.handleDelete, etc.
 *   // Bind actions.showConfirmModal, actions.selectedDetection, actions.confirmModalConfig to ConfirmModal
 */

import type { Detection } from '$lib/types/detection.types';
import { toastActions } from '$lib/stores/toast';
import { fetchWithCSRF } from '$lib/utils/api';
import { t } from '$lib/i18n';
import { loggers } from '$lib/utils/logger';
import { navigation } from '$lib/stores/navigation.svelte';
import { setDetectionVerification } from '$lib/utils/reviewDetection';

const logger = loggers.ui;

interface DetectionActionOptions {
  onRefresh?: () => void;
  /** Check whether a species is currently excluded */
  isSpeciesExcluded: (_commonName: string) => boolean;
  /** Called after successful API toggle to update local exclusion state */
  onToggleExclusion: (_commonName: string, _exclude: boolean) => void;
}

/** Response shape of POST /api/v2/detections/ignore (IgnoreSpeciesResponse). */
interface IgnoreSpeciesResponse {
  common_name: string;
  action: string;
  is_excluded: boolean;
}

export function useDetectionActions(options: DetectionActionOptions) {
  let showConfirmModal = $state(false);
  let selectedDetection = $state<Detection | null>(null);
  let confirmModalConfig = $state({
    title: '',
    message: '',
    confirmLabel: 'Confirm',
    onConfirm: async () => {},
  });

  function handleReview(detection: Detection) {
    navigation.navigate(`/ui/detections/${detection.id}?tab=review`);
  }

  function handleToggleSpecies(detection: Detection) {
    // Snapshot at modal-open so the awaited confirm uses a stable value even if
    // the detection prop is swapped by a background refresh.
    const commonName = detection.commonName;
    const wasExcluded = options.isSpeciesExcluded(commonName);
    confirmModalConfig = {
      title: wasExcluded
        ? t('dashboard.recentDetections.modals.showSpecies', { species: commonName })
        : t('dashboard.recentDetections.modals.ignoreSpecies', { species: commonName }),
      message: wasExcluded
        ? t('dashboard.recentDetections.modals.showSpeciesConfirm', { species: commonName })
        : t('dashboard.recentDetections.modals.ignoreSpeciesConfirm', { species: commonName }),
      confirmLabel: t('common.buttons.confirm'),
      onConfirm: async () => {
        try {
          const resp = await fetchWithCSRF<IgnoreSpeciesResponse | null>(
            '/api/v2/detections/ignore',
            {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ common_name: commonName }),
            }
          );
          // Trust the server's authoritative new state, not an optimistic
          // negation (the endpoint is a blind toggle that returns the result).
          // Guard against an empty body so a successful toggle still refreshes
          // the list rather than surfacing a misleading error toast.
          if (resp) {
            options.onToggleExclusion(resp.common_name, resp.is_excluded);
          }
          options.onRefresh?.();
        } catch (err) {
          toastActions.error(t('dashboard.recentDetections.errors.toggleSpeciesFailed'));
          logger.error('Error toggling species exclusion:', err);
        }
      },
    };
    selectedDetection = detection;
    showConfirmModal = true;
  }

  function handleToggleLock(detection: Detection) {
    const detectionId = detection.id;
    const commonName = detection.commonName;
    const wasLocked = detection.locked;
    confirmModalConfig = {
      title: wasLocked
        ? t('dashboard.recentDetections.modals.unlockDetection')
        : t('dashboard.recentDetections.modals.lockDetection'),
      message: wasLocked
        ? t('dashboard.recentDetections.modals.unlockDetectionConfirm', { species: commonName })
        : t('dashboard.recentDetections.modals.lockDetectionConfirm', { species: commonName }),
      confirmLabel: t('common.buttons.confirm'),
      onConfirm: async () => {
        try {
          await fetchWithCSRF(`/api/v2/detections/${detectionId}/lock`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ locked: !wasLocked }),
          });
          options.onRefresh?.();
        } catch (err) {
          toastActions.error(t('dashboard.recentDetections.errors.toggleLockFailed'));
          logger.error('Error toggling lock status:', err);
        }
      },
    };
    selectedDetection = detection;
    showConfirmModal = true;
  }

  function handleDelete(detection: Detection) {
    const detectionId = detection.id;
    const commonName = detection.commonName;
    confirmModalConfig = {
      title: t('dashboard.recentDetections.modals.deleteDetection', { species: commonName }),
      message: t('dashboard.recentDetections.modals.deleteDetectionConfirm', {
        species: commonName,
      }),
      confirmLabel: t('common.buttons.delete'),
      onConfirm: async () => {
        try {
          await fetchWithCSRF(`/api/v2/detections/${detectionId}`, {
            method: 'DELETE',
          });
          options.onRefresh?.();
        } catch (err) {
          toastActions.error(t('dashboard.recentDetections.errors.deleteFailed'));
          logger.error('Error deleting detection:', err);
        }
      },
    };
    selectedDetection = detection;
    showConfirmModal = true;
  }

  async function handleMarkCorrect(detection: Detection) {
    if (await setDetectionVerification(detection.id, 'correct')) {
      detection.verified = 'correct';
      options.onRefresh?.();
    }
  }

  async function handleMarkFalsePositive(detection: Detection) {
    if (await setDetectionVerification(detection.id, 'false_positive')) {
      detection.verified = 'false_positive';
      options.onRefresh?.();
    }
  }

  function closeModal() {
    showConfirmModal = false;
    selectedDetection = null;
  }

  async function confirmModal() {
    try {
      await confirmModalConfig.onConfirm();
    } finally {
      closeModal();
    }
  }

  return {
    get showConfirmModal() {
      return showConfirmModal;
    },
    get selectedDetection() {
      return selectedDetection;
    },
    get confirmModalConfig() {
      return confirmModalConfig;
    },
    handleReview,
    handleMarkCorrect,
    handleMarkFalsePositive,
    handleToggleSpecies,
    handleToggleLock,
    handleDelete,
    closeModal,
    confirmModal,
  };
}
