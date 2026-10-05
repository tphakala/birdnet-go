package analytics

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestServerChosenSpeciesResponses_CarryCommonName pins #4459: the four analytics payloads that
// name server-chosen species serialize the datastore's server-resolved common name as "commonName".
func TestServerChosenSpeciesResponses_CarryCommonName(t *testing.T) {
	t.Parallel()
	const (
		sci    = "Turdus merula"
		common = "Common Blackbird"
	)

	tests := []struct {
		name string
		resp any
	}{
		{"species distribution", newSpeciesHourlyDistributionResponse(
			[]datastore.SpeciesHourlyDistribution{{ScientificName: sci, CommonName: common}})},
		{"acoustic succession", newAcousticSuccessionResponse(
			[]datastore.SpeciesHourlyCounts{{ScientificName: sci, CommonName: common}})},
		{"confidence distribution", newConfidenceDistributionResponse(
			[]datastore.SpeciesConfidenceHistogram{{ScientificName: sci, CommonName: common}})},
		{"species phenology", newSpeciesPhenologyResponse(
			[]datastore.SpeciesPhenologyPoint{{ScientificName: sci, CommonName: common}})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			raw, err := json.Marshal(tt.resp)
			require.NoError(t, err)
			var rows []map[string]any
			require.NoError(t, json.Unmarshal(raw, &rows))
			require.Len(t, rows, 1)
			assert.Equal(t, sci, rows[0]["scientificName"])
			assert.Equal(t, common, rows[0]["commonName"])
		})
	}
}

// TestPayloadsNamingSpeciesCarryCommonName is a drift guard. A wire struct that names a species by
// scientific name must also carry its display name, otherwise the UI shows scientific names on any
// view that did not also fetch the species summary (#4459). Every named struct type in this
// package's non-test sources with a "scientificName" or "scientific_name" JSON field must have the
// matching "commonName" or "common_name" field. Anonymous struct literals and fields promoted from
// embedded structs are not inspected.
func TestPayloadsNamingSpeciesCarryCommonName(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	fset := token.NewFileSet()
	checked := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		require.NoError(t, err, file)
		ast.Inspect(parsed, func(n ast.Node) bool {
			spec, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := spec.Type.(*ast.StructType)
			if !ok {
				return true
			}
			tags := jsonTagNames(st)
			for sciTag, commonTag := range map[string]string{
				"scientificName":  "commonName",
				"scientific_name": "common_name",
			} {
				if !tags[sciTag] {
					continue
				}
				checked++
				assert.True(t, tags[commonTag],
					"%s (%s) has a %q field but no %q field", spec.Name.Name, file, sciTag, commonTag)
			}
			return true
		})
	}
	assert.Positive(t, checked, "guard found no scientific-name structs; the scan is broken")
}

// jsonTagNames returns the set of JSON tag names declared on the struct's fields.
func jsonTagNames(st *ast.StructType) map[string]bool {
	names := make(map[string]bool)
	for _, field := range st.Fields.List {
		if field.Tag == nil {
			continue
		}
		tag := reflect.StructTag(strings.Trim(field.Tag.Value, "`"))
		name, _, _ := strings.Cut(tag.Get("json"), ",")
		if name != "" {
			names[name] = true
		}
	}
	return names
}
