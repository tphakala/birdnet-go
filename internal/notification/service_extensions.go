package notification

import (
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// CreateWithMetadata creates a notification with full metadata support
// This is useful for creating notifications that need custom metadata like toast notifications
func (s *Service) CreateWithMetadata(notification *Notification) error {
	if notification == nil {
		return errors.Newf("notification cannot be nil").
			Component("notification").
			Category(errors.CategorySystem).
			Build()
	}

	// Check rate limit
	if !s.rateLimiter.Allow() {
		if s.config.Debug {
			s.logger.Debug("notification rate limit exceeded",
				logger.String("type", string(notification.Type)),
				logger.String("priority", string(notification.Priority)),
				logger.Int("title_length", len(notification.Title)))
		}
		return errors.Newf("rate limit exceeded").
			Component("notification").
			Category(errors.CategorySystem).
			Build()
	}

	if s.config.Debug {
		s.logger.Debug("creating notification with metadata",
			logger.String("id", notification.ID),
			logger.String("type", string(notification.Type)),
			logger.String("priority", string(notification.Priority)),
			logger.Int("title_length", len(notification.Title)),
			logger.Int("message_length", len(notification.Message)),
			logger.Int("metadata_keys", len(notification.Metadata)))
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
			logger.String("id", notification.ID),
			logger.Int("subscriber_count", len(s.subscribers)))
	}

	return nil
}
