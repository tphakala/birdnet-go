// noveltyCategory.ts - Shared novelty-category taxonomy for dashboard cards.
//
// Single source of truth for the "new species / new this year / new this season
// / infrequent" indicators shared by DailySummaryCard and NewSpeciesHighlightsCard:
// the precedence used to pick the winning category and the accent color for each.
// Each card keeps its own icon and label, but the category logic and colors live
// here so they cannot drift apart.

/** Novelty categories in precedence order: lifetime > year > season > infrequent. */
export type NoveltyCategory = 'lifetime' | 'year' | 'season' | 'infrequent';

/** The novelty flags a daily-summary row carries. */
export interface NoveltyFlags {
  is_new_species?: boolean;
  is_new_this_year?: boolean;
  is_new_this_season?: boolean;
  /** Days since the previous detection before a return; drives the infrequent tier. */
  days_since_last_seen?: number;
}

/** Options controlling the infrequent fallback tier. */
export interface NoveltyResolveOptions {
  /**
   * Absence threshold in days above which a returning species counts as
   * "infrequent". Omitted (undefined) when infrequent tracking is disabled, so
   * the tier never activates.
   */
  infrequentThresholdDays?: number;
  /**
   * Infrequent reflects live tracker state, so it only applies to today's view.
   * Past dates never surface the infrequent tier.
   */
  isToday?: boolean;
}

/**
 * Resolves the highest-precedence novelty category for a row, or null when the
 * species is not new in any tracked period. Infrequent is a today-only fallback
 * tier: it only claims rows not already categorized as lifetime/year/season.
 */
export function resolveNoveltyCategory(
  item: NoveltyFlags,
  options: NoveltyResolveOptions = {}
): NoveltyCategory | null {
  if (item.is_new_species) return 'lifetime';
  if (item.is_new_this_year) return 'year';
  if (item.is_new_this_season) return 'season';

  // Infrequent fallback: only on today's view, only when tracking supplies a
  // threshold, and only for a genuine absence gap. The `?? 0` guard handles API
  // omission of days_since_last_seen for first-ever and same-day returns.
  const { infrequentThresholdDays, isToday } = options;
  if (
    isToday === true &&
    infrequentThresholdDays !== undefined &&
    (item.days_since_last_seen ?? 0) > infrequentThresholdDays
  ) {
    return 'infrequent';
  }

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
    case 'infrequent':
      return 'var(--color-secondary)';
  }
}
