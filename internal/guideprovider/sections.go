package guideprovider

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Canonical guide-section vocabulary.
//
// This mirrors the frontend classifier in frontend/src/lib/types/species.ts
// (GUIDE_*_HEADINGS + classifyCanonicalHeading). The two must stay in sync.
//
// Why the backend needs it: MediaWiki bird articles very commonly nest the
// "Voice"/"Vocalizations" section (and occasionally habitat/behaviour) as a
// DEEPER sub-section (=== H ===) under a top-level "Description" or "Behaviour"
// section. convertWikiSections flattens deeper headers to a bare line, so that
// prose is absorbed into the parent section's body and the frontend's section
// splitter can no longer surface it as its own comparison row — the card then
// degrades into one giant "Appearance" block that includes the voice prose.
// To fix that at the source, convertWikiSections promotes a sub-section whose
// heading names a canonical comparison category to a top-level "## H" header, so
// the split happens and the frontend gets a distinct Voice/Habitat/Behaviour row.
// Non-canonical sub-sections (Breeding, Feeding, Subspecies, Dialects…) stay
// flattened so their content remains inline in the parent row and no prose is lost.
//
// Matching (see containsHeadingToken) is a case-insensitive, LEADING-word-boundary
// substring test: it accepts inflections like "Vocalizations" (token
// "vocalization") and "Songs" (token "song") while rejecting embedded false
// positives like "Subsong" (the "song" is mid-word). This is stricter than the
// frontend's plain substring match on purpose — the backend must not promote a
// sub-section that the frontend would then drop as a duplicate row — but it stays a
// compatible subset: anything the backend promotes, the frontend also classifies.
//
// Lists cover the 16 locales BirdNET-Go ships
// (cs da de en es fi fr hu it lv nb nl pl pt sk sv); unmatched headings degrade
// gracefully (the sub-section is simply left flattened / the section omitted).

// guideSongsHeadings are heading fragments that denote a "songs & calls" / voice section.
var guideSongsHeadings = []string{
	// en / de / fr / es-pt-it (canto, voce, voz) / pl / fi / sv
	"songs and calls",
	"song",
	"calls",
	"voice",
	"vocalization",
	"stimme",
	"gesang",
	"chant et cris",
	"voix",
	"voz",
	"canto",
	"voce",
	"głos",
	"ääntelyt",
	"läte",
	// cs / sk
	"hlas",
	"zpěv",
	"spev",
	// da / nb
	"sang",
	"stemme",
	// hu
	"hangja",
	"ének",
	// lv
	"balss",
	"dziesma",
	// nl
	"geluid",
	"zang",
	"roep",
}

// guideAppearanceHeadings are heading fragments that denote an appearance / description section.
var guideAppearanceHeadings = []string{
	// en / de / fr / es / fi / sv
	"description",
	"appearance",
	"identification",
	"beschreibung",
	"merkmale",
	"aussehen",
	"apparence",
	"descripción",
	"aspecto",
	"kuvaus",
	"utseende",
	// it / pt
	"descrizione",
	"aspetto",
	"descrição",
	"aparência",
	// cs / sk
	"popis",
	"vzhled",
	"vzhľad",
	// da / nb
	"beskrivelse",
	"kendetegn",
	"kjennetegn",
	// hu
	"leírás",
	"megjelenése",
	"külleme",
	// lv
	"apraksts",
	"izskats",
	// nl
	"beschrijving",
	"kenmerken",
	"uiterlijk",
	// pl
	"wygląd",
	"opis",
}

// guideHabitatHeadings are heading fragments that denote a distribution / habitat / range section.
var guideHabitatHeadings = []string{
	// en / de / fr / es / fi / sv
	"distribution and habitat",
	"distribution",
	"habitat",
	"range",
	"verbreitung",
	"lebensraum",
	"répartition",
	"distribución",
	"levinneisyys",
	"utbredning",
	// it / pt
	"distribuzione",
	"areale",
	"distribuição",
	// cs / sk
	"rozšíření",
	"rozšírenie",
	"výskyt",
	"biotop",
	// da / nb
	"udbredelse",
	"levested",
	"utbredelse",
	"leveområde",
	// hu
	"elterjedése",
	"előfordulása",
	"élőhely",
	// lv
	"izplatība",
	"dzīvotne",
	// nl
	"verspreiding",
	"leefgebied",
	// pl
	"występowanie",
	"zasięg",
	"środowisko",
}

