// update_yaml.go
package conf

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// yamlContext holds the state during YAML processing
type yamlContext struct {
	currentPath  []string
	indentLevels []int
	settingsMap  map[string]interface{}
	reader       *bufio.Scanner
}

// UpdateYAMLConfig updates the YAML configuration file with new settings.
// It writes to a temporary file and then replaces the original file to ensure atomic updates.
func UpdateYAMLConfig(configPath string, newSettings *Settings) error {
	settingsMap := createSettingsMap(newSettings)

	tempFile, err := createTempFile(configPath)
	if err != nil {
		return fmt.Errorf("error creating temporary file: %w", err)
	}

	if err := processYAMLFile(configPath, tempFile, settingsMap); err != nil {
		return fmt.Errorf("error processing YAML file: %w", err)
	}

	return finalizeUpdate(tempFile, configPath)
}

// createSettingsMap creates a flat map from the settings struct, including all values
func createSettingsMap(settings *Settings) map[string]interface{} {
	settingsValue := reflect.ValueOf(settings).Elem()
	settingsMap := make(map[string]interface{})
	createFlatMap(settingsValue, "", settingsMap)
	return settingsMap
}

// createFlatMap recursively creates a flat map from a reflect.Value
func createFlatMap(v reflect.Value, prefix string, result map[string]interface{}) {
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)
		key := fieldType.Name
		if prefix != "" {
			key = prefix + "." + key
		}
		key = strings.ToLower(key)

		switch field.Kind() {
		case reflect.Struct:
			createFlatMap(field, key, result)
		case reflect.Slice:
			if field.IsNil() {
				result[key] = reflect.MakeSlice(field.Type(), 0, 0).Interface()
			} else {
				result[key] = field.Interface()
			}
		default:
			result[key] = field.Interface()
		}
	}
}

// createTempFile creates a temporary file in the same directory as the config file
func createTempFile(configPath string) (*os.File, error) {
	tempDir := filepath.Dir(configPath)
	return os.CreateTemp(tempDir, "config.yaml.tmp")
}

// finalizeUpdate closes the temporary file and renames it to replace the original config file
func finalizeUpdate(tempFile *os.File, configPath string) error {
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("error closing temporary file: %w", err)
	}

	if err := os.Rename(tempFile.Name(), configPath); err != nil {
		return fmt.Errorf("error replacing config file: %w", err)
	}

	return nil
}

// processYAMLFile reads the original YAML configuration file, updates the settings, and writes to a temporary file.
func processYAMLFile(configPath string, tempFile *os.File, settingsMap map[string]interface{}) error {
	file, err := os.Open(configPath)
	if err != nil {
		return fmt.Errorf("error opening config file: %w", err)
	}
	defer file.Close()

	reader := bufio.NewScanner(file)
	writer := bufio.NewWriter(tempFile)
	ctx := &yamlContext{
		indentLevels: []int{0},
		settingsMap:  settingsMap,
		reader:       reader,
	}

	for reader.Scan() {
		line := reader.Text()
		if err := processLine(line, writer, ctx); err != nil {
			return err
		}
	}

	if err := reader.Err(); err != nil {
		return fmt.Errorf("error reading config file: %w", err)
	}

	return writer.Flush()
}

// processLine processes a single line of the YAML file
func processLine(line string, writer *bufio.Writer, ctx *yamlContext) error {
	trimmedLine := strings.TrimSpace(line)

	if isCommentOrEmpty(trimmedLine) {
		return writeLine(writer, line)
	}

	indentation := getIndentation(line)
	updateContext(ctx, indentation, trimmedLine)

	if strings.HasSuffix(trimmedLine, ":") {
		return processListIndicator(line, writer, ctx)
	}

	return processKeyValuePair(line, trimmedLine, writer, ctx)
}

