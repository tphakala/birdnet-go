package notification

import (
	"testing"
)

// TestUpdateNotificationFieldsMetadataDeepCopy tests that metadata is deep copied
func TestUpdateNotificationFieldsMetadataDeepCopy(t *testing.T) {
	t.Parallel()

	store := NewInMemoryStore(100)

	// Create and save original notification with metadata
	original := NewNotification(TypeError, PriorityMedium, "Test", "Message").
		WithComponent("test")
	original.Metadata = map[string]any{
		"key1": "value1",
		"key2": 42,
	}
	
	_, err := store.Save(original)
	if err != nil {
		t.Fatalf("Failed to save original notification: %v", err)
	}

	// Create update notification with different metadata
	update := NewNotification(TypeError, PriorityMedium, "Test", "Message").
		WithComponent("test")
	update.ID = original.ID  // Use same ID to update
	update.Metadata = map[string]any{
		"key3": "value3",
		"key4": 84,
	}

	// Update the notification
	err = store.Update(update)
	if err != nil {
		t.Fatalf("Failed to update notification: %v", err)
	}

	// Modify the source metadata after update
	update.Metadata["key5"] = "should not appear"
	delete(update.Metadata, "key3")

	// Get the stored notification
	stored, err := store.Get(original.ID)
	if err != nil {
		t.Fatalf("Failed to get notification: %v", err)
	}

	// Verify the stored metadata is isolated from changes to source
	if len(stored.Metadata) != 2 {
		t.Errorf("Expected 2 metadata entries, got %d", len(stored.Metadata))
	}

	// Check that original keys were replaced
	if _, exists := stored.Metadata["key1"]; exists {
		t.Error("Original metadata key1 should have been replaced")
	}

	// Check that update keys exist
	if val, exists := stored.Metadata["key3"]; !exists || val != "value3" {
		t.Errorf("Expected key3=value3, got %v (exists: %v)", val, exists)
	}
	if val, exists := stored.Metadata["key4"]; !exists || val != 84 {
		t.Errorf("Expected key4=84, got %v (exists: %v)", val, exists)
	}

	// Check that post-update modifications didn't affect stored metadata
	if _, exists := stored.Metadata["key5"]; exists {
		t.Error("Post-update modification should not affect stored metadata")
	}
}

// TestUpdateNotificationFieldsNilMetadata tests handling of nil metadata
func TestUpdateNotificationFieldsNilMetadata(t *testing.T) {
	t.Parallel()

	store := NewInMemoryStore(100)

	// Create notification with metadata
	notif := NewNotification(TypeError, PriorityMedium, "Test", "Message")
	notif.Metadata = map[string]any{"key": "value"}
	
	_, err := store.Save(notif)
	if err != nil {
		t.Fatalf("Failed to save notification: %v", err)
	}

	// Update with nil metadata
	update := NewNotification(TypeError, PriorityMedium, "Test", "Message")
	update.ID = notif.ID
	update.Metadata = nil

	err = store.Update(update)
	if err != nil {
		t.Fatalf("Failed to update notification: %v", err)
	}

	// Verify metadata is now nil
	stored, err := store.Get(notif.ID)
	if err != nil {
		t.Fatalf("Failed to get notification: %v", err)
	}

	if stored.Metadata != nil {
		t.Errorf("Expected nil metadata, got %v", stored.Metadata)
	}
}