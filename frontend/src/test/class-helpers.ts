/**
 * Class-list assertions shared by component tests.
 */

/**
 * Lists the classes on an element that set opacity, with or without a state variant
 * (`opacity-50`, `aria-disabled:opacity-50`). Classes under `ignoreVariant` are left out, for
 * example `disabled`, whose element the browser never focuses and so draws no ring for.
 */
export function opacityClasses(
  element: Element | null | undefined,
  { ignoreVariant }: { ignoreVariant?: string } = {}
): string[] {
  return [...(element?.classList ?? [])].filter(
    c => /(^|:)opacity-/.test(c) && !(ignoreVariant && c.startsWith(`${ignoreVariant}:`))
  );
}