// guideBehaviourHeadings are heading fragments that denote a behaviour / ecology section.
var guideBehaviourHeadings = []string{
	// en / de / fr / es / fi / sv
	"behaviour",
	"behavior",
	"ecology",
	"verhalten",
	"comportement",
	"comportamiento",
	"ecología",
	"käyttäytyminen",
	"elintavat",
	"ekologia",
	"beteende",
	"ekologi",
	// it / pt
	"comportamento",
	"ecologia", //nolint:misspell // Italian/Portuguese for "ecology"; a foreign-language section heading, not an English misspelling
	"biologia", //nolint:misspell // Italian/Portuguese for "biology"; a foreign-language section heading, not an English misspelling
	// cs / sk
	"chování",
	"ekologie",
	"správanie",
	"ekológia",
	// da / nb
	"adfærd",
	"atferd",
	"økologi",
	// hu
	"életmódja",
	"viselkedése",
	// lv
	"uzvedība",
	"ekoloģija",
	// nl
	"gedrag",
	"ecologie",
	"leefwijze",
	// pl
	"zachowanie",
}

// canonicalSectionID identifies a canonical comparison row.
type canonicalSectionID string

const (
	sectionAppearance canonicalSectionID = "appearance"
	sectionVoice      canonicalSectionID = "voice"
	sectionHabitat    canonicalSectionID = "habitat"
	sectionBehaviour  canonicalSectionID = "behaviour"
)

// classifyCanonicalHeading maps a section heading to a canonical comparison
// category, or reports ok=false when it matches none. The check order mirrors the
// frontend's classifyCanonicalHeading (appearance, voice, habitat, behaviour) so
// an ambiguous heading is resolved to the same category on both sides.
func classifyCanonicalHeading(heading string) (canonicalSectionID, bool) {
	h := strings.ToLower(strings.TrimSpace(heading))
	if h == "" {
		return "", false
	}
	switch {
	case matchesHeadingVocab(h, guideAppearanceHeadings):
		return sectionAppearance, true
	case matchesHeadingVocab(h, guideSongsHeadings):
		return sectionVoice, true
	case matchesHeadingVocab(h, guideHabitatHeadings):
		return sectionHabitat, true
	case matchesHeadingVocab(h, guideBehaviourHeadings):
		return sectionBehaviour, true
	default:
		return "", false
	}
}

// isCanonicalHeading reports whether heading names a canonical comparison section,
// and is therefore worth promoting to a top-level "## " header when it appears as a
// deeper MediaWiki sub-section.
func isCanonicalHeading(heading string) bool {
	_, ok := classifyCanonicalHeading(heading)
	return ok
}

// matchesHeadingVocab reports whether the already-lowercased heading contains any
// vocabulary token at a leading word boundary.
func matchesHeadingVocab(lowerHeading string, vocab []string) bool {
	for _, token := range vocab {
		if containsHeadingToken(lowerHeading, token) {
			return true
		}
	}
	return false
}

// containsHeadingToken reports whether token occurs in lowerHeading (both already
// lowercased) with a leading word boundary — i.e. the token starts the string or is
// preceded by a non-letter/non-digit rune. It intentionally does NOT require a
// trailing boundary, so inflected forms match ("song" in "songs", "vocalization" in
// "vocalizations"), while embedded false positives are rejected ("song" in
// "subsong" fails because it is preceded by the letter 'b').
func containsHeadingToken(lowerHeading, token string) bool {
	if token == "" {
		return false
	}
	for start := 0; start+len(token) <= len(lowerHeading); {
		idx := strings.Index(lowerHeading[start:], token)
		if idx < 0 {
			return false
		}
		pos := start + idx
		if pos == 0 {
			return true
		}
		r, _ := utf8.DecodeLastRuneInString(lowerHeading[:pos])
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) {
			return true
		}
		start = pos + len(token)
	}
	return false
}
