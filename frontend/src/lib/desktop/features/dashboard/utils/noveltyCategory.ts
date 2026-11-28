// noveltyCategory.ts - Shared novelty-category taxonomy for dashboard cards.
//
// Single source of truth for the "new species / new this year / new this season
// / infrequent" indicators shared by DailySummaryCard and NewSpeciesHighlightsCard:
// the precedence used to pick the winning category and the accent color for each.
// Each card keeps its own icon and label, but the category logic and colors live
// here so they cannot drift apart.

import { textColorFor } from '$lib/utils/statusTextColor';

/** Novelty categories in precedence order: lifetime > year > season > infrequent. */
export type NoveltyCategory = 'lifetime' | 'year' | 'season' | 'infrequent';

/** The novelty flags a daily-summary row carries. */
export interface NoveltyFlags {
  is_new_species?: boolean;
  is_new_this_year?: boolean;
  is_new_this_season?: boolean;
  /**
   * Set by the server when a returning species' absence gap exceeds the
   * configured infrequent-tracking threshold. Decided server-side so guests,
   * who cannot read the protected settings, get the same tier as logged-in users.
   */
  is_infrequent?: boolean;
}

/** Options controlling the infrequent fallback tier. */
export interface NoveltyResolveOptions {
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

  // Infrequent fallback: only on today's view. The server sets is_infrequent only
  // when infrequent tracking is enabled and the absence gap exceeds its threshold.
  if (options.isToday === true && item.is_infrequent === true) {
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

/**
 * Colour for a category's icon. Status categories use the AA text token for their fill; the
 * infrequent category keeps its secondary accent, which is not a status fill.
 */
export function noveltyCategoryTextColor(category: NoveltyCategory): string {
  const fill = noveltyCategoryColorVar(category);
  return category === 'infrequent' ? fill : textColorFor(fill);
}
