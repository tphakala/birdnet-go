/**
 * Wrap a plain object in a Svelte `$state` proxy so component tests can pass
 * reactive props.
 *
 * Components such as FilterForm/SpeciesFilterForm declare `filters = $bindable()`
 * and `bind:value={filters.x}` on child inputs. In dev mode Svelte injects a
 * runtime reactivity check that emits `binding_property_non_reactive` when the
 * bound object is not a reactive `$state` proxy. The real app always passes a
 * `$state` object through `bind:filters`, so it never warns; tests that render
 * with a plain object do. Wrapping the prop here matches real usage and clears
 * the warning without suppressing the check in production code.
 */
export function reactiveState<T>(initial: T): T {
  const state = $state(initial);
  return state;
}
