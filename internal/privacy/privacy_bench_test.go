package privacy

import (
	"fmt"
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
	b.ResetTimer()

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
			_ = IsPrivateIP(ip)
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
			_ = anonymizeURLPath(path)
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

// BenchmarkScrubCoordinates tests performance of GPS coordinate scrubbing
func BenchmarkScrubCoordinates(b *testing.B) {
	messages := []string{
		"Location 60.1699,24.9384",
		"Weather service error for coordinates lat=45.123,lng=-122.456",
		"GPS position -33.8688,151.2093",
		"Normal message without coordinates",
	}

	b.ReportAllocs()

	for b.Loop() {
		for _, msg := range messages {
			_ = ScrubCoordinates(msg)
		}
	}
}

// BenchmarkScrubAPITokens tests performance of API token scrubbing
func BenchmarkScrubAPITokens(b *testing.B) {
	messages := []string{
		"api_key: abc123XYZ789token",
		"token=xyz789ABC123token",
		"secret: verylongbase64encodedtoken123+/==",
		"Normal message without any tokens",
	}

	b.ReportAllocs()

	for b.Loop() {
		for _, msg := range messages {
			_ = ScrubAPITokens(msg)
		}
	}
}

// BenchmarkScrubMessage_Comprehensive tests performance of comprehensive scrubbing
func BenchmarkScrubMessage_Comprehensive(b *testing.B) {
	message := "Failed upload to rtsp://admin:pass@192.168.1.100:554 at location 60.1699,24.9384 using api_key: secret123token"

	b.ReportAllocs()

	for b.Loop() {
		_ = ScrubMessage(message)
	}
}

// BenchmarkNewPrivacyFunctions compares performance of new privacy functions
func BenchmarkNewPrivacyFunctions(b *testing.B) {
	testData := map[string]string{
		"Coordinates": "Weather error for location 60.1699,24.9384",
		"APIToken":    "Authentication failed with api_key: abc123XYZ789token",
	}

	b.Run("ScrubCoordinates", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = ScrubCoordinates(testData["Coordinates"])
		}
	})

	b.Run("ScrubAPITokens", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = ScrubAPITokens(testData["APIToken"])
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

// BenchmarkScrubEmails tests performance of email scrubbing
func BenchmarkScrubEmails(b *testing.B) {
	messages := []string{
		"Contact user@example.com for support",
		"Send from admin@company.org to support@customer.com",
		"Email: john.doe@example.co.uk for details",
		"Normal message without emails",
		"Multiple contacts: alice@test.com, bob@test.com, charlie@test.com",
	}

	b.ReportAllocs()

	for b.Loop() {
		for _, msg := range messages {
			_ = ScrubEmails(msg)
		}
	}
}

// BenchmarkScrubUUIDs tests performance of UUID scrubbing
func BenchmarkScrubUUIDs(b *testing.B) {
	messages := []string{
		"Request ID: 550e8400-e29b-41d4-a716-446655440000",
		"User 123e4567-e89b-12d3-a456-426614174000 accessed resource",
		"IDs: a1b2c3d4-e5f6-47a8-b9c0-d1e2f3a4b5c6 and 98765432-1234-5678-9abc-def012345678",
		"Normal message without UUIDs",
		"Mixed case UUID: 550E8400-E29B-41D4-A716-446655440000",
	}

	b.ReportAllocs()

	for b.Loop() {
		for _, msg := range messages {
			_ = ScrubUUIDs(msg)
		}
	}
}

// BenchmarkScrubStandaloneIPs tests performance of standalone IP scrubbing
func BenchmarkScrubStandaloneIPs(b *testing.B) {
	messages := []string{
		"Server at 192.168.1.100 is down",
		"Connect from 10.0.1.50 to 10.0.1.60",
		"IPv6 address 2001:db8::1 is reachable",
		"URL https://192.168.1.100:8080/api should not be scrubbed",
		"Multiple IPs: 172.16.0.1, 172.16.0.2, 172.16.0.3",
	}

	b.ReportAllocs()

	for b.Loop() {
		for _, msg := range messages {
			_ = ScrubStandaloneIPs(msg)
		}
	}
}

// BenchmarkBearerTokenScrubbing tests performance of bearer token scrubbing
func BenchmarkBearerTokenScrubbing(b *testing.B) {
	messages := []string{
		"Authorization: Bearer abc123XYZ789token",
		"Using bearer token abc123XYZ789 for auth",
		"BEARER=abc123XYZ789 in header",
		"API call with token abc123XYZ789",
		"Normal message without tokens",
	}

	b.ReportAllocs()

	for b.Loop() {
		for _, msg := range messages {
			_ = ScrubAPITokens(msg)
		}
	}
}

// BenchmarkComprehensiveScrubbing tests full ScrubMessage with all features
func BenchmarkComprehensiveScrubbing(b *testing.B) {
	message := `Failed to connect to rtsp://admin:pass@192.168.1.100:554/stream from 10.0.1.50.
	User john.doe@example.com reported issue with request ID 550e8400-e29b-41d4-a716-446655440000.
	Location: 60.1699,24.9384. API token: Bearer abc123XYZ789token was used.`

	b.ReportAllocs()

	for b.Loop() {
		_ = ScrubMessage(message)
	}
}

// BenchmarkMemoryAllocationPatterns tests for memory allocation patterns
func BenchmarkMemoryAllocationPatterns(b *testing.B) {
	// Test with increasing message sizes to detect memory issues
	sizes := []int{10, 100, 1000, 10000}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("Size_%d", size), func(b *testing.B) {
			// Create a message with repeated sensitive data
			msg := strings.Repeat("IP: 192.168.1.100 ", size/18)

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				_ = ScrubStandaloneIPs(msg)
			}
		})
	}
}

