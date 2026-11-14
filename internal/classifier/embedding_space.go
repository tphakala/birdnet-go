package classifier

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// EmbeddingSpaceBirdNETV24Bat names the embedding space of the BirdNET v2.4
// backbone the bat classifier heads are trained on, fed with 144000 samples at
// 256 kHz. It is the space of the catalog's shared embeddings file whatever
// generation of that file is installed: the generations are bit-identical in
// their embeddings, so the name follows the catalog path and not the file bytes.
// A change to the backbone or to the bat input convention that alters the
// embeddings must rename this constant, because stored vectors of the old name
// would otherwise be compared with incompatible new ones.
const EmbeddingSpaceBirdNETV24Bat = "birdnet-v2.4/bat-256k"

// embeddingSpaceHashPrefix starts the space name of an embeddings file the
// catalog does not own.
const embeddingSpaceHashPrefix = "sha256:"

// resolveEmbeddingSpace returns the embedding space of the bat backbone file at
// embeddingsPath. The catalog's shared embeddings file gets the named space
// EmbeddingSpaceBirdNETV24Bat without being read. Any other file is its own
// space, named by the SHA-256 of its content, so a user-supplied backbone can
// never be mixed with the catalog one.
func resolveEmbeddingSpace(modelsDir, embeddingsPath string) (string, error) {
	if isCatalogBatEmbeddings(modelsDir, embeddingsPath) {
		return EmbeddingSpaceBirdNETV24Bat, nil
	}

	f, err := os.Open(embeddingsPath) //nolint:gosec // G304: the path is the configured or resolved model file
	if err != nil {
		return "", errors.New(err).
			Category(errors.CategoryModelInit).
			Context("embedding_model", embeddingsPath).
			Build()
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", errors.New(err).
			Category(errors.CategoryModelInit).
			Context("embedding_model", embeddingsPath).
			Build()
	}
	return embeddingSpaceHashPrefix + hex.EncodeToString(h.Sum(nil)), nil
}

// isCatalogBatEmbeddings reports whether embeddingsPath is the shared embeddings
// file of a bat catalog entry under modelsDir, the location the model manager
// installs it to (modelsDir/shared/<local name>).
func isCatalogBatEmbeddings(modelsDir, embeddingsPath string) bool {
	if modelsDir == "" || embeddingsPath == "" {
		return false
	}
	want := filepath.Clean(embeddingsPath)
	catalog := ActiveCatalog()
	for i := range catalog {
		if catalog[i].RegistryID != RegistryIDBat {
			continue
		}
		for _, files := range entryFileSets(&catalog[i]) {
			for _, f := range files {
				if f.Role == RoleEmbeddings && filepath.Join(modelsDir, sharedDirName, f.LocalName) == want {
					return true
				}
			}
		}
	}
	return false
}
