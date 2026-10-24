import { vi } from 'vitest';
import type { ComponentProps } from 'svelte';
import LocationMap from '$lib/desktop/components/forms/LocationMap.svelte';

/**
 * Props of the last rendered LocationMap. The test file must automock the map with
 * vi.mock('$lib/desktop/components/forms/LocationMap.svelte'); the props object is
 * live, so later reads see updated values.
 */
export function latestMapProps(): ComponentProps<typeof LocationMap> {
  const call = vi.mocked(LocationMap).mock.calls.at(-1);
  const props = call?.[1];
  // The lint type checker types a mocked component's call as a one-element tuple,
  // so it cannot see that the props argument can be missing.
  // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition
  if (!props) throw new Error('LocationMap was not rendered');
  return props;
}
