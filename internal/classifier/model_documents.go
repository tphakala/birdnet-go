package classifier

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// agreementsFileName is the file under the config directory that holds the
	// recorded agreements. Only ModelDocuments writes it; the settings API never
	// reads or writes it.
	agreementsFileName = "model-agreements.json"
	// documentCacheDirName is the directory under the config directory that holds
	// verified document text, one file per SHA-256.
	documentCacheDirName = "model-documents"
	// agreementsFileVersion is the only agreements file version this build reads
	// and writes. A file that states another version is never overwritten.
	agreementsFileVersion = 1
	// documentFetchTimeout bounds one fetch attempt against one endpoint, so a
	// host that connects and stalls fails over promptly.
	documentFetchTimeout = 20 * time.Second
	// documentMaxBytes caps a document body. The pinned documents are a few
	// kilobytes; the cap bounds what a broken or hostile mirror can make the
	// server read.
	documentMaxBytes = 256 << 10
	// documentFileMode and documentDirMode are the permissions of the cache and
	// agreements files and their directories (world-readable, like the catalog).
	documentFileMode = 0o644
	documentDirMode  = 0o755
	// documentComponent is the telemetry component of this service's errors.
	documentComponent = "classifier.model_documents"
)

// ErrDocumentUnavailable is returned when a document's text could not be fetched
// from any endpoint, or the document has no hosted text to fetch.
var ErrDocumentUnavailable = errors.NewStd("model document is unavailable")

// ErrDocumentMismatch is returned when fetched bytes do not hash to the digest
// the document is pinned to. Such bytes are never served, cached or agreed to.
var ErrDocumentMismatch = errors.NewStd("model document does not match its pinned digest")

// sha256HexPattern matches a lowercase hex SHA-256, the only form used as a cache
// file name.
var sha256HexPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// agreementKey identifies an agreement: the repo (normalized), the document kind
// and the digest of the agreed text, nothing else.
type agreementKey struct {
	repo, kind, sha256 string
}

func keyOf(d *RepoDocument) agreementKey {
	return agreementKey{repo: normalizeDocumentRepo(d.Repo), kind: d.Kind, sha256: d.SHA256}
}

// agreementRecord is one entry of the agreements file.
type agreementRecord struct {
	Repo     string    `json:"repo"`
	Kind     string    `json:"kind"`
	SHA256   string    `json:"sha256"`
	AgreedAt time.Time `json:"agreedAt"`
}

// agreementsFile is the on-disk shape of the agreements file.
type agreementsFile struct {
	Version    int               `json:"version"`
	Agreements []agreementRecord `json:"agreements"`
}

// ModelDocuments serves verified licence and policy text and keeps the
// agreements users have recorded. It is memory-only until SetStateDir points it
// at the config directory. All methods are safe for concurrent use.
type ModelDocuments struct {
	endpoints   func() []string
	noteWorking func(string)

	mu sync.Mutex
	// dir is the config directory; empty keeps everything in memory.
	dir string
	// persistDisabled is set when the agreements file exists but this build cannot
	// trust it (unreadable, unparsable or a version it does not know). Records are
	// then kept in memory only and the file is never overwritten.
	persistDisabled bool
	records         []agreementRecord
	agreed          map[agreementKey]struct{}
	// memText holds verified text that could not be cached on disk, by digest.
	memText map[string][]byte
}

// newModelDocuments builds a memory-only service. endpoints returns the ordered
// HuggingFace endpoint chain to try for each fetch and noteWorking records the
// endpoint that served one.
func newModelDocuments(endpoints func() []string, noteWorking func(string)) *ModelDocuments {
	return &ModelDocuments{
		endpoints:   endpoints,
		noteWorking: noteWorking,
		agreed:      make(map[agreementKey]struct{}),
		memText:     make(map[string][]byte),
	}
}

// SetStateDir points the service at the config directory and loads the recorded
// agreements from it. A missing file is an empty store. A file that cannot be
// read or parsed, or that states an unknown version, loads as empty and switches
// persistence off for this process, so a newer build's records are never erased.
// Records made before the call are kept.
func (d *ModelDocuments) SetStateDir(dir string) {
	if dir == "" {
		return
	}
	path := filepath.Join(dir, agreementsFileName)
	data, readErr := os.ReadFile(path) //nolint:gosec // G304: fixed file name under the config directory

	d.mu.Lock()
	defer d.mu.Unlock()
	d.dir = dir
	d.persistDisabled = false

	switch {
	case readErr != nil && errors.Is(readErr, os.ErrNotExist):
		return
	case readErr != nil:
		d.disablePersistence("cannot read the agreements file", readErr)
		return
	}
	var file agreementsFile
	if err := json.Unmarshal(data, &file); err != nil {
		d.disablePersistence("cannot parse the agreements file", err)
		return
	}
	if file.Version != agreementsFileVersion {
		d.disablePersistence("the agreements file has an unknown version", nil)
		return
	}
	for _, rec := range file.Agreements {
		d.addLocked(rec)
	}
}

