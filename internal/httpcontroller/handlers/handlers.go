package handlers

import (
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
	"github.com/tphakala/birdnet-go/internal/securefs"
	"github.com/tphakala/birdnet-go/internal/security"
	"github.com/tphakala/birdnet-go/internal/serviceapi"
	"github.com/tphakala/birdnet-go/internal/spectrogram"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

var settingsMutex sync.RWMutex

// Handlers contains all the handler functions and their dependencies
type Handlers struct {
	baseHandler
	DS                   datastore.Interface
	Settings             *conf.Settings
	DashboardSettings    *conf.Dashboard
	BirdImageCache       *imageprovider.BirdImageCache
	SSE                  *SSEHandler                 // Server Side Events handler
	SunCalc              *suncalc.SunCalc            // SunCalc instance for calculating sun event times
	AudioLevelChan       chan myaudio.AudioLevelData // Channel for audio level updates
	OAuth2Server         *security.OAuth2Server
	controlChan          chan string
	notificationChan     chan Notification
	debug                bool
	Server               serviceapi.ServerFacade // Server facade providing security and processor access
	Telemetry            *TelemetryMiddleware    // Telemetry middleware for metrics and enhanced error handling
	Metrics              *observability.Metrics  // Shared metrics instance
	rtspAnonymMap        map[string]string       // Maps source IDs to anonymized names for O(1) lookups
	spectrogramGenerator *spectrogram.Generator  // Shared spectrogram generator
}

// HandlerError is a custom error type that includes an HTTP status code and a user-friendly message.
type HandlerError struct {
	Err     error
	Message string
	Code    int
}

// Error implements the error interface for HandlerError.
func (e *HandlerError) Error() string {
	return fmt.Sprintf("%s: %v", e.Message, e.Err)
}

// baseHandler provides common functionality for all handlers.
type baseHandler struct {
	errorHandler func(error) *HandlerError
	logger       *log.Logger
}

// newHandlerError creates a new HandlerError with the given parameters.
func (bh *baseHandler) NewHandlerError(err error, message string, code int) *HandlerError {
	handlerErr := &HandlerError{
		Err:     err,
		Message: fmt.Sprintf("%s: %v", message, err),
		Code:    code,
	}
	bh.logError(handlerErr)
	return handlerErr
}

// logError logs an error message.
func (bh *baseHandler) logError(err *HandlerError) {
	bh.logger.Printf("Error: %s (Code: %d, Underlying error: %v)", err.Message, err.Code, err.Err)
}

// logInfo logs an info message.
func (bh *baseHandler) logInfo(message string) {
	bh.logger.Printf("Info: %s", message)
}

// New creates a new Handlers instance with the given dependencies.
func New(ds datastore.Interface, settings *conf.Settings, dashboardSettings *conf.Dashboard, birdImageCache *imageprovider.BirdImageCache, logger *log.Logger, sunCalc *suncalc.SunCalc, audioLevelChan chan myaudio.AudioLevelData, oauth2Server *security.OAuth2Server, controlChan chan string, notificationChan chan Notification, server serviceapi.ServerFacade, httpMetrics *metrics.HTTPMetrics, metricsInstance *observability.Metrics) *Handlers {
	if logger == nil {
		logger = log.New(os.Stderr, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
	}

	// Build RTSP anonymization map for O(1) lookups
	rtspAnonymMap := buildRTSPAnonymizationMap(settings)

	// Initialize SecureFS for spectrogram generation
	exportPath := settings.Realtime.Audio.Export.Path
	sfs, err := securefs.New(exportPath)
	if err != nil {
		// SecureFS is essential for secure path validation - fail fast
		logger.Fatalf("Failed to initialize SecureFS for spectrograms: %v. Export path: %s", err, exportPath)
	}

	// Initialize spectrogram generator with shared generation logic
	spectrogramLogger := slog.Default().With("service", "htmx_spectrogram_generator")
	spectrogramGen := spectrogram.NewGenerator(settings, sfs, spectrogramLogger)

	return &Handlers{
		baseHandler: baseHandler{
			errorHandler: defaultErrorHandler,
			logger:       logger,
		},
		DS:                   ds,
		Settings:             settings,
		DashboardSettings:    dashboardSettings,
		BirdImageCache:       birdImageCache,
		SSE:                  NewSSEHandler(),
		SunCalc:              sunCalc,
		AudioLevelChan:       audioLevelChan,
		OAuth2Server:         oauth2Server,
		controlChan:          controlChan,
		notificationChan:     notificationChan,
		debug:                settings.Debug,
		Server:               server,
		Telemetry:            NewTelemetryMiddleware(httpMetrics),
		Metrics:              metricsInstance,
		rtspAnonymMap:        rtspAnonymMap,
		spectrogramGenerator: spectrogramGen,
	}
}

// buildRTSPAnonymizationMap creates a lookup map for O(1) RTSP URL anonymization
// Maps source IDs to their anonymized names (camera-1, camera-2, etc.)
func buildRTSPAnonymizationMap(settings *conf.Settings) map[string]string {
	anonymMap := make(map[string]string)

	if settings == nil || len(settings.Realtime.RTSP.URLs) == 0 {
		return anonymMap
	}

	// Get the audio source registry to map URLs to source IDs
	registry := myaudio.GetRegistry()
	if registry == nil {
		return anonymMap
	}

	// Build the mapping from source ID to anonymized name
	for i, url := range settings.Realtime.RTSP.URLs {
		if url == "" {
			continue
		}

		// Get the source from registry to get its ID
		if source, exists := registry.GetSourceByConnection(url); exists {
			anonymizedName := fmt.Sprintf("camera-%d", i+1)
			anonymMap[source.ID] = anonymizedName
		}
	}

	return anonymMap
}

// defaultErrorHandler is the default implementation of error handling.
func defaultErrorHandler(err error) *HandlerError {
	var he *HandlerError
	if errors.As(err, &he) {
		return he
	}
	return &HandlerError{
		Err:     err,
		Message: "An unexpected error occurred",
		Code:    http.StatusInternalServerError,
	}
}

// HandleError is a utility method to handle errors in Echo handlers.
func (h *Handlers) HandleError(err error, c echo.Context) error {
	var he *HandlerError
	var echoHTTPError *echo.HTTPError
	var enhancedErr *errors.EnhancedError

	switch {
	case errors.As(err, &enhancedErr):
		// Handle enhanced errors with better categorization
		code := h.mapCategoryToHTTPStatus(enhancedErr.GetCategory())
		he = &HandlerError{
			Err:     enhancedErr,
			Message: enhancedErr.Error(),
			Code:    code,
		}
	case errors.As(err, &echoHTTPError):
		he = &HandlerError{
			Err:     echoHTTPError,
			Message: fmt.Sprintf("%v", echoHTTPError.Message),
			Code:    echoHTTPError.Code,
		}
	case errors.As(err, &he):
		// It's already a HandlerError, use it as is
	default:
		// For any other error, treat it as an internal server error
		he = &HandlerError{
			Err:     err,
			Message: "An unexpected error occurred",
			Code:    http.StatusInternalServerError,
		}
	}

	// Check if headers have already been sent
	if c.Response().Committed {
		return nil
	}

	// Get security state for the current context
	securityState := h.GetSecurity(c)

	// Prepare error data with consistent structure across all error templates
	errorData := struct {
		Code       int
		Title      string
		Message    string
		StackTrace string
		Settings   *conf.Settings
		Security   *Security // Use the Security type from handlers package
		User       any
		Debug      bool
	}{
		Code:       he.Code,
		Title:      fmt.Sprintf("%d Error", he.Code),
		Message:    he.Message,
		StackTrace: string(debug.Stack()),
		Settings:   h.Settings,
		Security:   securityState, // Pass the security state
		User:       c.Get("user"),
		Debug:      h.Settings.Debug,
	}

	// Choose the appropriate template based on the error code
	template := h.getErrorTemplate(he.Code)

	// Set the response status code
	c.Response().Status = he.Code

	// Record template rendering metrics
	if h.Telemetry != nil {
		renderStart := time.Now()
		renderErr := c.Render(he.Code, template, errorData)
		h.Telemetry.RecordTemplateRender(template, time.Since(renderStart), renderErr)
		return renderErr
	}

	// Render the template without telemetry
	return c.Render(he.Code, template, errorData)
}

// mapCategoryToHTTPStatus maps error categories to appropriate HTTP status codes
func (h *Handlers) mapCategoryToHTTPStatus(category string) int {
	switch category {
	case string(errors.CategoryValidation):
		return http.StatusBadRequest
	case string(errors.CategoryDatabase):
		return http.StatusInternalServerError
	case string(errors.CategoryNetwork):
		return http.StatusBadGateway
	case string(errors.CategoryFileIO):
		return http.StatusInternalServerError
	case string(errors.CategoryConfiguration):
		return http.StatusInternalServerError
	case string(errors.CategorySystem):
		return http.StatusInternalServerError
	case string(errors.CategoryImageFetch), string(errors.CategoryImageCache), string(errors.CategoryImageProvider):
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

// getErrorTemplate returns the appropriate error template name based on the error code
func (h *Handlers) getErrorTemplate(code int) string {
	switch code {
	case http.StatusNotFound:
		return "error-404"
	case http.StatusInternalServerError:
		return "error-500"
	default:
		return "error-default" // Use the correct template name
	}
}

// WithErrorHandling wraps an Echo handler function with error handling.
func (h *Handlers) WithErrorHandling(fn func(echo.Context) error) echo.HandlerFunc {
	return func(c echo.Context) error {
		err := fn(c)
		if err != nil {
			return h.HandleError(err, c)
		}
		return nil
	}
}

// GetLabels returns the list of all available species labels
func (h *Handlers) GetLabels() []string {
	return h.Settings.BirdNET.Labels
}

// GetBirdNET returns the BirdNET instance from the processor
func (h *Handlers) GetBirdNET() *birdnet.BirdNET {
	if h.Server == nil {
		if h.debug {
			h.logInfo("GetBirdNET: Server reference is nil - cannot access processor")
		}
		return nil
	}

	// Get the processor using the Server interface
	processor := h.Server.GetProcessor()
	if processor == nil {
		if h.debug {
			h.logInfo("GetBirdNET: Processor reference is nil - cannot access BirdNET instance")
		}
		return nil
	}

	// Get BirdNET using the processor
	return processor.GetBirdNET()
}

// Security represents the authentication and access control state
type Security struct {
	Enabled       bool
	AccessAllowed bool
}

// GetSecurity returns the current security state for the context
func (h *Handlers) GetSecurity(c echo.Context) *Security {
	var accessAllowed bool
	if h.Server != nil {
		accessAllowed = h.Server.IsAccessAllowed(c)
	} else if h.debug {
		h.logInfo("GetSecurity: Server reference is nil - defaulting to access denied")
	}

	return &Security{
		Enabled:       h.Settings.Security.BasicAuth.Enabled || h.Settings.Security.GoogleAuth.Enabled || h.Settings.Security.GithubAuth.Enabled,
		AccessAllowed: accessAllowed,
	}
}
