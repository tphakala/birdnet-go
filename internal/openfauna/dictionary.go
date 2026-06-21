package openfauna

import "maps"

// BuildLocaleDictionary returns a scientific-name -> common-name dictionary for the
// given birdnet-go locale, covering every species OpenFauna translates in that locale
// or in English. The locale is resolved to a dataset locale via the same mapLocale
// rules used by name resolution, and English is baked in as the fallback for any
// species the target locale does not translate, so each dictionary is self-contained.
//
// Keys are scientific names verbatim from the dataset; values are the localized common
// name (target locale preferred, English otherwise). This is the build-time source for
// the per-locale dictionaries served to the dashboard, and it deliberately reuses
// mapLocale and the shared translation stream so client-side display/search resolution
// stays consistent with server-side resolution authority (single source of truth).
func BuildLocaleDictionary(bngLocale string) (map[string]string, error) {
	return buildLocaleDictionaryFromStream(streamTranslations, mapLocale(bngLocale))
}

// buildLocaleDictionaryFromStream is the testable core of BuildLocaleDictionary. It
// consumes a translation stream once, collecting the effective locale's names and the
// English fallback names, then folds the locale over English. eff must already be a
// mapped OpenFauna locale code (the result of mapLocale).
func buildLocaleDictionaryFromStream(stream func(translationRowFunc) error, eff string) (map[string]string, error) {
	inLocale := make(map[string]string)  // scientific -> common (effective locale)
	inEnglish := make(map[string]string) // scientific -> common (English fallback)

	if err := stream(func(sci, loc, common string) error {
		// An empty scientific name or translation cannot key or satisfy a lookup; skip
		// so an empty row cannot shadow a real name for the same species.
		if sci == "" || common == "" {
			return nil
		}
		switch {
		case loc == eff:
			if _, exists := inLocale[sci]; !exists {
				inLocale[sci] = common
			}
		case eff != localeFallback && loc == localeFallback:
			if _, exists := inEnglish[sci]; !exists {
				inEnglish[sci] = common
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	// English first, then overlay the target locale so a localized name always wins.
	out := make(map[string]string, len(inEnglish)+len(inLocale))
	maps.Copy(out, inEnglish)
	maps.Copy(out, inLocale)
	return out, nil
}