// disablePersistence switches file persistence off and logs why. The caller holds
// d.mu.
func (d *ModelDocuments) disablePersistence(reason string, err error) {
	d.persistDisabled = true
	fields := []logger.Field{logger.String("path", filepath.Join(d.dir, agreementsFileName))}
	if err != nil {
		fields = append(fields, logger.Error(err))
	}
	GetLogger().Warn(reason+"; agreements are kept in memory and the file is left unchanged", fields...)
}

// addLocked adds rec when its key is not present. The caller holds d.mu.
func (d *ModelDocuments) addLocked(rec agreementRecord) {
	key := agreementKey{repo: normalizeDocumentRepo(rec.Repo), kind: rec.Kind, sha256: rec.SHA256}
	if _, ok := d.agreed[key]; ok {
		return
	}
	d.agreed[key] = struct{}{}
	d.records = append(d.records, rec)
}

// Agreed reports whether an agreement is recorded for the document's repo, kind
// and digest.
func (d *ModelDocuments) Agreed(doc *RepoDocument) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.agreed[keyOf(doc)]
	return ok
}

// Outstanding returns the documents that require agreement and have none
// recorded, in the order given.
func (d *ModelDocuments) Outstanding(docs []RepoDocument) []RepoDocument {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []RepoDocument
	for i := range docs {
		if !docs[i].RequiresAgreement {
			continue
		}
		if _, ok := d.agreed[keyOf(&docs[i])]; !ok {
			out = append(out, docs[i])
		}
	}
	return out
}

// Record stores an agreement for every document not already recorded and writes
// the whole agreements file atomically, also when every document was already
// recorded, so an earlier failed write is retried. A write failure keeps the
// records in memory and is returned so the caller can log it; with no state dir, or with persistence
// switched off by an untrusted file, it only records in memory.
func (d *ModelDocuments) Record(docs []RepoDocument) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now().UTC()
	for i := range docs {
		d.addLocked(agreementRecord{Repo: docs[i].Repo, Kind: docs[i].Kind, SHA256: docs[i].SHA256, AgreedAt: now})
	}
	// The file is written even when every document was already recorded, so a
	// record whose earlier write failed (or was made before SetStateDir) reaches
	// the file the next time the same agreement is recorded.
	if len(docs) == 0 || d.dir == "" || d.persistDisabled {
		return nil
	}
	data, err := json.MarshalIndent(agreementsFile{Version: agreementsFileVersion, Agreements: d.records}, "", "  ")
	if err != nil {
		return errors.New(err).
			Component(documentComponent).
			Category(errors.CategoryFileParsing).
			Context("operation", "marshal_model_agreements").
			Build()
	}
	if err := writeFileAtomic(d.dir, agreementsFileName, append(data, '\n')); err != nil {
		return errors.New(err).
			Component(documentComponent).
			Category(errors.CategoryFileIO).
			Context("operation", "write_model_agreements").
			Build()
	}
	return nil
}

// Text returns the verified text of doc. It reads the in-memory copy, then the
// cache file (re-hashed on every read), then fetches the document from doc.Repo
// at its pinned revision through the endpoint chain. Bytes that do not hash to
// doc.SHA256 are never returned or cached. The cache is keyed by digest alone,
// so two repos pinning the same bytes share one file, but a fetch always goes to
// the repo the caller named.
func (d *ModelDocuments) Text(ctx context.Context, doc *RepoDocument) ([]byte, error) {
	if doc.Path == "" || doc.Revision == "" || !sha256HexPattern.MatchString(doc.SHA256) {
		return nil, fmt.Errorf("%w: %s %s has no hosted text", ErrDocumentUnavailable, doc.Repo, doc.Kind)
	}

	d.mu.Lock()
	dir := d.dir
	mem, inMemory := d.memText[doc.SHA256]
	d.mu.Unlock()
	if inMemory {
		return bytes.Clone(mem), nil
	}
	if dir != "" {
		if cached, ok := readCachedDocument(dir, doc.SHA256); ok {
			return cached, nil
		}
	}

	text, err := d.fetch(ctx, doc)
	if err != nil {
		return nil, err
	}
	if dir != "" {
		cacheDir := filepath.Join(dir, documentCacheDirName)
		werr := writeFileAtomic(cacheDir, doc.SHA256, text)
		if werr == nil {
			return text, nil
		}
		GetLogger().Warn("could not cache a model document; keeping it in memory",
			logger.String("sha256", doc.SHA256), logger.Error(werr))
	}
	d.mu.Lock()
	d.memText[doc.SHA256] = bytes.Clone(text)
	d.mu.Unlock()
	return text, nil
}