// processListIndicator handles list indicators in the YAML file
func processListIndicator(line string, writer *bufio.Writer, ctx *yamlContext) error {
	listKey := ctx.currentPath[len(ctx.currentPath)-1]
	fullKey := strings.ToLower(buildFullKey(ctx.currentPath[:len(ctx.currentPath)-1], listKey))

	if newValue, exists := ctx.settingsMap[fullKey]; exists {
		// Write the list indicator line
		if err := writeLine(writer, line); err != nil {
			return err
		}

		// Write new list items
		if err := writeNewListItems(writer, line, newValue); err != nil {
			return err
		}

		// Skip existing list items
		return skipExistingListItems(ctx, writer)
	}

	// If no new value exists, write the original line and process the next line
	if err := writeLine(writer, line); err != nil {
		return err
	}

	return processNextLine(ctx, writer)
}

// writeNewListItems writes new list items to the YAML file
func writeNewListItems(writer *bufio.Writer, line string, newValue interface{}) error {
	newList := convertToInterfaceSlice(newValue)

	// If the list is empty, don't write any entries
	if len(newList) == 0 {
		return nil
	}

	// Write new list items with the same indentation as the original list indicator
	indentation := getIndentation(line) + 2
	for _, item := range newList {
		var formattedItem string
		if str, ok := item.(string); ok {
			// Quote string items in lists
			formattedItem = fmt.Sprintf("\"%s\"", strings.ReplaceAll(str, "\"", "\\\""))
		} else {
			formattedItem = formatValue("", item)
		}
		newLine := fmt.Sprintf("%s- %s", strings.Repeat(" ", indentation), formattedItem)
		if err := writeLine(writer, newLine); err != nil {
			return err
		}
	}

	return nil
}

// skipExistingListItems skips over the existing list items in the original file
func skipExistingListItems(ctx *yamlContext, writer *bufio.Writer) error {
	currentIndentation := ctx.indentLevels[len(ctx.indentLevels)-1]

	for ctx.reader.Scan() {
		nextLine := ctx.reader.Text()
		nextIndentation := getIndentation(nextLine)

		// If we've reached a line with less or equal indentation, or it's not a list item,
		// we've finished skipping the list. Process this line and return.
		if nextIndentation <= currentIndentation || !strings.HasPrefix(strings.TrimSpace(nextLine), "-") {
			return processLine(nextLine, writer, ctx)
		}

		// Otherwise, continue skipping list items
	}

	return nil
}

// convertToInterfaceSlice converts a value to []interface{}
func convertToInterfaceSlice(value interface{}) []interface{} {
	switch v := value.(type) {
	case []interface{}:
		return v
	case []string:
		newList := make([]interface{}, len(v))
		for i, s := range v {
			newList[i] = s
		}
		return newList
	default:
		return nil
	}
}

// processKeyValuePair handles key-value pairs in the YAML file
func processKeyValuePair(line, trimmedLine string, writer *bufio.Writer, ctx *yamlContext) error {
	key, value, found := parseKeyValue(trimmedLine)
	if !found {
		return writeLine(writer, line)
	}

	fullKey := strings.ToLower(buildFullKey(ctx.currentPath, key))

	if newValue, exists := ctx.settingsMap[fullKey]; exists {
		return writeUpdatedLine(writer, line, key, value, newValue)
	}

	return writeLine(writer, line)
}

