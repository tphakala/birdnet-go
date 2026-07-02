package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

func TestMigrateLegacyAlertLists(t *testing.T) {
	db := setupSpeciesListsTestDB(t)
	ctx := t.Context()

	// Insert legacy rule and conditions
	rule := entities.AlertRule{
		Name: "Test Rule",
	}
	require.NoError(t, db.Create(&rule).Error)

	condCommon := entities.AlertCondition{
		RuleID:   rule.ID,
		Property: "scientific_name",
		Operator: "in",
		Value:    "Eurasian Blackbird, cyanistes caeruleus, UnrecognizedBirdName",
	}
	require.NoError(t, db.Create(&condCommon).Error)

	// Run migration using English locale
	err := MigrateLegacyAlertLists(ctx, db, "en-US")
	require.NoError(t, err)

	// Reload condition
	var updatedCond entities.AlertCondition
	err = db.First(&updatedCond, condCommon.ID).Error
	require.NoError(t, err)

	// Value should start with list:
	assert.Contains(t, updatedCond.Value, "list:")

	// Fetch the list
	var list entities.SpeciesList
	err = db.Preload("Members").First(&list).Error
	require.NoError(t, err)
	assert.Equal(t, "Migrated List (Test Rule)", list.Name)
	assert.False(t, list.IsSystem)

	// Verify member names were resolved or preserved
	membersMap := make(map[string]bool)
	for _, m := range list.Members {
		membersMap[m.ScientificName] = true
	}

	assert.Len(t, list.Members, 3)
	// "Eurasian Blackbird" -> "turdus merula"
	assert.True(t, membersMap["turdus merula"])
	// "cyanistes caeruleus" -> "cyanistes caeruleus"
	assert.True(t, membersMap["cyanistes caeruleus"])
	// "UnrecognizedBirdName" -> "unrecognizedbirdname" (imported as unrecognized)
	assert.True(t, membersMap["unrecognizedbirdname"])

	// Test case: Migrating a second condition for the same rule (duplicate list name test)
	condSecond := entities.AlertCondition{
		RuleID:   rule.ID,
		Property: "scientific_name",
		Operator: "in",
		Value:    "parus major",
	}
	require.NoError(t, db.Create(&condSecond).Error)

	err = MigrateLegacyAlertLists(ctx, db, "en-US")
	require.NoError(t, err)

	// Reload second condition
	var updatedSecondCond entities.AlertCondition
	err = db.First(&updatedSecondCond, condSecond.ID).Error
	require.NoError(t, err)
	assert.Contains(t, updatedSecondCond.Value, "list:")

	// Get lists in database, should have 2 lists now, name of second should be "Migrated List (Test Rule) 2"
	var lists []entities.SpeciesList
	err = db.Order("id asc").Find(&lists).Error
	require.NoError(t, err)
	require.Len(t, lists, 2)
	assert.Equal(t, "Migrated List (Test Rule)", lists[0].Name)
	assert.Equal(t, "Migrated List (Test Rule) 2", lists[1].Name)
}
