// execute.go
package processor

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

type ExecuteCommandAction struct {
	Command string
	Params  map[string]any
}

// GetDescription returns a description of the action
func (a ExecuteCommandAction) GetDescription() string {
	return fmt.Sprintf("Execute command: %s", a.Command)
}

// Execute implements the Action interface
func (a ExecuteCommandAction) Execute(ctx context.Context, data any) error {
	return a.ExecuteContext(ctx, data)
}

// ExecuteContext implements the ContextAction interface for proper context propagation
func (a ExecuteCommandAction) ExecuteContext(ctx context.Context, data any) error {
	log := GetLogger()
	log.Info("Executing command", logger.String("command", a.Command), logger.Any("params", a.Params))

	// Type assertion to check if data is of type Detections
	// The actual detection data is not used here since buildSafeArguments uses
	// pre-resolved values from a.Params (populated by parseCommandParams)
	if _, ok := data.(Detections); !ok {
		return errors.Newf("ExecuteCommandAction requires Detections type, got %T", data).
			Component("analysis.processor").
			Category(errors.CategoryValidation).
			Context("operation", "execute_command").
			Context("expected_type", "Detections").
			Build()
	}

	// Validate and resolve the command path
	cmdPath, err := validateCommandPath(a.Command)
	if err != nil {
		return errors.New(err).
			Component("analysis.processor").
			Category(errors.CategoryValidation).
			Priority(errors.PriorityHigh). // User-configured script issues should notify users
			Context("operation", "validate_command_path").
			Context("command_type", "external_script").
			Build()
	}

	// Building the command line arguments with validation
	// The params already contain resolved values from parseCommandParams (including normalized Confidence)
	args, err := buildSafeArguments(a.Params)
	if err != nil {
		// Extract parameter keys for better error context
		paramKeys := slices.Collect(maps.Keys(a.Params))
		return errors.New(err).
			Component("analysis.processor").
			Category(errors.CategoryValidation).
			Priority(errors.PriorityHigh). // User-configured script issues should notify users
			Context("operation", "build_command_arguments").
			Context("param_count", len(a.Params)).
			Context("param_keys", strings.Join(paramKeys, ", ")).
			Build()
	}

	log.Debug("Executing command with arguments", logger.String("command_path", cmdPath), logger.Any("args", args))

	// Create command with timeout, inheriting from parent context
	// This ensures cancellation propagates from CompositeAction
	cmdCtx, cancel := context.WithTimeout(ctx, ExecuteCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, cmdPath, args...) //nolint:gosec // G204: cmdPath validated by validateCommandPath(), args by buildSafeArguments()

	// Set a clean environment
	cmd.Env = getCleanEnvironment()

	// Execute the command with timing
	// Timing information helps identify performance issues and hanging scripts
	startTime := time.Now()
	output, err := cmd.CombinedOutput()
	executionDuration := time.Since(startTime)

	if err != nil {
		// Get exit code if available
		exitCode := -1
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}

		// Command execution failures are not retryable because:
		// - Script logic errors won't be fixed by retrying
		// - Non-zero exit codes indicate the script ran but failed
		// - Retrying could cause duplicate side effects (notifications, file writes)
		// Context includes execution metrics for performance analysis
		return errors.New(err).
			Component("analysis.processor").
			Category(errors.CategoryCommandExecution).
			Context("operation", "execute_command").
			Context("execution_duration_ms", executionDuration.Milliseconds()).
			Context("exit_code", exitCode).
			Context("output_size_bytes", len(output)).
			Context("retryable", false). // Command execution failures are typically not retryable
			Build()
	}

	// Log command success with size and truncated preview to avoid excessive log size
	outputStr := string(output)
	preview := outputStr
	if len(outputStr) > 200 {
		preview = outputStr[:200] + "... (truncated)"
	}
	log.Info("Command executed successfully",
		logger.Int("output_size_bytes", len(output)),
		logger.Int64("execution_duration_ms", executionDuration.Milliseconds()),
		logger.String("output_preview", preview))
	return nil
}

// validateCommandPath ensures the command exists and is executable
func validateCommandPath(command string) (string, error) {
	// Clean the path to remove any potential directory traversal
	command = filepath.Clean(command)

	// Check if it's an absolute path
	if !filepath.IsAbs(command) {
		return "", errors.Newf("command must use absolute path").
			Component("analysis.processor").
			Category(errors.CategoryValidation).
			Priority(errors.PriorityHigh). // User-configured script issues should notify users
			Context("operation", "validate_command_path").
			Context("security_check", "absolute_path_required").
			Context("path_classification", "relative_path").
			Context("validation_rule", "absolute_paths_only").
			Context("retryable", false). // Path validation failure is permanent
			Build()
	}

	// Verify the file exists and is executable
	info, err := os.Stat(command)
	if err != nil {
		// Classify OS errors for better telemetry and debugging
		// Using switch statement instead of if-else chain per gocritic best practices
		// This pattern provides clearer intent and better performance for multiple conditions
		var classification string
		switch {
		case os.IsNotExist(err):
			classification = "file_not_found"
		case os.IsPermission(err):
			classification = "permission_denied"
		default:
			classification = "file_access_error"
		}

		// File system errors are not retryable as they indicate permanent issues:
		// - Missing files won't suddenly appear
		// - Permission denials require manual intervention
		// - Other file access errors typically indicate corruption or system issues
		return "", errors.New(err).
			Component("analysis.processor").
			Category(errors.CategoryFileIO).
			Context("operation", "validate_command_path").
			Context("security_check", "file_existence").
			Context("error_classification", classification).
			Context("retryable", false). // File existence issues are permanent
			Build()
	}

	// Check file permissions
	if runtime.GOOS != "windows" {
		if info.Mode()&0o111 == 0 {
			return "", errors.Newf("command is not executable").
				Component("analysis.processor").
				Category(errors.CategoryValidation).
				Priority(errors.PriorityHigh). // User-configured script issues should notify users
				Context("operation", "validate_command_path").
				Context("security_check", "executable_permission").
				Context("file_mode", info.Mode().String()).
				Context("os_platform", runtime.GOOS).
				Context("retryable", false). // Permission issues are permanent
				Build()
		}
	}

	return command, nil
}

