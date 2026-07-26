/**
 * Shared classification of the runtime environment string the server reports.
 *
 * The values come from sysinfo.GetEnvironment (internal/sysinfo/environment.go)
 * and reach the UI through two unrelated endpoints: /api/v2/system/info feeds
 * the Overview card, /api/v2/system/inference feeds the AI Models page. Both
 * pick an icon from the same string, so the predicate lives here rather than
 * being copied into each, which is how the two would drift apart the next time
 * the server learns a new container runtime.
 */

/**
 * Environment strings that denote a container runtime, matching the Env*
 * constants the server sends verbatim. Comparison is case-sensitive and
 * prefix-based because the server may append a detail suffix.
 */
const CONTAINER_ENVIRONMENTS = ['Docker', 'Podman', 'LXC', 'Container', 'systemd-nspawn'];

/** Whether the reported environment is a container rather than a host install. */
export function isContainerEnvironment(environment: string | undefined): boolean {
  if (!environment) return false;
  return CONTAINER_ENVIRONMENTS.some(kind => environment.startsWith(kind));
}
