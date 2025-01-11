// internal/httpcontroller/utils.go - Utility functions for the HTTP controller package
package httpcontroller

import (
	"sort"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// prepareLocalesData returns sorted locale data to be used for select menu on the main settings page
func (s *Server) prepareLocalesData() []LocaleData {
	var locales []LocaleData
	for code, name := range conf.LocaleCodes {
		locales = append(locales, LocaleData{Code: code, Name: name})
	}
	sort.Slice(locales, func(i, j int) bool {
		return locales[i].Name < locales[j].Name
	})
	return locales
}

// removeDuplicates removes duplicate entries from a slice of strings
func removeDuplicates(slice []string) []string {
	keys := make(map[string]bool)
	list := []string{}
	for _, entry := range slice {
		if _, value := keys[entry]; !value {
			keys[entry] = true
			list = append(list, entry)
		}
	}
	return list
}