// buildSafeArguments creates a sanitized list of command arguments from the params map.
// The params map should contain already-resolved values (e.g., from parseCommandParams).
// This function validates parameter names, sanitizes values, and handles quoting.
func buildSafeArguments(params map[string]any) ([]string, error) {
	// Pre-allocate slice with capacity for all parameters
	args := make([]string, 0, len(params))

	// Get sorted keys for deterministic CLI argument order
	keys := slices.Collect(maps.Keys(params))
	slices.Sort(keys)

	for _, key := range keys {
		value := params[key]

		// Validate parameter name (allow only alphanumeric and _-)
		if !isValidParamName(key) {
			return nil, errors.Newf("invalid parameter name").
				Component("analysis.processor").
				Category(errors.CategoryValidation).
				Priority(errors.PriorityHigh). // User-configured script issues should notify users
				Context("operation", "build_command_arguments").
				Context("security_check", "parameter_name_validation").
				Context("validation_rule", "alphanumeric_underscore_dash_only").
				Context("param_name", key).
				Context("retryable", false). // Parameter validation failure is permanent
				Build()
		}

		// Convert and validate the value (already resolved from params)
		strValue, err := sanitizeValue(value)
		if err != nil {
			return nil, errors.New(err).
				Component("analysis.processor").
				Category(errors.CategoryValidation).
				Context("operation", "build_command_arguments").
				Context("security_check", "value_sanitization").
				Context("value_type", fmt.Sprintf("%T", value)).
				Context("param_name", key).
				Context("retryable", false). // Value sanitization failure is permanent
				Build()
		}

		// Handle quoting for values that need it
		if strings.ContainsAny(strValue, " @\"'") {
			// Check if already quoted to avoid double quoting
			if !strings.HasPrefix(strValue, "\"") || !strings.HasSuffix(strValue, "\"") {
				// Use %q for proper quoting (handles escaping automatically)
				strValue = fmt.Sprintf("%q", strValue)
			}
		}

		arg := fmt.Sprintf("--%s=%s", key, strValue)
		args = append(args, arg)
	}

	return args, nil
}

// isValidParamName checks if a parameter name contains only safe characters
func isValidParamName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') &&
			(r < '0' || r > '9') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

// sanitizeValue converts and validates a value to string
func sanitizeValue(value any) (string, error) {
	// Convert to string and validate
	str := fmt.Sprintf("%v", value)

	// Basic sanitization - remove any control characters
	str = strings.Map(func(r rune) rune {
		if r < 32 {
			return -1
		}
		return r
	}, str)

	// Additional validation can be added here

	return str, nil
}

// getCleanEnvironment returns a minimal set of necessary environment variables
func getCleanEnvironment() []string {
	// Provide only necessary environment variables
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"TEMP=" + os.Getenv("TEMP"),
		"TMP=" + os.Getenv("TMP"),
	}

	// Add system root for Windows
	if runtime.GOOS == "windows" {
		env = append(env, "SystemRoot="+os.Getenv("SystemRoot"))
	}

	return env
}

// getNoteValueByName retrieves a value from a Note by field name using reflection
func getNoteValueByName(note *datastore.Note, paramName string) any {
	val := reflect.ValueOf(*note)
	fieldVal := val.FieldByName(paramName)

	// Check if the field is valid (exists in the struct) and can be interfaced
	if fieldVal.IsValid() && fieldVal.CanInterface() {
		return fieldVal.Interface()
	}

	// Return nil or an appropriate zero value if the field does not exist
	return nil
}

// getResultValueByName retrieves a value from a Result by parameter name using explicit mapping.
// This maps external script parameter names to the appropriate Result fields.
func getResultValueByName(result *detection.Result, paramName string) any {
	switch paramName {
	// Species-related fields (nested in Species struct)
	case "CommonName":
		return result.Species.CommonName
	case "ScientificName":
		return result.Species.ScientificName
	case "SpeciesCode":
		return result.Species.Code

	// Direct Result fields
	case "ID":
		return result.ID
	case "Confidence":
		return result.Confidence
	case "Latitude":
		return result.Latitude
	case "Longitude":
		return result.Longitude
	case "ClipName":
		return result.ClipName
	case "Threshold":
		return result.Threshold
	case "Sensitivity":
		return result.Sensitivity
	case "SourceNode":
		return result.SourceNode
	case "ProcessingTime":
		return result.ProcessingTime
	case "Occurrence":
		return result.Occurrence

	// Time-related fields
	case "Date":
		return result.Date()
	case "Time":
		return result.Time()
	case "BeginTime":
		return result.BeginTime
	case "EndTime":
		return result.EndTime

	// AudioSource-related fields
	case "Source":
		return result.AudioSource.ID

	default:
		return nil
	}
}
