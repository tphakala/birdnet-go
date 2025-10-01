package notify

import (
	"fmt"
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
	)

	cmd := &cobra.Command{
		Use:   "notify",
		Short: "Send a test notification (use filters to trigger push)",
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

			n, err := service.CreateWithComponent(ntype, nprio, title, message, component)
			if err != nil {
				return fmt.Errorf("failed to create notification: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Notification sent: id=%s type=%s priority=%s\n", n.ID, n.Type, n.Priority)
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

	return cmd
}
