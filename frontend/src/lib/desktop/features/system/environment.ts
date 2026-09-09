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
 * constants in internal/sysinfo/environment.go verbatim. This is the frontend
 * counterpart of sysinfo.IsContainerEnv, which switches over the same five.
 *
 * Comparison is case-sensitive because the server sends these constants
 * unchanged. It is prefix-based purely as defensive tolerance: today neither
 * endpoint appends anything (system.go returns the sub-type in a separate
 * `virtualization` field, and the inference endpoint drops it), so an exact
 * match would behave identically on every value the server can currently send.
 */
const CONTAINER_ENVIRONMENTS = ['Docker', 'Podman', 'LXC', 'Container', 'systemd-nspawn'];

/** Whether the reported environment is a container rather than a host install. */
export function isContainerEnvironment(environment: string | undefined): boolean {
  if (!environment) return false;
  return CONTAINER_ENVIRONMENTS.some(kind => environment.startsWith(kind));
}
