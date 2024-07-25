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
)

// SaveSettings handles the request to save settings
func (h *Handlers) SaveSettings(c echo.Context) error {
	log.Println("handler.SaveSettings: Starting save process")

	settings := conf.GetSettings()
	if settings == nil {
		log.Println("SaveSettings: Error - settings is nil")
		return fmt.Errorf("settings is nil")
	}

	formParams, err := c.FormParams()
	if err != nil {
		log.Printf("handler.SaveSettings: Failed to parse form: %v", err)
		return fmt.Errorf("failed to parse form: %w", err)
	}

	if err := updateSettingsFromForm(settings, formParams); err != nil {
		log.Printf("handler.SaveSettings: Error updating settings: %v", err)
		return fmt.Errorf("error updating settings: %w", err)
	}

	if err := conf.SaveSettings(); err != nil {
		log.Printf("handler.SaveSettings: Error saving settings: %v", err)
		h.SSE.SendNotification(Notification{
			Message: fmt.Sprintf("Error saving settings: %v", err),
			Type:    "error",
		})
		return c.NoContent(http.StatusInternalServerError)
	}

	h.SSE.SendNotification(Notification{
		Message: "Settings saved successfully",
		Type:    "success",
	})

	log.Println("handler.SaveSettings: Settings saved successfully, now reloading")
	if err := h.reloadSettings(); err != nil {
		return fmt.Errorf("error reloading settings: %w", err)
	}

	h.SSE.SendNotification(Notification{
		Message: "Settings reloaded successfully",
		Type:    "success",
	})

	return c.NoContent(http.StatusOK)
}

// reloadSettings reloads the settings from the configuration file
func (h *Handlers) reloadSettings() error {
	log.Println("reloadSettings: Starting reload process")

	newSettings, err := conf.Load()
	if err != nil {
		log.Printf("reloadSettings: Error reloading settings: %v", err)
		return err
	}

	// Update the handlers settings
	h.Settings = newSettings

	log.Println("reloadSettings: Settings reloaded successfully")
	return nil
}

func updateSettingsFromForm(settings *conf.Settings, formValues map[string][]string) error {
	log.Printf("updateSettingsFromForm: Starting update process")
	return updateStructFromForm(reflect.ValueOf(settings).Elem(), formValues, "")
}

func updateStructFromForm(v reflect.Value, formValues map[string][]string, prefix string) error {
	//log.Printf("updateStructFromForm: Starting update process for prefix: %s", prefix)
	//log.Printf("updateStructFromForm: Form values received: %+v", formValues)
	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)
		fieldName := fieldType.Name

		if !field.CanSet() {
			log.Printf("updateStructFromForm: Field %s cannot be set, skipping", fieldName)
			continue
		}

		fullName := strings.ToLower(prefix + fieldName)
		formValue, exists := formValues[fullName]

		if !exists {
			if field.Kind() == reflect.Struct {
				if err := updateStructFromForm(field, formValues, fullName+"."); err != nil {
					return err
				}
			}
			continue
		}

		//log.Printf("updateStructFromForm: Updating field: %s, Current value: %v, New value: %v", fullName, field.Interface(), formValue)

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
			if err := updateSliceFromForm(field, formValue); err != nil {
				return fmt.Errorf("error updating slice for %s: %w", fullName, err)
			}
		case reflect.Struct:
			if err := updateStructFromForm(field, formValues, fullName+"."); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported field type for %s", fullName)
		}

		//log.Printf("updateStructFromForm: Updated field: %s, New value: %v", fullName, field.Interface())
	}

	return nil
}

func updateSliceFromForm(field reflect.Value, formValue []string) error {
	log.Printf("updateSliceFromForm: Updating slice with values: %v", formValue)
	sliceType := field.Type().Elem()
	newSlice := reflect.MakeSlice(field.Type(), 0, len(formValue))

	for _, val := range formValue {
		if val == "" {
			continue // Skip empty values
		}
		switch sliceType.Kind() {
		case reflect.String:
			// Check if the value is a JSON-encoded array
			var urls []string
			err := json.Unmarshal([]byte(val), &urls)
			if err == nil {
				// If it's a valid JSON array, add each URL separately
				for _, url := range urls {
					if url != "" {
						newSlice = reflect.Append(newSlice, reflect.ValueOf(url))
					}
				}
			} else {
				// If it's not a JSON array, add it as a single string
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

	field.Set(newSlice)
	log.Printf("updateSliceFromForm: Updated slice to: %v", newSlice.Interface())
	return nil
}