// readCachedDocument returns the cache file for sha when it exists and hashes to
// its name; anything else is a miss.
func readCachedDocument(dir, sha string) ([]byte, bool) {
	f, err := os.Open(filepath.Join(dir, documentCacheDirName, sha)) //nolint:gosec // G304: sha is validated lowercase hex
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, documentMaxBytes+1))
	if err != nil || len(data) > documentMaxBytes || hexSHA256(data) != sha {
		return nil, false
	}
	return data, true
}

// hexSHA256 returns the lowercase hex SHA-256 of data.
func hexSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// fetch walks the endpoint chain for doc, failing over only on a reachability
// failure (never on a definite status such as 404 or on a digest mismatch), and
// notes the endpoint that served it.
func (d *ModelDocuments) fetch(ctx context.Context, doc *RepoDocument) ([]byte, error) {
	endpoints := d.endpoints()
	var lastErr error
	for i, endpoint := range endpoints {
		text, err := fetchDocumentFrom(ctx, endpoint, doc)
		if err == nil {
			d.noteWorking(endpoint)
			return text, nil
		}
		lastErr = err
		if i == len(endpoints)-1 || !shouldFailover(err) {
			break
		}
		GetLogger().Warn("HuggingFace host unreachable, trying next endpoint for a model document",
			logger.String("repo", doc.Repo),
			logger.String("failed_endpoint", endpoint),
			logger.String("next_endpoint", endpoints[i+1]),
			logger.Error(err))
	}
	if lastErr == nil {
		lastErr = errors.Newf("no HuggingFace endpoint available for %s", doc.Repo).
			Component(documentComponent).
			Category(errors.CategoryValidation).
			Build()
	}
	if errors.Is(lastErr, ErrDocumentMismatch) {
		return nil, lastErr
	}
	return nil, fmt.Errorf("%w: %w", ErrDocumentUnavailable, lastErr)
}

// fetchDocumentFrom makes one bounded attempt against one endpoint and verifies
// the bytes against doc.SHA256.
func fetchDocumentFrom(ctx context.Context, endpoint string, doc *RepoDocument) ([]byte, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, documentFetchTimeout)
	defer cancel()

	url := buildHuggingFaceRevisionURL(endpoint, doc.Repo, doc.Revision, doc.Path)
	resp, err := getHuggingFace(attemptCtx, downloadHTTPClient, url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, documentMaxBytes+1))
	if err != nil {
		// A connection dropped mid-body is retryable on the next endpoint.
		return nil, &endpointAttemptError{
			retryable: conf.IsUnreachable(err),
			err: errors.Newf("read error fetching %s: %v", url, err).
				Component(documentComponent).
				Category(errors.CategoryNetwork).
				Context("url", url).
				Build(),
		}
	}
	if len(data) > documentMaxBytes {
		return nil, errors.Newf("document at %s is larger than %d bytes", url, documentMaxBytes).
			Component(documentComponent).
			Category(errors.CategoryValidation).
			Context("url", url).
			Build()
	}
	if got := hexSHA256(data); got != doc.SHA256 {
		return nil, fmt.Errorf("%w: expected %s, got %s from %s", ErrDocumentMismatch, doc.SHA256, got, url)
	}
	return data, nil
}

// writeFileAtomic writes data to dir/name through a temp file in the same
// directory, synced before the rename so a crash leaves the old file or the new
// one, never a partial file.
func writeFileAtomic(dir, name string, data []byte) error {
	if err := os.MkdirAll(dir, documentDirMode); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, name+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Removes the temp file on any failure; after a successful rename it is a no-op.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(documentFileMode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filepath.Join(dir, name))
}