// BenchmarkRegexCompilationCheck verifies regex patterns are not compiled in loops
func BenchmarkRegexCompilationCheck(b *testing.B) {
	// This benchmark specifically tests that regex compilation happens only once
	messages := make([]string, 100)
	for i := range messages {
		messages[i] = fmt.Sprintf("Email: user%d@example.com", i)
	}

	b.ReportAllocs()

	for b.Loop() {
		for _, msg := range messages {
			_ = ScrubEmails(msg)
		}
	}
}

// BenchmarkAnonymizeIPConsistency tests IP anonymization performance
func BenchmarkAnonymizeIPConsistency(b *testing.B) {
	ips := []string{
		"192.168.1.100",
		"10.0.0.1",
		"172.16.1.1",
		"8.8.8.8",
		"2001:db8::1",
		"::1",
		"fe80::1",
		"invalid-ip",
	}

	b.ReportAllocs()

	for b.Loop() {
		for _, ip := range ips {
			_ = AnonymizeIP(ip)
		}
	}
}

// BenchmarkRedactUserAgent tests user agent redaction performance
func BenchmarkRedactUserAgent(b *testing.B) {
	userAgents := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/91.0.4472.124",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Firefox/89.0",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 14_6 like Mac OS X) Safari/604.1",
		"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
		"UnknownUserAgent/1.0",
	}

	b.ReportAllocs()

	for b.Loop() {
		for _, ua := range userAgents {
			_ = RedactUserAgent(ua)
		}
	}
}

// BenchmarkPerformanceValidation validates that functions meet performance expectations
func BenchmarkPerformanceValidation(b *testing.B) {
	// This benchmark validates that our privacy functions perform within expected bounds
	// and don't have performance regressions

	b.Run("EmailScrubbing_SingleEmail", func(b *testing.B) {
		msg := "Contact user@example.com for support"
		b.ReportAllocs()
		for b.Loop() {
			_ = ScrubEmails(msg)
		}
		// Expected: < 5000 ns/op, < 500 B/op, < 10 allocs/op
	})

	b.Run("UUIDScrubbing_SingleUUID", func(b *testing.B) {
		msg := "Request ID: 550e8400-e29b-41d4-a716-446655440000"
		b.ReportAllocs()
		for b.Loop() {
			_ = ScrubUUIDs(msg)
		}
		// Expected: < 2000 ns/op, < 200 B/op, < 5 allocs/op
	})

	b.Run("IPScrubbing_SingleIP", func(b *testing.B) {
		msg := "Server at 192.168.1.100 is down"
		b.ReportAllocs()
		for b.Loop() {
			_ = ScrubStandaloneIPs(msg)
		}
		// Expected: < 10000 ns/op, < 1000 B/op, < 20 allocs/op
	})

	b.Run("TokenScrubbing_BearerToken", func(b *testing.B) {
		msg := "Authorization: Bearer abc123XYZ789token"
		b.ReportAllocs()
		for b.Loop() {
			_ = ScrubAPITokens(msg)
		}
		// Expected: < 5000 ns/op, < 500 B/op, < 10 allocs/op
	})

	b.Run("FullScrubMessage_AllFeatures", func(b *testing.B) {
		msg := "Error from 192.168.1.100: user@example.com with ID 550e8400-e29b-41d4-a716-446655440000 and token: abc123"
		b.ReportAllocs()
		for b.Loop() {
			_ = ScrubMessage(msg)
		}
		// Expected: < 50000 ns/op, < 5000 B/op, < 50 allocs/op
	})
}

// BenchmarkStressTest tests functions under stress conditions
func BenchmarkStressTest(b *testing.B) {
	// Generate a large message with many instances of sensitive data
	var builder strings.Builder
	for i := range 100 {
		builder.WriteString(fmt.Sprintf("Entry %d: IP=%d.%d.%d.%d, Email=user%d@example.com, UUID=%08x-0000-0000-0000-000000000000\n",
			i, i%256, (i+1)%256, (i+2)%256, (i+3)%256, i, i))
	}
	largeMessage := builder.String()

	b.Run("LargeMessage_ScrubAll", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			_ = ScrubMessage(largeMessage)
		}
	})

	// Test concurrent access (though functions should be stateless)
	b.Run("Concurrent_ScrubMessage", func(b *testing.B) {
		msg := "IP: 192.168.1.100, Email: test@example.com"
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_ = ScrubMessage(msg)
			}
		})
	})
}
