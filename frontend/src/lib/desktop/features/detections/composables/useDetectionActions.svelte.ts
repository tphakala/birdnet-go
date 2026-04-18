/**
 * useDetectionActions.svelte.ts
 *
 * Shared composable for detection card action handlers (review, delete, lock, ignore species).
 * Used by both DetectionCardGrid (dashboard) and DetectionsCardView (detections listing)
 * to eliminate duplicated modal + API logic.
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
    const isExcluded = options.isSpeciesExcluded(detection.commonName);
    confirmModalConfig = {
      title: isExcluded
        ? t('dashboard.recentDetections.modals.showSpecies', { species: detection.commonName })
        : t('dashboard.recentDetections.modals.ignoreSpecies', { species: detection.commonName }),
      message: isExcluded
        ? t('dashboard.recentDetections.modals.showSpeciesConfirm', {
            species: detection.commonName,
          })
        : t('dashboard.recentDetections.modals.ignoreSpeciesConfirm', {
            species: detection.commonName,
          }),
      confirmLabel: t('common.buttons.confirm'),
      onConfirm: async () => {
        try {
          await fetchWithCSRF('/api/v2/detections/ignore', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ common_name: detection.commonName }),
          });
          options.onToggleExclusion(detection.commonName, !isExcluded);
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
    confirmModalConfig = {
      title: detection.locked
        ? t('dashboard.recentDetections.modals.unlockDetection')
        : t('dashboard.recentDetections.modals.lockDetection'),
      message: detection.locked
        ? t('dashboard.recentDetections.modals.unlockDetectionConfirm', {
            species: detection.commonName,
          })
        : t('dashboard.recentDetections.modals.lockDetectionConfirm', {
            species: detection.commonName,
          }),
      confirmLabel: t('common.buttons.confirm'),
      onConfirm: async () => {
        try {
          await fetchWithCSRF(`/api/v2/detections/${detection.id}/lock`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ locked: !detection.locked }),
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
    confirmModalConfig = {
      title: t('dashboard.recentDetections.modals.deleteDetection', {
        species: detection.commonName,
      }),
      message: t('dashboard.recentDetections.modals.deleteDetectionConfirm', {
        species: detection.commonName,
      }),
      confirmLabel: t('common.buttons.delete'),
      onConfirm: async () => {
        try {
          await fetchWithCSRF(`/api/v2/detections/${detection.id}`, {
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
