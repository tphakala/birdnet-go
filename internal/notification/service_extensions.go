package notification

import (
	"fmt"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// CreateWithMetadata creates a notification with full metadata support
// This is useful for creating notifications that need custom metadata like toast notifications
func (s *Service) CreateWithMetadata(notification *Notification) error {
	if notification == nil {
		return errors.New(fmt.Errorf("notification cannot be nil")).
			Component("notification").
			Category(errors.CategorySystem).
			Build()
	}

	// Check rate limit
	if !s.rateLimiter.Allow() {
		if s.config.Debug {
			s.logger.Debug("notification rate limit exceeded",
				"type", notification.Type,
				"priority", notification.Priority,
				"title_length", len(notification.Title))
		}
		return errors.Newf("rate limit exceeded").
			Component("notification").
			Category(errors.CategorySystem).
			Build()
	}

	if s.config.Debug {
		s.logger.Debug("creating notification with metadata",
			"id", notification.ID,
			"type", notification.Type,
			"priority", notification.Priority,
			"title_length", len(notification.Title),
			"message_length", len(notification.Message),
			"metadata_keys", len(notification.Metadata))
	}

	// Save to store
	if err := s.store.Save(notification); err != nil {
		return errors.New(err).
			Component("notification").
			Category(errors.CategorySystem).
			Context("operation", "save_notification").
			Build()
	}

	// Broadcast to subscribers
	s.broadcast(notification)

	if s.config.Debug {
		s.logger.Debug("notification created and broadcast",
			"id", notification.ID,
			"subscriber_count", len(s.subscribers))
	}

	return nil
}