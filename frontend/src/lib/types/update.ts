/**
 * Response shape of GET /api/v2/system/update-check.
 *
 * Shared by the sidebar version indicator and the "what's changed" modal so the
 * two stay in sync. All fields except updateAvailable are optional because the
 * backend omits them for dev builds and when the check is unavailable (offline).
 */
export interface UpdateInfo {
  updateAvailable: boolean;
  currentVersion?: string;
  latestVersion?: string;
  latestName?: string;
  releasedAt?: string;
  notes?: string;
  channel?: string;
  releaseURL?: string;
  critical?: boolean;
}
