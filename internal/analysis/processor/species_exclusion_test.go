package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsSpeciesExcluded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		commonName     string
		scientificName string
		excludeList    []string
		want           bool
	}{
		{
			name:           "match by common name",
			commonName:     "American Robin",
			scientificName: "Turdus migratorius",
			excludeList:    []string{"American Robin"},
			want:           true,
		},
		{
			name:           "match by scientific name",
			commonName:     "American Robin",
			scientificName: "Turdus migratorius",
			excludeList:    []string{"Turdus migratorius"},
			want:           true,
		},
		{
			name:           "case insensitive common name",
			commonName:     "American Robin",
			scientificName: "Turdus migratorius",
			excludeList:    []string{"american robin"},
			want:           true,
		},
		{
			name:           "case insensitive scientific name",
			commonName:     "American Robin",
			scientificName: "Turdus migratorius",
			excludeList:    []string{"turdus migratorius"},
			want:           true,
		},
		{
			name:           "no match",
			commonName:     "American Robin",
			scientificName: "Turdus migratorius",
			excludeList:    []string{"House Sparrow", "Blue Jay"},
			want:           false,
		},
		{
			name:           "empty exclude list",
			commonName:     "American Robin",
			scientificName: "Turdus migratorius",
			excludeList:    []string{},
			want:           false,
		},
		{
			name:           "nil exclude list",
			commonName:     "American Robin",
			scientificName: "Turdus migratorius",
			excludeList:    nil,
			want:           false,
		},
		{
			name:           "multiple entries with match",
			commonName:     "House Sparrow",
			scientificName: "Passer domesticus",
			excludeList:    []string{"American Robin", "House Sparrow", "Blue Jay"},
			want:           true,
		},
		{
			name:           "partial name does not match",
			commonName:     "American Robin",
			scientificName: "Turdus migratorius",
			excludeList:    []string{"Robin", "American"},
			want:           false,
		},
		{
			name:           "empty common name matches by scientific name",
			commonName:     "",
			scientificName: "Turdus migratorius",
			excludeList:    []string{"Turdus migratorius"},
			want:           true,
		},
		{
			name:           "empty scientific name matches by common name",
			commonName:     "American Robin",
			scientificName: "",
			excludeList:    []string{"American Robin"},
			want:           true,
		},
		{
			name:           "both names empty never matches",
			commonName:     "",
			scientificName: "",
			excludeList:    []string{"American Robin"},
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isSpeciesExcluded(tt.commonName, tt.scientificName, tt.excludeList)
			assert.Equal(t, tt.want, got)
		})
	}
}
