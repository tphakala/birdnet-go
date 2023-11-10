package config

import (
	"sync"
	"time"
)

//var globalContext *Context

// OccurrenceMonitor to track species occurrences and manage state reset.
type OccurrenceMonitor struct {
	LastSpecies   string
	OccurrenceMap map[string]int
	ResetDuration time.Duration
	Mutex         sync.Mutex
	Timer         *time.Timer
}

// Context holds the overall application state, including the Settings and the OccurrenceMonitor.
type Context struct {
	Settings            *Settings
	OccurrenceMonitor   *OccurrenceMonitor
	ExcludedSpeciesList []string // Field to hold the list of excluded species
}

// NewOccurrenceMonitor creates a new instance of OccurrenceMonitor with the given reset duration.
func NewOccurrenceMonitor(resetDuration time.Duration) *OccurrenceMonitor {
	return &OccurrenceMonitor{
		OccurrenceMap: make(map[string]int),
		ResetDuration: resetDuration,
	}
}

// NewContext creates a new instance of Context with the provided settings and occurrence monitor.
func NewContext(settings *Settings, occurrenceMonitor *OccurrenceMonitor) *Context {
	return &Context{
		Settings:          settings,
		OccurrenceMonitor: occurrenceMonitor,
	}
}

// TrackSpecies checks and updates the species occurrences in the OccurrenceMonitor.
func (om *OccurrenceMonitor) TrackSpecies(species string) bool {
	om.Mutex.Lock()
	defer om.Mutex.Unlock()

	if om.Timer == nil || om.LastSpecies != species {
		om.resetState(species)
		return false
	}

	om.OccurrenceMap[species]++

	return om.OccurrenceMap[species] > 1
}

// resetState resets the state of the OccurrenceMonitor.
func (om *OccurrenceMonitor) resetState(species string) {
	om.OccurrenceMap = map[string]int{species: 1}
	om.LastSpecies = species
	if om.Timer != nil {
		om.Timer.Stop()
	}
	om.Timer = time.AfterFunc(om.ResetDuration, func() {
		om.Mutex.Lock()
		defer om.Mutex.Unlock()
		om.OccurrenceMap = make(map[string]int)
		om.Timer = nil
		om.LastSpecies = ""
	})
}
