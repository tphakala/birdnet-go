package audiocore

// SetAnalyzerManager sets the analyzer manager for the audio manager
func (m *managerImpl) SetAnalyzerManager(analyzerManager AnalyzerManager) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.analyzerManager = analyzerManager
}

// SetHealthMonitor sets the health monitor for the audio manager
func (m *managerImpl) SetHealthMonitor(healthMonitor *AudioHealthMonitor) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.healthMonitor = healthMonitor
}

// GetAnalyzerManager returns the analyzer manager
func (m *managerImpl) GetAnalyzerManager() AnalyzerManager {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.analyzerManager
}

// GetHealthMonitor returns the health monitor
func (m *managerImpl) GetHealthMonitor() *AudioHealthMonitor {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.healthMonitor
}