package notification

import (
	"context"
	"fmt"
	"io"
	"log"
	"slices"
	"strings"
	"time"

	shoutrrr "github.com/nicholas-fedor/shoutrrr"
	router "github.com/nicholas-fedor/shoutrrr/pkg/router"
	stypes "github.com/nicholas-fedor/shoutrrr/pkg/types"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// shoutrrrDefaultTimeout is applied when no per-provider timeout is configured,
// preventing indefinite hangs on unresponsive servers.
const shoutrrrDefaultTimeout = 30 * time.Second

// ShoutrrrProvider sends via nicholas-fedor/shoutrrr
// Creates a single sender for multiple URLs.
type ShoutrrrProvider struct {
	name    string
	enabled bool
	urls    []string
	types   map[string]bool
	sender  *router.ServiceRouter
	timeout time.Duration
}

func NewShoutrrrProvider(name string, enabled bool, urls, supportedTypes []string, timeout time.Duration) *ShoutrrrProvider {
	sp := &ShoutrrrProvider{
		name:    strings.TrimSpace(name),
		enabled: enabled,
		urls:    slices.Clone(urls),
		types:   map[string]bool{},
		timeout: timeout,
	}
	if sp.name == "" {
		sp.name = "shoutrrr"
	}
	if len(supportedTypes) == 0 {
		sp.types["error"] = true
		sp.types["warning"] = true
		sp.types["info"] = true
		sp.types["detection"] = true
		sp.types["system"] = true
	} else {
		for _, t := range supportedTypes {
			sp.types[t] = true
		}
	}
	return sp
}

func (s *ShoutrrrProvider) GetName() string          { return s.name }
func (s *ShoutrrrProvider) IsEnabled() bool          { return s.enabled }
func (s *ShoutrrrProvider) SupportsType(t Type) bool { return s.types[string(t)] }

func (s *ShoutrrrProvider) ValidateConfig() error {
	if !s.enabled {
		return nil
	}
	if len(s.urls) == 0 {
		return fmt.Errorf("at least one URL is required")
	}
	// Build sender to validate URLs
	sender, err := shoutrrr.CreateSender(s.urls...)
	if err != nil {
		// Wrap error to sanitize any URLs that may contain tokens/credentials
		return privacy.WrapError(err)
	}
	s.sender = sender
	// Apply configured timeout; fall back to a sane default to prevent indefinite hangs
	if s.timeout > 0 {
		s.sender.Timeout = s.timeout
	} else {
		s.sender.Timeout = shoutrrrDefaultTimeout
	}
	s.sender.SetLogger(log.New(io.Discard, "", 0))
	return nil
}

func (s *ShoutrrrProvider) Send(ctx context.Context, n *Notification) error {
	if s.sender == nil {
		return fmt.Errorf("shoutrrr sender not initialized")
	}
	_ = ctx // router handles its own timeouts

	body := n.Message
	params := stypes.Params{}
	if n.Title != "" {
		params.SetTitle(n.Title)
	}
	errs := s.sender.Send(body, &params)
	if len(errs) > 0 {
		var firstErr error
		for _, e := range errs {
			if e != nil {
				firstErr = e
				break
			}
		}
		if firstErr != nil {
			// Wrap error to sanitize any URLs that may contain tokens/credentials
			return privacy.WrapError(firstErr)
		}
	}
	return nil
}
