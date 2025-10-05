package notify

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// Command returns a cobra command that sends a test notification via the notification service
func Command(settings *conf.Settings) *cobra.Command {
	var (
		typ       string
		prio      string
		title     string
		message   string
		component string
		wait      time.Duration
		metadata  []string
	)

	cmd := &cobra.Command{
		Use:   "notify",
		Short: "Send a test notification (use filters to trigger push)",
		Long: `Send a test notification through the notification service.

Examples:
  # Basic notification
  birdnet-go notify --type=info --priority=low --title="Test" --message="Hello"
  
  # Notification with confidence metadata for testing confidence filters
  birdnet-go notify --type=detection --metadata="confidence=0.95" --metadata="species=robin"
  
  # Multiple metadata with different types
  birdnet-go notify --metadata="confidence=0.85" --metadata="verified=true" --metadata="location=backyard"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Map type
			var ntype notification.Type
			switch typ {
			case "error":
				ntype = notification.TypeError
			case "warning":
				ntype = notification.TypeWarning
			case "info":
				ntype = notification.TypeInfo
			case "detection":
				ntype = notification.TypeDetection
			case "system":
				ntype = notification.TypeSystem
			default:
				return fmt.Errorf("invalid type: %s", typ)
			}

			// Map priority
			var nprio notification.Priority
			switch prio {
			case "critical":
				nprio = notification.PriorityCritical
			case "high":
				nprio = notification.PriorityHigh
			case "medium":
				nprio = notification.PriorityMedium
			case "low":
				nprio = notification.PriorityLow
			default:
				return fmt.Errorf("invalid priority: %s", prio)
			}

			service := notification.GetService()
			if service == nil {
				return fmt.Errorf("notification service not initialized")
			}

			// Parse metadata if provided
			metadataMap := make(map[string]any)
			for _, kv := range metadata {
				parts := strings.SplitN(kv, "=", 2)
				if len(parts) != 2 {
					return fmt.Errorf("invalid metadata format: %s (expected key=value)", kv)
				}
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])

				// Try to parse as number (float64), then boolean, otherwise keep as string
				if floatVal, err := strconv.ParseFloat(value, 64); err == nil {
					metadataMap[key] = floatVal
				} else if boolVal, err := strconv.ParseBool(value); err == nil {
					metadataMap[key] = boolVal
				} else {
					metadataMap[key] = value
				}
			}

			// Create notification with metadata if provided, otherwise use simple method
			var n *notification.Notification
			var err error

			if len(metadataMap) > 0 {
				// Create notification manually and use CreateWithMetadata
				n = notification.NewNotification(ntype, nprio, title, message)
				n.Component = component
				n.Metadata = metadataMap
				err = service.CreateWithMetadata(n)
			} else {
				// Use existing simple method
				n, err = service.CreateWithComponent(ntype, nprio, title, message, component)
			}

			if err != nil {
				return fmt.Errorf("failed to create notification: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Notification sent: id=%s type=%s priority=%s", n.ID, n.Type, n.Priority)
			if len(n.Metadata) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), " metadata=%d_keys", len(n.Metadata))
			}
			fmt.Fprintln(cmd.OutOrStdout())
			if wait > 0 {
				time.Sleep(wait)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&typ, "type", "info", "Notification type: error|warning|info|detection|system")
	cmd.Flags().StringVar(&prio, "priority", "low", "Notification priority: critical|high|medium|low")
	cmd.Flags().StringVar(&title, "title", "Test Notification", "Notification title")
	cmd.Flags().StringVar(&message, "message", "This is a test push notification", "Notification message")
	cmd.Flags().StringVar(&component, "component", "cli", "Notification component tag")
	cmd.Flags().DurationVar(&wait, "wait", 2*time.Second, "Time to wait for push delivery (0 to disable)")
	cmd.Flags().StringSliceVar(&metadata, "metadata", nil, "Metadata key-value pairs in format key=value (supports numbers, booleans, and strings)")

	return cmd
}
