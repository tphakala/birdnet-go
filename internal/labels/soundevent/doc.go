// Package soundevent is the registry of sound-event classes that classifiers
// emit alongside, or instead of, species labels.
//
// The current classes are the AudioSet/FSD50K sound-event classes of the
// Google Perch v2 model (198 entries in classes.go), each assigned a
// [Category]. They include animal sound events such as dog, frog and crow, so
// membership means "a sound-event class", not "not a bird". Species labels
// (binomials) are never members. The registry is shared across models, so
// another model family adds its classes here.
//
// It exposes these lookup paths:
//
//   - Full-label lookup via [CategoryOf] and [IsLabel]: matches the complete
//     raw model label (e.g. "power_tool", "human_voice") exactly.
//   - Truncated first-token lookup via [IsName]: matches both the full label
//     and the first underscore-delimited token of a multi-word class
//     (e.g. "Power" from "power_tool", "Engine" from "engine"). This is the
//     lookup the image provider uses for names stored before full labels were
//     kept whole; new detections carry the full label.
//   - Multi-word label recognition via [MultiWordLabelNames]: lets every label
//     splitter keep a full class label such as "Domestic_sounds_and_home_sounds"
//     whole instead of splitting it at the first underscore.
//
// The static classes map in classes.go is the committed, hand-categorized
// source of truth. The firstTokenSet is derived from it automatically in
// init() so it can never drift from the data.
//
// # Regenerating the candidate list
//
// Candidate sound-event classes are the lines in a Perch label CSV that are
// single-token OR contain "_":
//
//	awk 'NR>1 && ($0 ~ /_/ || $0 !~ / /)' perch_v2_labels.csv
//
// Then lowercase and hand-categorize into classes.go. The committed file is
// the source of truth; the categorization is applied by hand so a future
// genus-level single-token label cannot be silently misfiled.
package soundevent
