package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

var fieldsToSkip = map[string]bool{
	"birdnet.rangefilter.species":     true,
	"birdnet.rangefilter.lastupdated": true,
}

// GetAudioDevices handles the request to list available audio devices
func (h *Handlers) GetAudioDevices(c echo.Context) error {
	devices, err := myaudio.ListAudioSources()

	fmt.Println("Devices:", devices)

	if err != nil {
		log.Println("Error listing audio devices:", err)
		return h.NewHandlerError(err, "Failed to list audio devices", http.StatusInternalServerError)
	}

	return c.JSON(http.StatusOK, devices)
}

// SaveSettings handles the request to save settings
func (h *Handlers) SaveSettings(c echo.Context) error {
	settings := conf.GetSettings()
	if settings == nil {
		// Return an error if settings are not initialized
		return h.NewHandlerError(fmt.Errorf("settings is nil"), "Settings not initialized", http.StatusInternalServerError)
	}

	formParams, err := c.FormParams()
	if err != nil {
		// Return an error if form parameters cannot be parsed
		return h.NewHandlerError(err, "Failed to parse form", http.StatusBadRequest)
	}

	if err := updateSettingsFromForm(settings, formParams); err != nil {
		// Return an error if updating settings from form parameters fails
		return h.NewHandlerError(err, "Error updating settings", http.StatusInternalServerError)
	}

	// Send success notification for reloading settings
	h.SSE.SendNotification(Notification{
		Message: "Settings applied successfully",
		Type:    "success",
	})

	if err := conf.SaveSettings(); err != nil {
		// Send error notification if saving settings fails
		h.SSE.SendNotification(Notification{
			Message: fmt.Sprintf("Error saving settings: %v", err),
			Type:    "error",
		})
		return h.NewHandlerError(err, "Failed to save settings", http.StatusInternalServerError)
	}

	// Send success notification for saving settings
	h.SSE.SendNotification(Notification{
		Message: "Settings saved successfully",
		Type:    "success",
	})

	return c.NoContent(http.StatusOK)
}

// updateSettingsFromForm updates the settings based on form values
func updateSettingsFromForm(settings *conf.Settings, formValues map[string][]string) error {
	// Delegate the update process to updateStructFromForm
	return updateStructFromForm(reflect.ValueOf(settings).Elem(), formValues, "")
}

// updateStructFromForm recursively updates a struct's fields from form values
func updateStructFromForm(v reflect.Value, formValues map[string][]string, prefix string) error {
	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)
		fieldName := fieldType.Name

		if !field.CanSet() {
			// Skip fields that cannot be set
			continue
		}

		fullName := strings.ToLower(prefix + fieldName)

		// Skip fields that should not be updated from the form
		if fieldsToSkip[fullName] {
			continue
		}

		formValue, exists := formValues[fullName]

		// DEBUG: Log the field name and form value
		log.Printf("%s: %v", fullName, formValue)

		if !exists {
			if field.Kind() == reflect.Struct {
				// Recursively update nested structs
				if err := updateStructFromForm(field, formValues, fullName+"."); err != nil {
					return err
				}
			}
			continue
		}

		// Update the field based on its type
		switch field.Kind() {
		case reflect.String:
			if len(formValue) > 0 {
				field.SetString(formValue[0])
			}
		case reflect.Bool:
			boolValue := false
			if len(formValue) > 0 {
				boolValue = formValue[0] == "on" || formValue[0] == "true"
			}
			field.SetBool(boolValue)
		case reflect.Int, reflect.Int64:
			if len(formValue) > 0 {
				intValue, err := strconv.ParseInt(formValue[0], 10, 64)
				if err != nil {
					return fmt.Errorf("invalid integer value for %s: %w", fullName, err)
				}
				field.SetInt(intValue)
			}
		case reflect.Float32, reflect.Float64:
			if len(formValue) > 0 {
				floatValue, err := strconv.ParseFloat(formValue[0], 64)
				if err != nil {
					return fmt.Errorf("invalid float value for %s: %w", fullName, err)
				}
				if field.Kind() == reflect.Float32 {
					field.SetFloat(float64(float32(floatValue)))
				} else {
					field.SetFloat(floatValue)
				}
			}
		case reflect.Slice:
			if fieldType.Type.Elem().Kind() == reflect.String {
				// Handle string slice (e.g., species lists)
				if len(formValue) > 0 {
					var stringSlice []string
					err := json.Unmarshal([]byte(formValue[0]), &stringSlice)
					if err != nil {
						return fmt.Errorf("error unmarshaling JSON for %s: %w", fullName, err)
					}
					field.Set(reflect.ValueOf(stringSlice))
				}
			} else {
				// Handle other slice types as before
				if err := updateSliceFromForm(field, formValue); err != nil {
					return fmt.Errorf("error updating slice for %s: %w", fullName, err)
				}
			}
		case reflect.Struct:
			if err := updateStructFromForm(field, formValues, fullName+"."); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported field type for %s", fullName)
		}
	}

	return nil
}

// updateSliceFromForm updates a slice field from form values
func updateSliceFromForm(field reflect.Value, formValue []string) error {
	sliceType := field.Type().Elem()
	newSlice := reflect.MakeSlice(field.Type(), 0, len(formValue))

	for _, val := range formValue {
		if val == "" {
			continue // Skip empty values
		}
		switch sliceType.Kind() {
		case reflect.String:
			var urls []string
			err := json.Unmarshal([]byte(val), &urls)
			if err == nil {
				// Add each URL separately if it's a valid JSON array
				for _, url := range urls {
					if url != "" {
						newSlice = reflect.Append(newSlice, reflect.ValueOf(url))
					}
				}
			} else {
				// Add as a single string if not a JSON array
				newSlice = reflect.Append(newSlice, reflect.ValueOf(val))
			}
		case reflect.Int, reflect.Int64:
			intVal, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid integer value: %w", err)
			}
			newSlice = reflect.Append(newSlice, reflect.ValueOf(intVal).Convert(sliceType))
		case reflect.Float32, reflect.Float64:
			floatVal, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return fmt.Errorf("invalid float value: %w", err)
			}
			newSlice = reflect.Append(newSlice, reflect.ValueOf(floatVal).Convert(sliceType))
		default:
			return fmt.Errorf("unsupported slice element type: %v", sliceType.Kind())
		}
	}

	// Set the updated slice back to the field
	field.Set(newSlice)
	return nil
}