// getIndentation returns the number of leading spaces in a line
func getIndentation(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

// updateContext updates the YAML context based on the current line's indentation and content
func updateContext(ctx *yamlContext, indentation int, line string) {
	// Remove outdated context
	for len(ctx.indentLevels) > 0 && indentation <= ctx.indentLevels[len(ctx.indentLevels)-1] {
		ctx.indentLevels = ctx.indentLevels[:len(ctx.indentLevels)-1]
		if len(ctx.currentPath) > 0 {
			ctx.currentPath = ctx.currentPath[:len(ctx.currentPath)-1]
		}
	}

	// Add new context for nested structures
	if strings.HasSuffix(line, ":") {
		ctx.indentLevels = append(ctx.indentLevels, indentation)
		newPath := strings.TrimSuffix(strings.TrimSpace(line), ":")
		ctx.currentPath = append(ctx.currentPath, newPath)
	}
}

// isCommentOrEmpty returns true if the line is a comment or empty
func isCommentOrEmpty(line string) bool {
	return strings.HasPrefix(line, "#") || line == ""
}

// parseKeyValue splits a YAML line into key and value, handling special cases
func parseKeyValue(line string) (key, value string, found bool) {
	line = strings.TrimSpace(line)

	if strings.HasPrefix(line, "- ") {
		return "", "", false
	}

	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}

	key = strings.TrimSpace(parts[0])
	value = strings.TrimSpace(parts[1])

	if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
		return key, value[1 : len(value)-1], true
	}

	if commentIndex := strings.Index(value, "#"); commentIndex != -1 {
		value = strings.TrimSpace(value[:commentIndex])
	}

	return key, value, true
}

// buildFullKey constructs the full key path
func buildFullKey(path []string, key string) string {
	return strings.ToLower(strings.Join(append(path, key), "."))
}

// writeUpdatedLine writes an updated line to the writer
func writeUpdatedLine(writer io.Writer, line, key string, oldValue, newValue interface{}) error {
	indentation := strings.Repeat(" ", getIndentation(line))
	parts := strings.SplitN(line, "#", 2)
	updatedLine := fmt.Sprintf("%s%s:", indentation, key)

	if reflect.TypeOf(newValue).Kind() == reflect.Slice {
		if err := writeLine(writer, updatedLine); err != nil {
			return err
		}
		return writeSliceValues(writer, indentation, newValue)
	}

	formattedValue := formatValue(key, newValue)
	updatedLine += fmt.Sprintf(" %s", formattedValue)
	if len(parts) > 1 {
		updatedLine += " #" + parts[1]
	}
	return writeLine(writer, updatedLine)
}

// writeSliceValues writes slice values to the YAML file
func writeSliceValues(writer io.Writer, indentation string, slice interface{}) error {
	reflectSlice := reflect.ValueOf(slice)
	for i := 0; i < reflectSlice.Len(); i++ {
		element := reflectSlice.Index(i).Interface()
		formattedElement := formatValue("", element)
		elementLine := fmt.Sprintf("%s  - %s", indentation, formattedElement)
		if err := writeLine(writer, elementLine); err != nil {
			return err
		}
	}
	return nil
}

// formatValue formats the value based on its type and key
func formatValue(key string, value interface{}) string {
	switch v := value.(type) {
	case string:
		return fmt.Sprintf("\"%s\"", strings.ReplaceAll(v, "\"", "\\\""))
	case bool:
		return fmt.Sprintf("%v", v)
	case float64:
		switch strings.ToLower(key) {
		case "latitude", "longitude":
			return fmt.Sprintf("%.6f", v)
		case "threshold", "sensitivity":
			return fmt.Sprintf("%.2f", v)
		default:
			return fmt.Sprintf("%g", v)
		}
	case int, int64:
		return fmt.Sprintf("%d", v)
	case []interface{}:
		return formatSlice(v)
	case []string:
		return formatStringSlice(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// formatSlice formats a slice of interfaces for YAML output
func formatSlice(slice []interface{}) string {
	var parts []string
	for _, item := range slice {
		parts = append(parts, formatValue("", item))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// formatStringSlice formats a slice of strings for YAML output
func formatStringSlice(slice []string) string {
	var parts []string
	for _, item := range slice {
		parts = append(parts, formatValue("", item))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// writeLine writes a single line to the writer
func writeLine(writer io.Writer, line string) error {
	_, err := fmt.Fprintln(writer, line)
	return err
}

// processNextLine processes the next line in the YAML file
func processNextLine(ctx *yamlContext, writer *bufio.Writer) error {
	if ctx.reader.Scan() {
		return processLine(ctx.reader.Text(), writer, ctx)
	}
	return nil
}
