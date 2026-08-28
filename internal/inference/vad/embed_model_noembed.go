//go:build noembed

package vad

// embeddedModel is empty in noembed builds; the VAD then requires an explicit
// model path (Config.ModelPath) supplied by the caller.
var embeddedModel []byte
