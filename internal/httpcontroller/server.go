// internal/httpcontroller/server.go
package httpcontroller

import (
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	echolog "github.com/labstack/gommon/log"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/api/v2"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/httpcontroller/handlers"
	"github.com/tphakala/birdnet-go/internal/securefs"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability"
	obsmetrics "github.com/tphakala/birdnet-go/internal/observability/metrics"
	runtimectx "github.com/tphakala/birdnet-go/internal/buildinfo"
	"github.com/tphakala/birdnet-go/internal/security"
	"github.com/tphakala/birdnet-go/internal/serviceapi"
	"github.com/tphakala/birdnet-go/internal/suncalc"
	"golang.org/x/crypto/acme/autocert"
)

// Server encapsulates Echo server and related configurations.
type Server struct {
	Echo              *echo.Echo
	DS                datastore.Interface
	Settings          *conf.Settings
	OAuth2Server      *security.OAuth2Server
	DashboardSettings *conf.Dashboard
	BirdImageCache    *imageprovider.BirdImageCache
	Handlers          *handlers.Handlers
	SunCalc           *suncalc.SunCalc
	AudioLevelChan    chan myaudio.AudioLevelData
	controlChan       chan string
	notificationChan  chan handlers.Notification
	Processor         *processor.Processor
	APIV2             *api.Controller // Our new JSON API
	RuntimeContext    *runtimectx.Context // Build-time metadata
	metrics           *observability.Metrics

	// Page and partial routes
	pageRoutes    map[string]PageRouteConfig
	partialRoutes map[string]PartialRouteConfig

	// New structured loggers
	webLogger      *slog.Logger // Structured logger for web operations
	webLoggerClose func() error // Function to close the log file
}

// New initializes a new HTTP server with given context and datastore.
func New(settings *conf.Settings, runtimeContext *runtimectx.Context, dataStore datastore.Interface, birdImageCache *imageprovider.BirdImageCache, audioLevelChan chan myaudio.AudioLevelData, controlChan chan string, proc *processor.Processor, observabilityMetrics *observability.Metrics) *Server {
	configureDefaultSettings(settings)

	s := &Server{
		Echo:              echo.New(),
		DS:                dataStore,
		Settings:          settings,
		RuntimeContext:    runtimeContext,
		BirdImageCache:    birdImageCache,
		AudioLevelChan:    audioLevelChan,
		DashboardSettings: &settings.Realtime.Dashboard,
		OAuth2Server:      security.NewOAuth2Server(),
		controlChan:       controlChan,
		notificationChan:  make(chan handlers.Notification, 10),
		Processor:         proc,
		metrics:           observabilityMetrics,
	}

	// Configure an IP extractor
	s.Echo.IPExtractor = echo.ExtractIPFromXFFHeader()

	// Initialize SunCalc for calculating sun event times
	s.SunCalc = suncalc.NewSunCalc(settings.BirdNET.Latitude, settings.BirdNET.Longitude)

	// Initialize handlers with metrics
	var httpMetrics *obsmetrics.HTTPMetrics
	if observabilityMetrics != nil {
		httpMetrics = observabilityMetrics.HTTP
	}
	s.Handlers = handlers.New(s.DS, s.Settings, s.DashboardSettings, s.BirdImageCache, nil, s.SunCalc, s.AudioLevelChan, s.OAuth2Server, s.controlChan, s.notificationChan, s, httpMetrics, observabilityMetrics)

	// Add processor middleware
	s.Echo.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set("processor", s.Processor)
			return next(c)
		}
	})

	s.initializeServer()
	return s
}

