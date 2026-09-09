// huggingface_chain.go adds automatic mirror failover on top of the single
// endpoint resolved by huggingface.go.
//
// huggingface.co is unreachable from some networks (notably behind the Great
// Firewall), where hf-mirror.com serves the same repositories. When no endpoint
// override is configured, callers try the canonical host first and fail over to
// the mirror on a network-level failure, so a fresh install works out of the box
// without the user having to discover and set a mirror. An explicit HF_ENDPOINT
// or settings override stays authoritative: it is the only endpoint tried, so a
// deliberately chosen mirror is never second-guessed.
//
// Nothing here performs network I/O at construction time; the resolver only
// reads a small local state file, so it is safe on the startup path of a host
// with no route out.
package conf

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// FallbackHuggingFaceMirror is the community mirror tried automatically after
	// the canonical host when no endpoint override is configured. It proxies both
	// the /api/ (commit lookup) and /resolve/ (file download) surfaces, so SHA
	// pinning and file downloads both work through it. This is an additive
	// default, not a host allowlist: an explicit override still wins and disables
	// failover entirely.
	FallbackHuggingFaceMirror = "https://hf-mirror.com"

	// hfStickyRevalidateInterval is how long a working endpoint stays preferred
	// before the chain re-probes from the top. It bounds how long the client
	// stays pinned to the mirror after the canonical host recovers.
	hfStickyRevalidateInterval = 30 * time.Minute

	// stickyPersistCadence bounds how stale the persisted sticky freshness may
	// get relative to actual use. NoteWorking persists on an endpoint change and
	// otherwise at most once per cadence, so continued use of the same host keeps
	// the on-disk UpdatedAt current without rewriting the file on every file in
	// an install. It is well under hfStickyRevalidateInterval so a restart
	// shortly after a successful download does not re-probe a blocked host.
	stickyPersistCadence = 5 * time.Minute

	// remoteCatalogStateDir is the subdirectory of the config directory that
	// holds remote-catalog state files. The sticky-endpoint file lives here; a
	// later phase adds the remote catalog cache and its refresh state under the
	// same directory.
	remoteCatalogStateDir = "remote-catalog"

	// hfEndpointStateFile records the last endpoint that worked. It is a
	// dedicated file so a later remote-catalog refresh state can use its own
	// file under remoteCatalogStateDir without either clobbering the other.
	hfEndpointStateFile = "endpoint_state.json"
)

// ResolveHuggingFaceEndpointChain returns the ordered list of base URLs to try
// for HuggingFace fetches, most-preferred first.
//
//   - A configured override (settings field, then HF_ENDPOINT) that validates is
//     authoritative: the returned chain is exactly that one endpoint, so failover
//     never retargets a deliberately chosen mirror.
//   - With no override, the chain is the canonical host followed by the mirror,
//     so a blocked canonical host fails over automatically.
//   - An invalid override degrades to the default chain, matching
//     ResolveHuggingFaceEndpoint. The warning is logged there, not here, to avoid
//     logging the same rejection twice.
//
// Every element is normalized without a trailing slash, so callers can append
// "/" + path. This never performs network I/O.
func ResolveHuggingFaceEndpointChain(configured string) []string {
	if raw, _, hasValue := huggingFaceOverrideSource(configured); hasValue {
		if endpoint, err := normalizeHuggingFaceEndpoint(raw); err == nil {
			return []string{endpoint}
		}
	}
	return []string{DefaultHuggingFaceEndpoint, FallbackHuggingFaceMirror}
}

// IsUnreachable reports whether err, returned by an HTTP client's Do or by a
// response-body read, means the host could not be reached, so a caller walking
// an endpoint chain should fail over to the next endpoint.
//
// An HTTP client returns a non-nil error from Do only when no response was
// obtained: a DNS failure, a refused or reset connection, a TLS error, an HTTP/2
// GOAWAY, or a timeout. A completed response, including 404 or 500, comes back
// with a nil error and its status in the response, so this function is never
// used to classify a status code (see IsGatewayStatus for that). Because every
// such error is a transport failure, this returns true for any non-nil error
// with a single exception: context.Canceled, which means the caller deliberately
// aborted (a cancelled install, not a blocked host) and must not trigger
// failover. A deadline being exceeded is not excepted: a bounded timeout firing
// is exactly the blocked-host signal that failover exists for.
func IsUnreachable(err error) bool {
	if err == nil {
		return false
	}
	return !errors.Is(err, context.Canceled)
}

