import { SvelteSet } from 'svelte/reactivity';

export function useSelectionMode(getTotalMatchingCount: () => number) {
  let selectionActive = $state(false);
  let selectedIds = $state(new SvelteSet<string>());
  let allMatchingSelectedFlag = $state(false);
  let lastSelectedId = $state<string | null>(null);

  const selectedCount = $derived(
    // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- allMatchingSelectedFlag is $state(false), ESLint cannot infer rune type
    allMatchingSelectedFlag ? getTotalMatchingCount() : selectedIds.size
  );

  function activate() {
    selectionActive = true;
  }

  function deactivate() {
    selectionActive = false;
    clear();
  }

  function toggle(id: string) {
    allMatchingSelectedFlag = false;
    if (selectedIds.has(id)) {
      selectedIds.delete(id);
    } else {
      selectedIds.add(id);
    }
    lastSelectedId = id;
  }

  function toggleWithShift(id: string, pageIds: string[], shiftKey: boolean) {
    if (!shiftKey || lastSelectedId === null) {
      toggle(id);
      return;
    }

    allMatchingSelectedFlag = false;
    const lastIndex = pageIds.indexOf(lastSelectedId);
    const currentIndex = pageIds.indexOf(id);
    if (lastIndex === -1 || currentIndex === -1) {
      toggle(id);
      return;
    }

    const start = Math.min(lastIndex, currentIndex);
    const end = Math.max(lastIndex, currentIndex);
    for (let i = start; i <= end; i++) {
      // eslint-disable-next-line security/detect-object-injection -- i is a numeric index bounded by array length checks above
      selectedIds.add(pageIds[i]);
    }
    lastSelectedId = id;
  }

  function toggleAllOnPage(pageIds: string[]) {
    const allSelected = pageIds.every(id => selectedIds.has(id));
    allMatchingSelectedFlag = false;
    if (allSelected) {
      for (const id of pageIds) {
        selectedIds.delete(id);
      }
    } else {
      for (const id of pageIds) {
        selectedIds.add(id);
      }
    }
  }

  function selectAllMatching() {
    allMatchingSelectedFlag = true;
  }

  function clear() {
    selectedIds = new SvelteSet<string>();
    allMatchingSelectedFlag = false;
    lastSelectedId = null;
  }

  function isSelected(id: string): boolean {
    return allMatchingSelectedFlag || selectedIds.has(id);
  }

  function allOnPageSelected(pageIds: string[]): boolean {
    if (allMatchingSelectedFlag) return true;
    return pageIds.length > 0 && pageIds.every(id => selectedIds.has(id));
  }

  function someOnPageSelected(pageIds: string[]): boolean {
    if (allMatchingSelectedFlag) return false;
    const count = pageIds.filter(id => selectedIds.has(id)).length;
    return count > 0 && count < pageIds.length;
  }

  function getSelectedIds(): string[] {
    return [...selectedIds];
  }

  return {
    get selectionActive() {
      return selectionActive;
    },
    get selectedCount() {
      return selectedCount;
    },
    get allMatchingSelected() {
      return allMatchingSelectedFlag;
    },
    activate,
    deactivate,
    toggle,
    toggleWithShift,
    toggleAllOnPage,
    selectAllMatching,
    clear,
    isSelected,
    allOnPageSelected,
    someOnPageSelected,
    getSelectedIds,
  };
}