// Start begins listening and serving HTTP requests.
func (s *Server) Start() {
	errChan := make(chan error)

	go func() {
		var err error

		if s.Settings.Security.AutoTLS {
			// Validate AutoTLS environment before starting
			if validationErr := s.validateAutoTLSEnvironment(); validationErr != nil {
				s.LogError(nil, validationErr, "AutoTLS validation failed")
				log.Printf("AutoTLS validation failed: %v", validationErr)
				log.Println("AutoTLS has been disabled. Starting HTTP server on configured port.")
				s.Settings.Security.AutoTLS = false
				err = s.Echo.Start(":" + s.Settings.WebServer.Port)
			} else {
				// AutoTLS requires standard HTTPS ports
				configPaths, configErr := conf.GetDefaultConfigPaths()
				if configErr != nil {
					errChan <- fmt.Errorf("failed to get config paths: %w", configErr)
					return
				}

				// Start HTTP server on port 80 for ACME challenges in a separate goroutine
				go func() {
					httpServer := echo.New()
					httpServer.HideBanner = true
					// Redirect all HTTP traffic to HTTPS
					httpServer.Pre(middleware.HTTPSRedirect())

					s.Debug("Starting HTTP redirect server on port 80 for ACME challenges")
					if httpErr := httpServer.Start(":80"); httpErr != nil {
						log.Printf("Failed to start HTTP redirect server on port 80: %v", httpErr)
						// This is not fatal - AutoTLS can work with DNS challenges
					}
				}()

				// Configure AutoTLS manager
				s.Echo.AutoTLSManager.Prompt = autocert.AcceptTOS
				s.Echo.AutoTLSManager.Cache = autocert.DirCache(configPaths[0])
				s.Echo.AutoTLSManager.HostPolicy = autocert.HostWhitelist(s.Settings.Security.Host)

				// Start HTTPS server on port 443
				s.Debug("Starting HTTPS server with AutoTLS on port 443")
				err = s.Echo.StartAutoTLS(":443")
			}
		} else {
			err = s.Echo.Start(":" + s.Settings.WebServer.Port)
		}

		if err != nil {
			errChan <- err
		}
	}()

	go handleServerError(errChan)

	if s.Settings.Security.AutoTLS {
		fmt.Printf("HTTPS server started with AutoTLS on ports 80 (redirect) and 443 (secure)\n")
		fmt.Printf("Domain: %s\n", s.Settings.Security.Host)
	} else {
		fmt.Printf("HTTP server started on port %s\n", s.Settings.WebServer.Port)
	}
}

func (s *Server) isAuthenticationEnabled(c echo.Context) bool {
	return s.Handlers.OAuth2Server.IsAuthenticationEnabled(s.RealIP(c))
}

// IsAccessAllowed checks if a user is authenticated based on the context
func (s *Server) IsAccessAllowed(c echo.Context) bool {
	return s.OAuth2Server.IsUserAuthenticated(c)
}

func (s *Server) RealIP(c echo.Context) string {
	// Get the X-Forwarded-For header
	if xff := c.Request().Header.Get("X-Forwarded-For"); xff != "" {
		// Split and get the first IP in the chain
		ips := strings.Split(xff, ",")
		if len(ips) > 0 {
			// Take the last IP which should be the original client IP
			return strings.TrimSpace(ips[len(ips)-1])
		}
	}

	// Fallback to direct RemoteAddr
	ip, _, _ := net.SplitHostPort(c.Request().RemoteAddr)

	// If we're running in a container and the client appears to be localhost,
	// try to resolve the actual host IP
	if conf.RunningInContainer() && (ip == "127.0.0.1" || ip == "::1" || ip == "localhost") {
		// Try to get the host IP
		hostIP, err := conf.GetHostIP()
		if err == nil && hostIP != nil {
			return hostIP.String()
		}
	}

	return ip
}

// initializeServer configures and initializes the server.
func (s *Server) initializeServer() {
	s.Echo.HideBanner = true
	s.initLogger()

	s.configureMiddleware() // Configure other standard middleware
	s.initRoutes()          // Initialize HTML/V1 routes
	s.initHLSCleanupTask()  // Initialize HLS cleanup task

	// Initialize the JSON API v2 - Pass OAuth2Server directly
	s.Debug("Initializing JSON API v2")
	
	var err error
	s.APIV2, err = api.InitializeAPI(
		s.Echo,
		s.DS,
		s.Settings,
		s.RuntimeContext,
		s.BirdImageCache,
		s.SunCalc,
		s.controlChan,
		log.Default(),
		s.Processor,
		s.OAuth2Server, // Pass OAuth2Server instance
		s.metrics,      // Pass observability metrics
	)
	if err != nil {
		s.LogError(nil, err, "Failed to initialize API v2")
		log.Fatalf("Failed to initialize API v2: %v", err)
	}

	// Connect the processor's SSE broadcaster to the API controller's SSE manager
	s.Debug("Setting up SSE broadcaster connection")
	if s.Processor != nil && s.APIV2 != nil {
		s.Processor.SetSSEBroadcaster(s.APIV2.BroadcastDetection)
		s.Debug("SSE broadcaster connected to processor")
	}
}