// IsGatewayStatus reports whether an HTTP status warrants failing over to the
// next endpoint. 502, 503 and 504 mean the origin (not the requested file) is
// unavailable, so a mirror may still serve the file. A 404 is deliberately not
// included: it means the host is reachable and the file is genuinely absent,
// which failover would only mask.
func IsGatewayStatus(status int) bool {
	switch status {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// HFEndpointResolver orders the endpoint chain for each fetch and remembers the
// endpoint that last worked, so once the canonical host is found to be blocked
// the client goes straight to the mirror for subsequent files and later
// installs instead of paying the connect timeout every time.
//
// The sticky endpoint is persisted so the preference survives a restart. All
// methods are safe for concurrent use.
type HFEndpointResolver struct {
	mu          sync.Mutex
	sticky      string
	stickyAt    time.Time
	lastSavedAt time.Time // when the sticky state was last persisted
	now         func() time.Time
	statePath   string // empty disables persistence (in-memory only)
}

// hfEndpointState is the on-disk shape of the sticky endpoint.
type hfEndpointState struct {
	Endpoint  string    `json:"endpoint"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// NewHFEndpointResolver builds a resolver whose sticky state persists under
// configDir. An empty configDir keeps the sticky preference in memory only.
// Construction reads at most one small local file and never touches the network,
// so it is safe on the startup path.
func NewHFEndpointResolver(configDir string) *HFEndpointResolver {
	r := &HFEndpointResolver{now: time.Now}
	if configDir != "" {
		r.statePath = filepath.Join(configDir, remoteCatalogStateDir, hfEndpointStateFile)
		r.load()
	}
	return r
}

// OrderedEndpoints returns the endpoint chain for configured, with the sticky
// endpoint moved to the front when it is still a member of the chain and has
// not gone stale. An explicit override yields a single-element chain, so sticky
// ordering does not apply and no failover happens.
func (r *HFEndpointResolver) OrderedEndpoints(configured string) []string {
	chain := ResolveHuggingFaceEndpointChain(configured)
	if len(chain) < 2 {
		return chain
	}

	r.mu.Lock()
	sticky, stickyAt := r.sticky, r.stickyAt
	r.mu.Unlock()

	if sticky == "" || r.now().Sub(stickyAt) >= hfStickyRevalidateInterval {
		return chain
	}
	idx := slices.Index(chain, sticky)
	if idx <= 0 {
		// Sticky is not in the chain, or already at the front: nothing to reorder.
		return chain
	}
	ordered := make([]string, 0, len(chain))
	ordered = append(ordered, sticky)
	for i, ep := range chain {
		if i != idx {
			ordered = append(ordered, ep)
		}
	}
	return ordered
}

// NoteWorking records endpoint as the one that last served a request, so later
// calls prefer it, and persists the preference best-effort.
func (r *HFEndpointResolver) NoteWorking(endpoint string) {
	if endpoint == "" {
		return
	}
	r.mu.Lock()
	changed := r.sticky != endpoint
	r.sticky = endpoint
	r.stickyAt = r.now()
	// Persist on an endpoint change, and otherwise at most once per cadence, so
	// continued success on the same host keeps the persisted freshness current.
	// Persisting only on change would freeze the on-disk UpdatedAt at the first
	// mirror success, so a restart more than the revalidate interval after that
	// first success would re-probe the blocked canonical host even though the
	// mirror was used seconds ago. Files within one install complete well inside
	// the cadence, so this still writes at most once per install.
	needSave := changed || r.stickyAt.Sub(r.lastSavedAt) >= stickyPersistCadence
	if needSave {
		r.lastSavedAt = r.stickyAt
	}
	state := hfEndpointState{Endpoint: r.sticky, UpdatedAt: r.stickyAt}
	r.mu.Unlock()

	if needSave {
		r.save(state)
	}
}

// load reads the persisted sticky endpoint, ignoring any error: a missing or
// unreadable file simply means no preference yet.
func (r *HFEndpointResolver) load() {
	data, err := os.ReadFile(r.statePath)
	if err != nil {
		return
	}
	var state hfEndpointState
	if err := json.Unmarshal(data, &state); err != nil {
		return
	}
	// Only trust a persisted endpoint that still validates, so a stale or
	// tampered file can never retarget downloads at an arbitrary host.
	if normalized, err := normalizeHuggingFaceEndpoint(state.Endpoint); err == nil {
		r.sticky = normalized
		r.stickyAt = state.UpdatedAt
		r.lastSavedAt = state.UpdatedAt
	}
}

// save writes the sticky endpoint atomically, best-effort. A failure is logged
// at debug level and otherwise ignored: losing the preference only costs one
// extra connect attempt after a restart.
func (r *HFEndpointResolver) save(state hfEndpointState) {
	if r.statePath == "" {
		return
	}
	dir := filepath.Dir(r.statePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		GetLogger().Debug("could not create HuggingFace endpoint state directory",
			logger.String("dir", dir), logger.Error(err))
		return
	}
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, hfEndpointStateFile+".*.tmp")
	if err != nil {
		GetLogger().Debug("could not create HuggingFace endpoint state temp file",
			logger.String("dir", dir), logger.Error(err))
		return
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		GetLogger().Debug("could not write HuggingFace endpoint state temp file",
			logger.String("path", tmpPath), logger.Error(err))
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		GetLogger().Debug("could not close HuggingFace endpoint state temp file",
			logger.String("path", tmpPath), logger.Error(err))
		return
	}
	if err := os.Rename(tmpPath, r.statePath); err != nil {
		_ = os.Remove(tmpPath)
		GetLogger().Debug("could not persist HuggingFace endpoint state",
			logger.String("path", r.statePath), logger.Error(err))
	}
}
