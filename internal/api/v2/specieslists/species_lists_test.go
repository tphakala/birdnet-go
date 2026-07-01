package specieslists

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type mockV2Manager struct {
	db *gorm.DB
}

func (m *mockV2Manager) DB() *gorm.DB              { return m.db }
func (m *mockV2Manager) IsMySQL() bool            { return false }
func (m *mockV2Manager) TablePrefix() string      { return "" }
func (m *mockV2Manager) CheckpointWAL() error     { return nil }
func (m *mockV2Manager) Close() error             { return nil }
func (m *mockV2Manager) Initialize() error         { return nil }
func (m *mockV2Manager) Path() string             { return "" }
func (m *mockV2Manager) Delete() error             { return nil }
func (m *mockV2Manager) Exists() bool             { return true }

func TestSpeciesListAPI(t *testing.T) {
	// 1. Setup in-memory DB and auto-migrate entities
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	err = db.AutoMigrate(&entities.SpeciesList{}, &entities.SpeciesListMember{})
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})

	// Set enhanced mode to true for test
	datastoreV2.SetEnhancedDatabaseMode()
	t.Cleanup(func() {
		datastoreV2.ResetDatabaseMode()
	})

	// 2. Initialize Echo and Core
	e := echo.New()
	core := apitest.NewCore(t, apitest.WithEcho(e))
	core.AuthMiddleware = passthroughMiddleware()
	mgr := &mockV2Manager{db: db}
	core.V2Manager = mgr

	controlChan := make(chan string, 10)
	var callbackCalled bool
	refreshCB := func(ctx context.Context) error {
		callbackCalled = true
		return nil
	}

	handler := New(core, controlChan, refreshCB)
	handler.RegisterRoutes(e.Group("/api/v2"))

	// 3. Test POST /api/v2/species-lists (Create)
	payload := `{"name":"Test List","description":"Test Desc","species":["turdus merula","parus major"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v2/species-lists", bytes.NewBufferString(payload))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	var createResponse struct {
		List     entities.SpeciesList `json:"list"`
		Warnings any                  `json:"warnings,omitempty"`
	}
	err = json.Unmarshal(rec.Body.Bytes(), &createResponse)
	require.NoError(t, err)
	createdList := createResponse.List
	require.Equal(t, "Test List", createdList.Name)
	require.Len(t, createdList.Members, 2)
	require.Equal(t, "turdus merula", createdList.Members[0].ScientificName)
	require.True(t, callbackCalled)

	// Read signal from control channel
	select {
	case sig := <-controlChan:
		require.Equal(t, "rebuild_extended_capture", sig)
	default:
		t.Fatal("expected control signal to be sent")
	}

	// Reset callback flag
	callbackCalled = false

	// 4. Test GET /api/v2/species-lists (List)
	req = httptest.NewRequest(http.MethodGet, "/api/v2/species-lists", http.NoBody)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var listResponse struct {
		Lists []entities.SpeciesList `json:"lists"`
		Count int                   `json:"count"`
	}
	err = json.Unmarshal(rec.Body.Bytes(), &listResponse)
	require.NoError(t, err)
	require.Equal(t, 1, listResponse.Count)
	require.Equal(t, "Test List", listResponse.Lists[0].Name)

	// 5. Test GET /api/v2/species-lists/:id (Get)
	req = httptest.NewRequest(http.MethodGet, "/api/v2/species-lists/1", http.NoBody)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var gotList entities.SpeciesList
	err = json.Unmarshal(rec.Body.Bytes(), &gotList)
	require.NoError(t, err)
	require.Equal(t, "Test List", gotList.Name)

	// 6. Test PUT /api/v2/species-lists/:id (Update)
	updatePayload := `{"name":"Updated Name","description":"Updated Desc","species":["turdus merula","cyanistes caeruleus"]}`
	req = httptest.NewRequest(http.MethodPut, "/api/v2/species-lists/1", bytes.NewBufferString(updatePayload))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var updateResponse struct {
		List     entities.SpeciesList `json:"list"`
		Warnings any                  `json:"warnings,omitempty"`
	}
	err = json.Unmarshal(rec.Body.Bytes(), &updateResponse)
	require.NoError(t, err)
	updatedList := updateResponse.List
	require.Equal(t, "Updated Name", updatedList.Name)
	require.Len(t, updatedList.Members, 2)
	require.Equal(t, "cyanistes caeruleus", updatedList.Members[1].ScientificName)
	require.True(t, callbackCalled)

	// 7. Test DELETE /api/v2/species-lists/:id (Delete)
	req = httptest.NewRequest(http.MethodDelete, "/api/v2/species-lists/1", http.NoBody)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)

	// Verify it's deleted
	req = httptest.NewRequest(http.MethodGet, "/api/v2/species-lists/1", http.NoBody)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	// 8. Create a system list directly in DB
	systemList := entities.SpeciesList{
		ID:          999,
		Name:        "System List",
		Description: "Read-only system list",
		IsSystem:    true,
	}
	require.NoError(t, db.Create(&systemList).Error)

	// Test PUT /api/v2/species-lists/999 fails
	req = httptest.NewRequest(http.MethodPut, "/api/v2/species-lists/999", bytes.NewBufferString(`{"name":"Updated System","description":"foo"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)

	// Test DELETE /api/v2/species-lists/999 fails
	req = httptest.NewRequest(http.MethodDelete, "/api/v2/species-lists/999", http.NoBody)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func passthroughMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			return next(c)
		}
	}
}
