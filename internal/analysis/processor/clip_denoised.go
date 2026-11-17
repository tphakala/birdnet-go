package processor

// SetClipDenoiseRecorder sets where the denoise marker of a saved clip is written.
// Call it before Start and never reset it; r must not be a typed nil. Without one
// (the legacy store) the marker is not written.
func (p *Processor) SetClipDenoiseRecorder(r clipDenoiseRecorder) {
	p.clipMarks = r
}

// ClipDenoiseRecorder returns the recorder set by SetClipDenoiseRecorder, or nil.
func (p *Processor) ClipDenoiseRecorder() clipDenoiseRecorder {
	return p.clipMarks
}
