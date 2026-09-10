package classifier

// region_staleness.go implements recommend-only detection of installed regional
// model variants made stale by a station coordinate change, plus the bell
// notification that surfaces them. Rule D2: it never installs, switches, or
// unloads a model; it only tells the user a better regional slice exists so they
// can switch it from the gallery themselves.

import (
	"fmt"

	"github.com/tphakala/birdnet-go/internal/classifier/region"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// RegionStalenessChange records one installed regional model variant whose
// region no longer matches what the newly saved coordinates resolve to.
type RegionStalenessChange struct {
	CatalogID string // catalog entry id of the installed model
	ModelName string // user-facing model name, for the notification text
	OldRegion string // display name of the installed variant's region
	NewRegion string // display name of the newly resolved region; "" when the global model is now recommended
}

// InstalledModelRegion is a snapshot of one installed model used as detector
// input. It is kept separate from the catalog types so DetectRegionStaleness
// stays a pure function of its arguments and is table-testable without the
// catalog or the model manager.
type InstalledModelRegion struct {
	CatalogID string // catalog entry id
	ModelName string // user-facing model name
	Repo      string // HuggingFace repo id, the key into the region tables
	Region    string // installed variant's region slug; "" for a hardware/global variant
}

// RegionCoords is one coordinate snapshot for staleness detection. Configured
// mirrors settings.BirdNET.LocationConfigured: coordinate resolution is only
// meaningful once the user has set a station location.
type RegionCoords struct {
	Lat, Lon   float64
	Configured bool
}

// DetectRegionStaleness reports which installed regional model variants are made
// stale by a coordinate change. It is pure: given the region tables, the
// installed-model snapshot, the ModelRegion mode, and the old and new
// coordinates, it returns the stale variants without touching any global state.
//
// Recommend-only (rule D2): pinned and global modes are explicit user choices a
// coordinate edit must not second-guess, so they never produce a change. A
// variant is reported only when the new coordinates resolve to a region that
// differs from BOTH the installed variant's region AND what the old coordinates
// resolved to, which keeps a re-save, an in-region nudge, an A->B->A round trip,
// and a pre-existing mismatch all silent.
func DetectRegionStaleness(
	tables map[string]*region.Table,
	installed []InstalledModelRegion,
	modelRegion string,
	oldCoords, newCoords RegionCoords,
) []RegionStalenessChange {
	// Pinned or global mode: the user pinned a region (or forced global), so a
	// coordinate change carries no recommendation. This intentionally silences
	// even a pinned-fallback family (a pinned slug absent from that family's own
	// table, where coordinates would otherwise drive selection): the explicit pin
	// still wins over an automatic recommendation.
	if modelRegion != region.ModeAuto && modelRegion != "" {
		return nil
	}
	// Nothing to resolve against until a location is configured.
	if !newCoords.Configured {
		return nil
	}

	var changes []RegionStalenessChange
	for i := range installed {
		m := &installed[i]
		if m.Region == "" {
			continue // hardware/global variant: no regional slice to go stale
		}
		tbl, ok := tables[m.Repo]
		if !ok || tbl == nil {
			continue // family without a region table
		}
		newSel := region.Select(tbl, region.ModeAuto, newCoords.Lat, newCoords.Lon)
		if newSel.Slug == m.Region {
			continue // the new location still matches the installed slice
		}
		// Spam guard: suppress only a pre-existing mismatch, i.e. when a
		// previously-configured location already resolved to the same region the
		// new one does. It is gated on oldCoords.Configured so that a first-time
		// location set is never silenced: without the gate, an unconfigured old
		// location (oldSlug "") and a new location that resolves to the global
		// model (Slug "") would collide and wrongly suppress the "your installed
		// regional model no longer covers your location" recommendation.
		if oldCoords.Configured {
			oldSlug := region.Select(tbl, region.ModeAuto, oldCoords.Lat, oldCoords.Lon).Slug
			if newSel.Slug == oldSlug {
				continue
			}
		}
		changes = append(changes, RegionStalenessChange{
			CatalogID: m.CatalogID,
			ModelName: m.ModelName,
			OldRegion: regionDisplayName(tbl, m.Region),
			NewRegion: regionDisplayName(tbl, newSel.Slug),
		})
	}
	return changes
}

// regionDisplayName maps a region slug to its display name in tbl, falling back
// to the slug itself when the slug is unknown. An empty slug (the global model)
// stays empty so callers can branch on "no region".
func regionDisplayName(tbl *region.Table, slug string) string {
	if slug == "" || tbl == nil {
		return slug
	}
	if r, ok := tbl.Regions[slug]; ok && r.Name != "" {
		return r.Name
	}
	return slug
}

// NotifyRegionStaleness emits one persistent bell notification per change,
// mirroring emitORTUnavailableNotification. It is nil-safe when the notification
// service is not initialized. Recommend-only: it never installs, switches, or
// unloads a model.
func NotifyRegionStaleness(changes []RegionStalenessChange) {
	if len(changes) == 0 {
		return
	}
	svc := notification.GetService()
	if svc == nil {
		return
	}
	for i := range changes {
		ch := &changes[i]
		messageKey := notification.MsgModelRegionStaleMessage
		args := map[string]any{
			"modelName": ch.ModelName,
			"oldRegion": ch.OldRegion,
			"newRegion": ch.NewRegion,
		}
		fallbackMsg := fmt.Sprintf(
			"%s is installed for %s, but your new location resolves to %s. You can switch variants from the model gallery in Settings.",
			ch.ModelName, ch.OldRegion, ch.NewRegion)
		// When the new location falls outside every regional tile the recommendation
		// is the global variant; a dedicated message avoids rendering "resolves to "
		// with an empty region name.
		if ch.NewRegion == "" {
			messageKey = notification.MsgModelRegionStaleGlobalMessage
			args = map[string]any{
				"modelName": ch.ModelName,
				"oldRegion": ch.OldRegion,
			}
			fallbackMsg = fmt.Sprintf(
				"%s is installed for %s, but your new location is outside its coverage. You can switch to the global variant from the model gallery in Settings.",
				ch.ModelName, ch.OldRegion)
		}
		notif := notification.NewNotification(
			notification.TypeWarning,
			notification.PriorityMedium,
			"Model region may be outdated",
			fallbackMsg,
		).
			WithComponent("classifier").
			WithTitleKey(notification.MsgModelRegionStaleTitle, nil).
			WithMessageKey(messageKey, args).
			WithDeliveryTarget("bell")
		_ = svc.CreateWithMetadata(notif)
	}
}

// InstalledRegionalModels snapshots the visible installed models and the region
// slug of their installed variant ("" for hardware/global variants), as input
// for DetectRegionStaleness. It reads catalog and manager state, so it is not
// pure; the detector it feeds is. Hidden entries are excluded (VisibleCatalog),
// matching the region endpoint's family scope, so a hidden installed model never
// triggers a staleness notification.
func (mm *ModelManager) InstalledRegionalModels() []InstalledModelRegion {
	visible := VisibleCatalog()
	out := make([]InstalledModelRegion, 0, len(visible))
	// Hold one read lock for the whole scan rather than reacquiring it per entry
	// through InstalledVariantID. VisibleCatalog already released the catalog lock
	// before returning its copy, so there is no lock-ordering hazard here.
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	for i := range visible {
		e := &visible[i]
		im, ok := mm.installed[e.ID]
		if !ok {
			continue // not installed
		}
		out = append(out, InstalledModelRegion{
			CatalogID: e.ID,
			ModelName: e.Name,
			Repo:      e.HuggingFaceRepo,
			Region:    VariantRegion(e, im.VariantID),
		})
	}
	return out
}
