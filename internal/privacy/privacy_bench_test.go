package privacy

import (
	"strings"
	"testing"
)

// Benchmark data
var (
	benchmarkMessages = []string{
		"Failed to connect to rtsp://admin:password@192.168.1.100:554/stream1",
		"Error fetching http://api.example.com/v1/data with authentication failed",
		"Multiple URLs: rtsp://user:pass@cam1.local/stream and https://secure.api.service.com:8443/upload/data",
		"Connection timeout for rtsp://camera1:8554/live/main and backup rtsp://camera2:8554/live/backup",
		"Simple message without any URLs for baseline performance testing",
	}
	
	benchmarkURLs = []string{
		"rtsp://admin:password@192.168.1.100:554/stream1/channel1/main",
		"http://api.example.com/v1/data/users/12345/profile",
		"https://secure.service.com:8443/api/v2/upload/files/documents",
		"rtsp://user:complexpassword123@camera.local:8554/live/stream",
		"http://localhost:8080/api/health/check",
	}
	
	benchmarkRTSPURLs = []string{
		"rtsp://admin:password@192.168.1.100:554/stream1/channel1",
		"rtsp://user:pass@camera.local:8554/live/main/stream",
		"rtsp://viewer:secret123@10.0.0.50:554/axis-media/media.amp",
		"rtsp://192.168.1.200:554/stream/live/1",
		"rtsp://admin:admin123@camera.example.com:8554/h264/ch1/main/av_stream",
	}
)

// BenchmarkScrubMessage tests performance of message scrubbing
func BenchmarkScrubMessage(b *testing.B) {
	b.ReportAllocs()
	
	for b.Loop() {
		for _, msg := range benchmarkMessages {
			_ = ScrubMessage(msg)
		}
	}
}

// BenchmarkScrubMessageLarge tests performance with larger messages
func BenchmarkScrubMessageLarge(b *testing.B) {
	// Create a large message with multiple URLs
	largeMessage := strings.Repeat("Some text before URL ", 10) +
		"rtsp://admin:password@192.168.1.100:554/stream1 " +
		strings.Repeat("some text between URLs ", 20) +
		"https://api.example.com/v1/data " +
		strings.Repeat("more text after URLs ", 15)
	
	b.ReportAllocs()
	
	for b.Loop() {
		_ = ScrubMessage(largeMessage)
	}
}

// BenchmarkAnonymizeURL tests performance of URL anonymization
func BenchmarkAnonymizeURL(b *testing.B) {
	b.ReportAllocs()
	
	for b.Loop() {
		for _, url := range benchmarkURLs {
			_ = AnonymizeURL(url)
		}
	}
}

// BenchmarkAnonymizeURLSingle tests performance for a single typical URL
func BenchmarkAnonymizeURLSingle(b *testing.B) {
	url := "rtsp://admin:password@192.168.1.100:554/stream1/channel1"
	
	b.ReportAllocs()
	
	for b.Loop() {
		_ = AnonymizeURL(url)
	}
}

// BenchmarkSanitizeRTSPUrl tests performance of RTSP URL sanitization
func BenchmarkSanitizeRTSPUrl(b *testing.B) {
	b.ReportAllocs()
	
	for b.Loop() {
		for _, url := range benchmarkRTSPURLs {
			_ = SanitizeRTSPUrl(url)
		}
	}
}

// BenchmarkSanitizeRTSPUrlSingle tests performance for a single RTSP URL
func BenchmarkSanitizeRTSPUrlSingle(b *testing.B) {
	url := "rtsp://admin:password@192.168.1.100:554/stream1/channel1"
	
	b.ReportAllocs()
	
	for b.Loop() {
		_ = SanitizeRTSPUrl(url)
	}
}

// BenchmarkGenerateSystemID tests performance of system ID generation
func BenchmarkGenerateSystemID(b *testing.B) {
	b.ReportAllocs()
	
	for b.Loop() {
		_, err := GenerateSystemID()
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkIsValidSystemID tests performance of system ID validation
func BenchmarkIsValidSystemID(b *testing.B) {
	validIDs := []string{
		"A1B2-C3D4-E5F6",
		"1234-5678-9ABC",
		"FFFF-0000-AAAA",
		"a1b2-c3d4-e5f6",
		"9999-8888-7777",
	}
	
	b.ReportAllocs()
	
	for b.Loop() {
		for _, id := range validIDs {
			_ = IsValidSystemID(id)
		}
	}
}

// BenchmarkCategorizeHost tests performance of host categorization
func BenchmarkCategorizeHost(b *testing.B) {
	hosts := []string{
		"192.168.1.100",
		"example.com",
		"localhost",
		"10.0.0.1",
		"api.service.org",
	}
	
	b.ReportAllocs()
	
	for b.Loop() {
		for _, host := range hosts {
			_ = categorizeHost(host)
		}
	}
}

// BenchmarkIsPrivateIP tests performance of private IP detection
func BenchmarkIsPrivateIP(b *testing.B) {
	ips := []string{
		"192.168.1.100",
		"10.0.0.1",
		"172.16.1.1",
		"8.8.8.8",
		"169.254.1.1",
	}
	
	b.ReportAllocs()
	
	for b.Loop() {
		for _, ip := range ips {
			_ = isPrivateIP(ip)
		}
	}
}

// BenchmarkIsIPAddress tests performance of IP address detection
func BenchmarkIsIPAddress(b *testing.B) {
	hosts := []string{
		"192.168.1.100",
		"example.com",
		"2001:db8::1",
		"::1",
		"localhost",
	}
	
	b.ReportAllocs()
	
	for b.Loop() {
		for _, host := range hosts {
			_ = isIPAddress(host)
		}
	}
}

// BenchmarkAnonymizePath tests performance of path anonymization
func BenchmarkAnonymizePath(b *testing.B) {
	paths := []string{
		"/stream1/channel1/main",
		"/api/v1/users/12345",
		"/live/camera/stream",
		"/axis-media/media.amp",
		"/h264/ch1/main/av_stream",
	}
	
	b.ReportAllocs()
	
	for b.Loop() {
		for _, path := range paths {
			_ = anonymizePath(path)
		}
	}
}

// BenchmarkPrivacyFunctionsComparison compares performance of different privacy functions
func BenchmarkPrivacyFunctionsComparison(b *testing.B) {
	testURL := "rtsp://admin:password@192.168.1.100:554/stream1"
	testMessage := "Failed to connect to " + testURL
	
	b.Run("ScrubMessage", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = ScrubMessage(testMessage)
		}
	})
	
	b.Run("AnonymizeURL", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = AnonymizeURL(testURL)
		}
	})
	
	b.Run("SanitizeRTSPUrl", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = SanitizeRTSPUrl(testURL)
		}
	})
}

// BenchmarkRegexVsStringOperations compares regex vs string operations performance
func BenchmarkRegexVsStringOperations(b *testing.B) {
	rtspURL := "rtsp://admin:password@192.168.1.100:554/stream1"
	
	b.Run("RegexBased_ScrubMessage", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = ScrubMessage("Error with " + rtspURL)
		}
	})
	
	b.Run("StringBased_SanitizeRTSP", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = SanitizeRTSPUrl(rtspURL)
		}
	})
}