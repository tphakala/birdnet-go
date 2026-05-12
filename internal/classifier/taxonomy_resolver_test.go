package classifier

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Compile-time check that TaxonomyResolver implements NameResolver.
var _ NameResolver = (*TaxonomyResolver)(nil)

const testTaxonomyCSV = `idx,sci_name,com_name,species_code,class_name,common_name_de,common_name_fi,common_name_fr,common_name_zh-CN,common_name_pt_PT
0,Struthio camelus,Common Ostrich,ostric2,Aves,Afrikanischer Strauß,Afrikkanstrutssi,Autruche d'Afrique,鸵鸟,Avestruz-comum
1,Dromaius novaehollandiae,Emu,emu1,Aves,Emu,Emu,Émeu d'Australie,鸸鹋,Emu
2,Apteryx mantelli,North Island Brown Kiwi,nibkiw1,Aves,Nördlicher Streifenkiwi,Pohjanruskokiivi,Kiwi austral,北岛褐几维,Quivi-castanho
3,Tinamus major,Great Tinamou,gretia1,Aves,Großtinamu,Isotinami,Grand Tinamou,大共鸟,Macuco
`

func writeTaxonomyFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "taxonomy.csv")
	require.NoError(t, os.WriteFile(path, []byte(testTaxonomyCSV), 0o644))
	return path
}

func TestTaxonomyResolver_EnglishDefault(t *testing.T) {
	t.Parallel()
	path := writeTaxonomyFixture(t)

	resolver, err := NewTaxonomyResolver(path, "en-uk")
	require.NoError(t, err)

	assert.Equal(t, "Common Ostrich", resolver.Resolve("Struthio camelus", ""))
	assert.Equal(t, "Emu", resolver.Resolve("Dromaius novaehollandiae", ""))
	assert.Equal(t, "North Island Brown Kiwi", resolver.Resolve("Apteryx mantelli", ""))
}

func TestTaxonomyResolver_GermanLocale(t *testing.T) {
	t.Parallel()
	path := writeTaxonomyFixture(t)

	resolver, err := NewTaxonomyResolver(path, "de")
	require.NoError(t, err)

	assert.Equal(t, "Afrikanischer Strauß", resolver.Resolve("Struthio camelus", ""))
	assert.Equal(t, "Emu", resolver.Resolve("Dromaius novaehollandiae", ""))
	assert.Equal(t, "Großtinamu", resolver.Resolve("Tinamus major", ""))
}

func TestTaxonomyResolver_FinnishLocale(t *testing.T) {
	t.Parallel()
	path := writeTaxonomyFixture(t)

	resolver, err := NewTaxonomyResolver(path, "fi")
	require.NoError(t, err)

	assert.Equal(t, "Afrikkanstrutssi", resolver.Resolve("Struthio camelus", ""))
	assert.Equal(t, "Isotinami", resolver.Resolve("Tinamus major", ""))
}

func TestTaxonomyResolver_ChineseLocale(t *testing.T) {
	t.Parallel()
	path := writeTaxonomyFixture(t)

	resolver, err := NewTaxonomyResolver(path, "zh")
	require.NoError(t, err)

	assert.Equal(t, "鸵鸟", resolver.Resolve("Struthio camelus", ""))
	assert.Equal(t, "鸸鹋", resolver.Resolve("Dromaius novaehollandiae", ""))
}

func TestTaxonomyResolver_PortuguesePortugal(t *testing.T) {
	t.Parallel()
	path := writeTaxonomyFixture(t)

	resolver, err := NewTaxonomyResolver(path, "pt-pt")
	require.NoError(t, err)

	assert.Equal(t, "Avestruz-comum", resolver.Resolve("Struthio camelus", ""))
	assert.Equal(t, "Quivi-castanho", resolver.Resolve("Apteryx mantelli", ""))
}

func TestTaxonomyResolver_FallbackToEnglish(t *testing.T) {
	t.Parallel()
	path := writeTaxonomyFixture(t)

	resolver, err := NewTaxonomyResolver(path, "ar")
	require.NoError(t, err)

	assert.Equal(t, "Common Ostrich", resolver.Resolve("Struthio camelus", ""),
		"unsupported locale should fall back to English com_name")
}

func TestTaxonomyResolver_CaseInsensitive(t *testing.T) {
	t.Parallel()
	path := writeTaxonomyFixture(t)

	resolver, err := NewTaxonomyResolver(path, "en-uk")
	require.NoError(t, err)

	assert.Equal(t, "Emu", resolver.Resolve("dromaius novaehollandiae", ""))
	assert.Equal(t, "Emu", resolver.Resolve("DROMAIUS NOVAEHOLLANDIAE", ""))
}

func TestTaxonomyResolver_MissingSpecies(t *testing.T) {
	t.Parallel()
	path := writeTaxonomyFixture(t)

	resolver, err := NewTaxonomyResolver(path, "en-uk")
	require.NoError(t, err)

	assert.Empty(t, resolver.Resolve("Nonexistent species", ""))
}

func TestTaxonomyResolver_EmptyCSV(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.csv")
	require.NoError(t, os.WriteFile(path, []byte(""), 0o644))

	resolver, err := NewTaxonomyResolver(path, "en-uk")
	require.NoError(t, err)
	assert.Empty(t, resolver.Resolve("Anything", ""))
}

func TestTaxonomyResolver_NilIndex(t *testing.T) {
	t.Parallel()
	resolver := &TaxonomyResolver{}
	assert.Empty(t, resolver.Resolve("Struthio camelus", ""))
}

func TestTaxonomyResolver_MissingFile(t *testing.T) {
	t.Parallel()
	_, err := NewTaxonomyResolver("/nonexistent/path/taxonomy.csv", "en-uk")
	assert.Error(t, err)
}