// initHLSCleanupTask initializes a background task to clean up idle HLS streams
func (s *Server) initHLSCleanupTask() {
	s.Debug("Initializing HLS stream cleanup task")

	// Run cleanup every 5 minutes
	ticker := time.NewTicker(5 * time.Minute)

	go func() {
		// Do initial cleanup after a short delay
		time.Sleep(1 * time.Minute)
		s.Debug("Running initial HLS stream cleanup")
		s.Handlers.CleanupIdleHLSStreams()

		for range ticker.C {
			s.Debug("Running scheduled HLS stream cleanup")
			s.Handlers.CleanupIdleHLSStreams()
		}
	}()

	// Ensure ticker is stopped on server shutdown
	s.Echo.Server.RegisterOnShutdown(func() {
		s.Debug("Stopping HLS cleanup task")
		ticker.Stop()
	})
}

// configureDefaultSettings sets default values for server settings.
func configureDefaultSettings(settings *conf.Settings) {
	if settings.WebServer.Port == "" {
		settings.WebServer.Port = "8080"
	}
}

// handleServerError listens for server errors and handles them.
func handleServerError(errChan chan error) {
	for err := range errChan {
		log.Printf("Server error: %v", err)
		// Additional error handling logic here
	}
}

// initLogger initializes the custom logger.
func (s *Server) initLogger() {
	if !s.Settings.WebServer.Log.Enabled {
		fmt.Println("Logging disabled")
		return
	}

	// Initialize structured logger for web requests (using slog)
	webLogPath := "logs/web.log"
	webLevelVar := new(slog.LevelVar) // Create LevelVar
	initialLevel := slog.LevelInfo    // Set desired initial level
	webLevelVar.Set(initialLevel)

	webLogger, closeFunc, err := logging.NewFileLogger(webLogPath, "web", webLevelVar)
	if err != nil {
		log.Printf("Warning: Failed to initialize web structured logger: %v", err)
		// Fallback to a disabled logger (writes to io.Discard) but respects the level var
		log.Printf("Web server falling back to a disabled logger due to initialization error.")
		fbHandler := slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: webLevelVar})
		s.webLogger = slog.New(fbHandler).With("service", "web") // Assign to s.webLogger
		s.webLoggerClose = func() error { return nil }           // Provide a no-op closer for fallback
	} else {
		s.webLogger = webLogger
		s.webLoggerClose = closeFunc
		log.Printf("Web structured logging initialized to %s", webLogPath)
	}

	// Replace Echo's default logger output ONLY if our structured logger is available
	if s.webLogger != nil {
		s.Echo.Logger.SetOutput(io.Discard) // Discard Echo's default log output, rely on middleware
		s.Echo.Logger.SetLevel(echolog.OFF) // Use the constant via the alias
	}
}

// Debug logs debug messages if debug mode is enabled
func (s *Server) Debug(format string, v ...interface{}) {
	if s.Settings.WebServer.Debug {
		// Original debug logging (keep for backward compatibility)
		switch len(v) {
		case 0:
			log.Print(format)
		default:
			log.Printf(format, v...)
		}

		// Also log to structured logger if available
		if s.webLogger != nil {
			// Format the message if arguments are provided
			var msg string
			switch len(v) {
			case 0:
				msg = format
			default:
				msg = fmt.Sprintf(format, v...)
			}
			s.webLogger.Debug(msg)
		}
	}
}

// Shutdown performs cleanup operations and gracefully stops the server
func (s *Server) Shutdown() error {
	// Run one final cleanup of HLS streams to terminate all streaming processes
	s.Debug("Running final HLS stream cleanup before shutdown")
	s.Handlers.CleanupIdleHLSStreams()

	// Close the web logger if it was initialized
	if s.webLoggerClose != nil {
		if err := s.webLoggerClose(); err != nil {
			log.Printf("Error closing web log file: %v", err)
		}
	}

	// Close all named-pipe handles created at startup
	securefs.CleanupNamedPipes()

	// Gracefully shutdown the server
	return s.Echo.Close()
}

// LogError logs an error with structured information
func (s *Server) LogError(c echo.Context, err error, message string) {
	// Continue using the old logger (for backward compatibility)
	log.Printf("ERROR: %s: %v", message, err)

	// If the new logger is available, use it for enhanced structured logging
	if s.webLogger != nil {
		// Extract request information
		req := c.Request()

		// Get client IP
		ip := s.RealIP(c)

		// Add additional error context
		s.webLogger.Error("Error",
			"message", message,
			"error", err.Error(),
			"path", req.URL.Path,
			"method", req.Method,
			"ip", ip,
			"user_agent", req.UserAgent(),
		)
	}
}

