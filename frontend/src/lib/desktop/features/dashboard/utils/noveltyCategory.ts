// noveltyCategory.ts - Shared novelty-category taxonomy for dashboard cards.
//
// Single source of truth for the "new species / new this year / new this season"
// indicators shared by DailySummaryCard and NewSpeciesHighlightsCard: the
// precedence used to pick the winning category and the accent color for each.
// Each card keeps its own icon and label, but the category logic and colors live
// here so they cannot drift apart.

/** Novelty categories in precedence order: lifetime > year > season. */
export type NoveltyCategory = 'lifetime' | 'year' | 'season';

/** The novelty flags a daily-summary row carries. */
export interface NoveltyFlags {
  is_new_species?: boolean;
  is_new_this_year?: boolean;
  is_new_this_season?: boolean;
}

/**
 * Resolves the highest-precedence novelty category for a row, or null when the
 * species is not new in any tracked period.
 */
export function resolveNoveltyCategory(item: NoveltyFlags): NoveltyCategory | null {
  if (item.is_new_species) return 'lifetime';
  if (item.is_new_this_year) return 'year';
  if (item.is_new_this_season) return 'season';
  return null;
}

/** CSS color variable for a category's accent (left border / icon tint). */
export function noveltyCategoryColorVar(category: NoveltyCategory): string {
  switch (category) {
    case 'lifetime':
      return 'var(--color-warning)';
    case 'year':
      return 'var(--color-info)';
    case 'season':
      return 'var(--color-success)';
  }
}
