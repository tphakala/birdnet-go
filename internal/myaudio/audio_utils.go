package myaudio

// getFileExtension returns the appropriate file extension based on the format
func GetFileExtension(format string) string {
	switch format {
	case "aac":
		return "m4a"
	default:
		return format
	}
}