// LogRequest logs details about an HTTP request with structured information
func (s *Server) LogRequest(c echo.Context, message string, level slog.Level, additionalAttrs ...any) {
	// Skip if structured logger is not available
	if s.webLogger == nil {
		return
	}

	// Extract request information
	req := c.Request()
	res := c.Response()

	// Get client IP
	ip := s.RealIP(c)

	// Base attributes for all request logs
	attrs := []any{
		"method", req.Method,
		"path", req.URL.Path,
		"query", req.URL.RawQuery,
		"status", res.Status,
		"ip", ip,
		"user_agent", req.UserAgent(),
	}

	// Add any additional attributes
	attrs = append(attrs, additionalAttrs...)

	// Log at the appropriate level
	switch level {
	case slog.LevelDebug:
		s.webLogger.Debug(message, attrs...)
	case slog.LevelInfo:
		s.webLogger.Info(message, attrs...)
	case slog.LevelWarn:
		s.webLogger.Warn(message, attrs...)
	case slog.LevelError:
		s.webLogger.Error(message, attrs...)
	default:
		// Default to Info level
		s.webLogger.Info(message, attrs...)
	}
}

// LoggingMiddleware creates a middleware function that logs HTTP requests
// with detailed structured information, similar to the API v2 logging middleware
func (s *Server) LoggingMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(ctx echo.Context) error {
			// Skip if structured logger is not available
			if s.webLogger == nil {
				return next(ctx)
			}

			// Record the start time to calculate latency
			start := time.Now()

			// Process the request
			err := next(ctx)

			// Now log the completed request with timing information
			req := ctx.Request()
			res := ctx.Response()

			// Get client IP
			ip := s.RealIP(ctx)

			// Base attributes for request logs
			attrs := []any{
				"method", req.Method,
				"path", req.URL.Path,
				"query", req.URL.RawQuery,
				"status", res.Status,
				"ip", ip,
				"user_agent", req.UserAgent(),
				"latency_ms", time.Since(start).Milliseconds(),
				"bytes_out", res.Size,
			}

			// Add error info if there was an error and log at appropriate level
			switch {
			case err != nil || res.Status >= 500: // Error or 5xx status -> Error level
				if err != nil {
					attrs = append(attrs, "error", err.Error())
				} else {
					// Optionally add a generic error message for 5xx without explicit error
					attrs = append(attrs, "error", fmt.Sprintf("server error status %d", res.Status))
				}
				s.webLogger.Error("HTTP Request", attrs...)
			case res.Status >= 400: // 4xx status -> Warn level
				s.webLogger.Warn("HTTP Request", attrs...)
			default: // Others -> Info level
				s.webLogger.Info("HTTP Request", attrs...)
			}

			return err
		}
	}
}

// GetProcessor returns the processor instance
func (s *Server) GetProcessor() serviceapi.BirdNETProvider {
	return s.Processor
}

// validateAutoTLSEnvironment checks if the environment is suitable for AutoTLS
func (s *Server) validateAutoTLSEnvironment() error {
	// Check if host is set
	if s.Settings.Security.Host == "" {
		return errors.Newf("security.host must be set when AutoTLS is enabled").
			Component("http-controller").
			Category(errors.CategoryConfiguration).
			Context("operation", "validate_autotls_host").
			Build()
	}

	// Check if we can bind to privileged ports
	if !canBindPrivilegedPorts() {
		return errors.Newf("AutoTLS requires ability to bind to ports 80 and 443. Run as root or with CAP_NET_BIND_SERVICE capability").
			Component("http-controller").
			Category(errors.CategoryConfiguration).
			Context("operation", "validate_autotls_ports").
			Build()
	}

	// Warning about Docker port requirements
	if conf.RunningInContainer() {
		log.Println("WARNING: AutoTLS requires ports 80 and 443 to be exposed in your Docker configuration.")
		log.Println("Ensure your docker-compose.yml maps these ports:")
		log.Println("  ports:")
		log.Println("    - \"80:80\"")
		log.Println("    - \"443:443\"")
	}

	return nil
}

// canBindPrivilegedPorts checks if the process can bind to ports < 1024
func canBindPrivilegedPorts() bool {
	// Check if running as root
	if os.Geteuid() == 0 {
		return true
	}

	// Try to create a test listener on port 80
	listener, err := net.Listen("tcp", ":80") // #nosec G102 -- intentional privileged port test, listener immediately closed
	if err == nil {
		_ = listener.Close() // Best effort close for test listener
		return true
	}

	// Try port 443 as well
	listener, err = net.Listen("tcp", ":443") // #nosec G102 -- intentional privileged port test, listener immediately closed
	if err == nil {
		_ = listener.Close() // Best effort close for test listener
		return true
	}

	return false
}
