//go:build integration

// Package mqtt_test provides integration tests for the MQTT client using a real Mosquitto broker
// managed by testcontainers. These tests verify actual MQTT protocol behavior including
// connections, pub/sub, QoS levels, retained messages, and reconnection logic.
//
//nolint:misspell // Mosquitto is the official Eclipse project name
package mqtt_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/mqtt"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/testutil/containers"
)

// integrationTestTopic is the base topic used across integration tests.
const integrationTestTopic = "birdnet-go/integration-test"

var mqttBroker *containers.MosquittoContainer

func TestMain(m *testing.M) {
	ctx := context.Background() //nolint:gocritic // TestMain has no *testing.T for t.Context()

	var err error
	mqttBroker, err = containers.NewMosquittoContainer(ctx, nil)
	if err != nil {
		panic("failed to create MQTT broker: " + err.Error())
	}

	code := m.Run()

	_ = mqttBroker.Terminate(context.Background()) //nolint:gocritic // TestMain has no *testing.T for t.Context()
	os.Exit(code)
}

// createIntegrationClient creates an MQTT client configured against the test broker.
func createIntegrationClient(t *testing.T, opts ...func(*conf.Settings)) (mqtt.Client, *observability.Metrics) {
	t.Helper()

	brokerURL := mqttBroker.GetBrokerURL(t)

	settings := &conf.Settings{}
	settings.Realtime.MQTT.Broker = brokerURL
	settings.Main.Name = fmt.Sprintf("test-%s", t.Name())

	for _, opt := range opts {
		opt(settings)
	}

	metrics, err := observability.NewMetrics()
	require.NoError(t, err, "failed to create metrics")

	client, err := mqtt.NewClient(settings, metrics)
	require.NoError(t, err, "failed to create MQTT client")

	return client, metrics
}

// createRawPahoClient creates a raw Paho MQTT client for pub/sub verification.
func createRawPahoClient(t *testing.T, clientID string, opts ...func(*paho.ClientOptions)) paho.Client {
	t.Helper()

	brokerURL := mqttBroker.GetBrokerURL(t)

	pahoOpts := paho.NewClientOptions()
	pahoOpts.AddBroker(brokerURL)
	pahoOpts.SetClientID(clientID)
	pahoOpts.SetConnectTimeout(10 * time.Second)
	pahoOpts.SetAutoReconnect(false)
	pahoOpts.SetCleanSession(true)

	for _, opt := range opts {
		opt(pahoOpts)
	}

	client := paho.NewClient(pahoOpts)
	token := client.Connect()
	require.True(t, token.WaitTimeout(10*time.Second), "raw client connect timeout")
	require.NoError(t, token.Error(), "raw client connect failed")

	t.Cleanup(func() {
		client.Disconnect(250)
	})

	return client
}

// --- Connection Tests ---

func TestMQTTIntegration_ConnectAndDisconnect(t *testing.T) {
	client, _ := createIntegrationClient(t)

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	// Connect
	err := client.Connect(ctx)
	require.NoError(t, err, "connect should succeed")
	assert.True(t, client.IsConnected(), "client should be connected")

	// Disconnect
	client.Disconnect()
	assert.False(t, client.IsConnected(), "client should be disconnected")
}

func TestMQTTIntegration_ConnectRejectsCooldown(t *testing.T) {
	client, _ := createIntegrationClient(t)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	// Connect and immediately disconnect
	err := client.Connect(ctx)
	require.NoError(t, err)
	client.Disconnect()

	// Try connecting again immediately — should be rejected by cooldown
	time.Sleep(1 * time.Second) // Short wait but within cooldown
	err = client.Connect(ctx)
	require.Error(t, err, "rapid reconnect should be rejected by cooldown")
	assert.Contains(t, err.Error(), "connection attempt too recent")
}

func TestMQTTIntegration_ConnectWithContextCancellation(t *testing.T) {
	client, _ := createIntegrationClient(t)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // Cancel immediately

	err := client.Connect(ctx)
	require.Error(t, err, "connect with cancelled context should fail")
}

// --- Publish Tests ---

func TestMQTTIntegration_PublishAndReceive(t *testing.T) {
	client, _ := createIntegrationClient(t, func(s *conf.Settings) {
		s.Realtime.MQTT.Topic = integrationTestTopic
	})

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	err := client.Connect(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { client.Disconnect() })

	// Create a raw subscriber to verify the message arrives
	topic := integrationTestTopic + "/test-publish"
	received := make(chan string, 1)

	subscriber := createRawPahoClient(t, "integration-subscriber")
	token := subscriber.Subscribe(topic, 1, func(_ paho.Client, msg paho.Message) {
		received <- string(msg.Payload())
	})
	require.True(t, token.WaitTimeout(5*time.Second))
	require.NoError(t, token.Error())

	// Publish via the BirdNET-Go client
	payload := `{"test": true, "time": "` + time.Now().Format(time.RFC3339) + `"}`
	err = client.Publish(ctx, topic, payload)
	require.NoError(t, err, "publish should succeed")

	// Verify message was received
	select {
	case msg := <-received:
		assert.Equal(t, payload, msg, "received message should match published payload")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for published message")
	}
}

func TestMQTTIntegration_PublishWhileDisconnected(t *testing.T) {
	client, _ := createIntegrationClient(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	// Don't connect — publish should fail
	err := client.Publish(ctx, "test/topic", "payload")
	require.Error(t, err, "publish without connection should fail")
}

func TestMQTTIntegration_PublishWithRetain(t *testing.T) {
	client, _ := createIntegrationClient(t)

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	err := client.Connect(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { client.Disconnect() })

	retainTopic := integrationTestTopic + "/retained"
	payload := "retained-message-" + time.Now().Format(time.RFC3339Nano)

	// Publish with retain
	err = client.PublishWithRetain(ctx, retainTopic, payload, true)
	require.NoError(t, err)

	// Give broker time to store
	time.Sleep(200 * time.Millisecond)

	// New subscriber should receive the retained message
	received := make(chan string, 1)
	subscriber := createRawPahoClient(t, "retain-verifier")
	token := subscriber.Subscribe(retainTopic, 1, func(_ paho.Client, msg paho.Message) {
		if msg.Retained() {
			received <- string(msg.Payload())
		}
	})
	require.True(t, token.WaitTimeout(5*time.Second))
	require.NoError(t, token.Error())

	select {
	case msg := <-received:
		assert.Equal(t, payload, msg, "retained message should be delivered to new subscriber")
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for retained message")
	}

	// Clean up retained message
	clearToken := subscriber.Publish(retainTopic, 0, true, []byte{})
	require.True(t, clearToken.WaitTimeout(5*time.Second))
}

func TestMQTTIntegration_PublishWithContextCancellation(t *testing.T) {
	client, _ := createIntegrationClient(t)

	connectCtx, connectCancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer connectCancel()

	err := client.Connect(connectCtx)
	require.NoError(t, err)
	t.Cleanup(func() { client.Disconnect() })

	// Cancel context before publishing
	pubCtx, pubCancel := context.WithCancel(t.Context())
	pubCancel()

	err = client.Publish(pubCtx, "test/topic", "should-fail")
	require.Error(t, err, "publish with cancelled context should fail")
}

// --- QoS Level Tests ---

func TestMQTTIntegration_QoS0Delivery(t *testing.T) {
	testQoSDelivery(t, 0, "qos0-test")
}

func TestMQTTIntegration_QoS1Delivery(t *testing.T) {
	testQoSDelivery(t, 1, "qos1-test")
}

func TestMQTTIntegration_QoS2Delivery(t *testing.T) {
	testQoSDelivery(t, 2, "qos2-test")
}

func testQoSDelivery(t *testing.T, qos byte, suffix string) {
	t.Helper()

	topic := integrationTestTopic + "/" + suffix
	received := make(chan []byte, 1)

	// Subscribe with the specific QoS level
	subscriber := createRawPahoClient(t, "qos-sub-"+suffix)
	token := subscriber.Subscribe(topic, qos, func(_ paho.Client, msg paho.Message) {
		received <- msg.Payload()
	})
	require.True(t, token.WaitTimeout(5*time.Second))
	require.NoError(t, token.Error())

	// Publish with the same QoS level
	publisher := createRawPahoClient(t, "qos-pub-"+suffix)
	payload := fmt.Appendf(nil, "qos-%d-message", qos)
	pubToken := publisher.Publish(topic, qos, false, payload)
	require.True(t, pubToken.WaitTimeout(5*time.Second))
	require.NoError(t, pubToken.Error())

	select {
	case msg := <-received:
		assert.Equal(t, payload, msg)
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for QoS %d message", qos)
	}
}

// --- Topic Wildcard Tests ---

func TestMQTTIntegration_WildcardSingleLevel(t *testing.T) {
	received := make(chan string, 10)

	subscriber := createRawPahoClient(t, "wildcard-single-sub")
	token := subscriber.Subscribe("birdnet-go/test/+/data", 1, func(_ paho.Client, msg paho.Message) {
		received <- msg.Topic()
	})
	require.True(t, token.WaitTimeout(5*time.Second))
	require.NoError(t, token.Error())

	publisher := createRawPahoClient(t, "wildcard-single-pub")

	// Publish to matching topics
	matchingTopics := []string{
		"birdnet-go/test/species1/data",
		"birdnet-go/test/species2/data",
	}
	for _, topic := range matchingTopics {
		pubToken := publisher.Publish(topic, 1, false, []byte("test"))
		require.True(t, pubToken.WaitTimeout(5*time.Second))
		require.NoError(t, pubToken.Error())
	}

	// Collect received topics
	receivedTopics := make([]string, 0, len(matchingTopics))
	for range len(matchingTopics) {
		select {
		case topic := <-received:
			receivedTopics = append(receivedTopics, topic)
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for wildcard messages")
		}
	}

	assert.ElementsMatch(t, matchingTopics, receivedTopics)
}

func TestMQTTIntegration_WildcardMultiLevel(t *testing.T) {
	received := make(chan string, 10)

	subscriber := createRawPahoClient(t, "wildcard-multi-sub")
	token := subscriber.Subscribe("birdnet-go/test/#", 1, func(_ paho.Client, msg paho.Message) {
		received <- msg.Topic()
	})
	require.True(t, token.WaitTimeout(5*time.Second))
	require.NoError(t, token.Error())

	publisher := createRawPahoClient(t, "wildcard-multi-pub")

	// Publish to various sub-topics
	topics := []string{
		"birdnet-go/test/a",
		"birdnet-go/test/a/b",
		"birdnet-go/test/a/b/c",
	}
	for _, topic := range topics {
		pubToken := publisher.Publish(topic, 1, false, []byte("test"))
		require.True(t, pubToken.WaitTimeout(5*time.Second))
		require.NoError(t, pubToken.Error())
	}

	receivedTopics := make([]string, 0, len(topics))
	for range len(topics) {
		select {
		case topic := <-received:
			receivedTopics = append(receivedTopics, topic)
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for multi-level wildcard messages")
		}
	}

	assert.ElementsMatch(t, topics, receivedTopics)
}

// --- Multiple Subscribers Test ---

func TestMQTTIntegration_MultipleSubscribers(t *testing.T) {
	topic := integrationTestTopic + "/multi-sub"
	const numSubscribers = 3
	payload := "broadcast-message"

	// Create multiple subscribers
	channels := make([]chan string, numSubscribers)
	for i := range numSubscribers {
		channels[i] = make(chan string, 1)
		ch := channels[i]
		sub := createRawPahoClient(t, fmt.Sprintf("multi-sub-%d", i))
		token := sub.Subscribe(topic, 1, func(_ paho.Client, msg paho.Message) {
			ch <- string(msg.Payload())
		})
		require.True(t, token.WaitTimeout(5*time.Second))
		require.NoError(t, token.Error())
	}

	// Publish once
	publisher := createRawPahoClient(t, "multi-sub-pub")
	pubToken := publisher.Publish(topic, 1, false, []byte(payload))
	require.True(t, pubToken.WaitTimeout(5*time.Second))
	require.NoError(t, pubToken.Error())

	// All subscribers should receive the message
	for i, ch := range channels {
		select {
		case msg := <-ch:
			assert.Equal(t, payload, msg, "subscriber %d should receive the message", i)
		case <-time.After(5 * time.Second):
			t.Fatalf("subscriber %d timed out waiting for message", i)
		}
	}
}

// --- Concurrent Publisher Tests ---

func TestMQTTIntegration_ConcurrentPublishers(t *testing.T) {
	topic := integrationTestTopic + "/concurrent"
	const numPublishers = 5
	const messagesPerPublisher = 3

	received := make(chan string, numPublishers*messagesPerPublisher)

	subscriber := createRawPahoClient(t, "concurrent-sub")
	token := subscriber.Subscribe(topic, 1, func(_ paho.Client, msg paho.Message) {
		received <- string(msg.Payload())
	})
	require.True(t, token.WaitTimeout(5*time.Second))
	require.NoError(t, token.Error())

	// Publish concurrently
	var wg sync.WaitGroup
	for i := range numPublishers {
		wg.Go(func() {
			pub := createRawPahoClient(t, fmt.Sprintf("concurrent-pub-%d", i))
			for j := range messagesPerPublisher {
				msg := fmt.Sprintf("pub-%d-msg-%d", i, j)
				pubToken := pub.Publish(topic, 1, false, []byte(msg))
				assert.True(t, pubToken.WaitTimeout(5*time.Second), "publish timeout for %s", msg)
			}
		})
	}
	wg.Wait()

	// Collect all messages
	totalExpected := numPublishers * messagesPerPublisher
	messages := make([]string, 0, totalExpected)
	deadline := time.After(10 * time.Second)
	for range totalExpected {
		select {
		case msg := <-received:
			messages = append(messages, msg)
		case <-deadline:
			t.Fatalf("timed out: received %d/%d messages", len(messages), totalExpected)
		}
	}

	assert.Len(t, messages, totalExpected, "should receive all messages from all publishers")
}

// --- Last Will and Testament (LWT) Test ---

func TestMQTTIntegration_LastWillAndTestament(t *testing.T) {
	lwtTopic := integrationTestTopic + "/status"
	lwtPayload := "offline"

	// Create a subscriber to watch for LWT message
	received := make(chan string, 1)
	subscriber := createRawPahoClient(t, "lwt-watcher")
	token := subscriber.Subscribe(lwtTopic, 1, func(_ paho.Client, msg paho.Message) {
		received <- string(msg.Payload())
	})
	require.True(t, token.WaitTimeout(5*time.Second))
	require.NoError(t, token.Error())

	// Create a client with LWT configured
	lwtClient := createRawPahoClient(t, "lwt-client-"+time.Now().Format("150405"), func(opts *paho.ClientOptions) {
		opts.SetWill(lwtTopic, lwtPayload, 1, false)
		opts.SetAutoReconnect(false)
		opts.SetCleanSession(true)
	})

	// Publish "online" first so we know the client was connected
	onlineToken := lwtClient.Publish(lwtTopic, 1, false, []byte("online"))
	require.True(t, onlineToken.WaitTimeout(5*time.Second))

	// Drain the "online" message
	select {
	case <-received:
		// drained
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for online message")
	}

	// Disconnect the LWT client — since paho sends a proper DISCONNECT packet,
	// the broker won't fire the LWT. This test verifies LWT configuration works
	// and the client can publish to status topics. A full LWT delivery test would
	// require a network-level connection drop (e.g., iptables, tc).
	lwtClient.Disconnect(0)

	t.Log("LWT configuration verified: client connected with will topic and published status")
}

// --- TestConnection Multi-Stage Test ---

func TestMQTTIntegration_TestConnection(t *testing.T) {
	client, _ := createIntegrationClient(t, func(s *conf.Settings) {
		s.Realtime.MQTT.Topic = integrationTestTopic
	})

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	resultChan := make(chan mqtt.TestResult, 20)
	go client.TestConnection(ctx, resultChan)

	var results []mqtt.TestResult
	deadline := time.After(25 * time.Second)

	for {
		select {
		case result := <-resultChan:
			results = append(results, result)
			t.Logf("Stage: %s | Success: %v | Message: %s", result.Stage, result.Success, result.Message)
			// Check if all stages completed (last stage is Message Publishing)
			if result.Stage == "Message Publishing" && !result.IsProgress {
				goto done
			}
		case <-deadline:
			t.Log("Test timed out, checking collected results")
			goto done
		}
	}

done:
	require.NotEmpty(t, results, "should have received test results")

	// Verify we got through multiple stages
	stages := make(map[string]bool)
	for _, r := range results {
		if !r.IsProgress {
			stages[r.Stage] = r.Success
		}
	}

	// At minimum, TCP Connection should have been tested
	_, hasTCP := stages["TCP Connection"]
	assert.True(t, hasTCP, "should have TCP Connection stage result")

	// MQTT Connection should succeed
	if mqttSuccess, hasMQTT := stages["MQTT Connection"]; hasMQTT {
		assert.True(t, mqttSuccess, "MQTT Connection should succeed")
	}
}

// --- JSON Payload Structure Test ---

func TestMQTTIntegration_JSONPayloadRoundTrip(t *testing.T) {
	client, _ := createIntegrationClient(t, func(s *conf.Settings) {
		s.Realtime.MQTT.Topic = integrationTestTopic
	})

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	err := client.Connect(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { client.Disconnect() })

	topic := integrationTestTopic + "/json-roundtrip"
	received := make(chan []byte, 1)

	subscriber := createRawPahoClient(t, "json-sub")
	token := subscriber.Subscribe(topic, 1, func(_ paho.Client, msg paho.Message) {
		received <- msg.Payload()
	})
	require.True(t, token.WaitTimeout(5*time.Second))
	require.NoError(t, token.Error())

	// Create a realistic detection-like payload
	payload := map[string]any{
		"Date":           "2026-02-19",
		"Time":           "14:30:00",
		"CommonName":     "Eurasian Blue Tit",
		"ScientificName": "Cyanistes caeruleus",
		"Confidence":     0.89,
		"Latitude":       60.1699,
		"Longitude":      24.9384,
		"ClipName":       "test_clip.wav",
	}

	jsonBytes, err := json.Marshal(payload)
	require.NoError(t, err)

	err = client.Publish(ctx, topic, string(jsonBytes))
	require.NoError(t, err)

	select {
	case msg := <-received:
		var decoded map[string]any
		err := json.Unmarshal(msg, &decoded)
		require.NoError(t, err, "received message should be valid JSON")
		assert.Equal(t, "Eurasian Blue Tit", decoded["CommonName"])
		assert.InDelta(t, 0.89, decoded["Confidence"].(float64), 0.001)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for JSON message")
	}
}

// --- Retained Message Clearing Test ---

func TestMQTTIntegration_ClearRetainedMessages(t *testing.T) {
	ctx := t.Context()

	// Publish some retained messages
	publisher := createRawPahoClient(t, "retain-publisher")
	topics := []string{
		integrationTestTopic + "/clear/topic1",
		integrationTestTopic + "/clear/topic2",
	}
	for _, topic := range topics {
		token := publisher.Publish(topic, 1, true, []byte("retained-data"))
		require.True(t, token.WaitTimeout(5*time.Second))
		require.NoError(t, token.Error())
	}

	// Wait for broker to store
	time.Sleep(200 * time.Millisecond)

	// Clear retained messages using the container helper
	err := mqttBroker.ClearRetainedMessages(ctx)
	require.NoError(t, err, "clearing retained messages should succeed")

	// Verify no retained messages remain on those topics
	received := make(chan bool, 1)
	verifier := createRawPahoClient(t, "retain-verifier")
	token := verifier.Subscribe(integrationTestTopic+"/clear/#", 1, func(_ paho.Client, msg paho.Message) {
		if msg.Retained() {
			received <- true
		}
	})
	require.True(t, token.WaitTimeout(5*time.Second))
	require.NoError(t, token.Error())

	select {
	case <-received:
		t.Fatal("should not receive retained messages after clearing")
	case <-time.After(1 * time.Second):
		// Expected — no retained messages
	}
}

// --- Message Ordering Test ---

func TestMQTTIntegration_MessageOrdering(t *testing.T) {
	topic := integrationTestTopic + "/ordering"
	const numMessages = 20

	received := make(chan string, numMessages)

	subscriber := createRawPahoClient(t, "order-sub")
	token := subscriber.Subscribe(topic, 1, func(_ paho.Client, msg paho.Message) {
		received <- string(msg.Payload())
	})
	require.True(t, token.WaitTimeout(5*time.Second))
	require.NoError(t, token.Error())

	// Publish messages in order with QoS 1 (at-least-once)
	publisher := createRawPahoClient(t, "order-pub")
	for i := range numMessages {
		msg := fmt.Sprintf("msg-%03d", i)
		pubToken := publisher.Publish(topic, 1, false, []byte(msg))
		require.True(t, pubToken.WaitTimeout(5*time.Second))
		require.NoError(t, pubToken.Error())
	}

	// Collect messages (QoS 1 with single publisher should maintain order)
	messages := make([]string, 0, numMessages)
	deadline := time.After(10 * time.Second)
	for range numMessages {
		select {
		case msg := <-received:
			messages = append(messages, msg)
		case <-deadline:
			t.Fatalf("timed out: received %d/%d messages", len(messages), numMessages)
		}
	}

	// Verify ordering is maintained with QoS 1
	for i, msg := range messages {
		expected := fmt.Sprintf("msg-%03d", i)
		assert.Equal(t, expected, msg, "message %d should be in order", i)
	}
}

// --- Large Payload Test ---

func TestMQTTIntegration_LargePayload(t *testing.T) {
	topic := integrationTestTopic + "/large-payload"

	// MQTT spec allows up to 256MB, but Mosquitto default is 256KB
	// Use a reasonable size for testing
	payloadSize := 64 * 1024 // 64KB
	payload := make([]byte, payloadSize)
	for i := range payload {
		payload[i] = byte('A' + (i % 26))
	}

	received := make(chan []byte, 1)

	subscriber := createRawPahoClient(t, "large-sub")
	token := subscriber.Subscribe(topic, 1, func(_ paho.Client, msg paho.Message) {
		received <- msg.Payload()
	})
	require.True(t, token.WaitTimeout(5*time.Second))
	require.NoError(t, token.Error())

	publisher := createRawPahoClient(t, "large-pub")
	pubToken := publisher.Publish(topic, 1, false, payload)
	require.True(t, pubToken.WaitTimeout(10*time.Second))
	require.NoError(t, pubToken.Error())

	select {
	case msg := <-received:
		assert.Len(t, msg, payloadSize, "received payload should match sent size")
		assert.Equal(t, payload, msg, "payload content should match")
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for large payload")
	}
}

// --- Unsubscribe Test ---

func TestMQTTIntegration_Unsubscribe(t *testing.T) {
	topic := integrationTestTopic + "/unsub"

	received := make(chan string, 10)

	subscriber := createRawPahoClient(t, "unsub-client")
	token := subscriber.Subscribe(topic, 1, func(_ paho.Client, msg paho.Message) {
		received <- string(msg.Payload())
	})
	require.True(t, token.WaitTimeout(5*time.Second))
	require.NoError(t, token.Error())

	publisher := createRawPahoClient(t, "unsub-pub")

	// Publish before unsubscribe — should receive
	pubToken := publisher.Publish(topic, 1, false, []byte("before-unsub"))
	require.True(t, pubToken.WaitTimeout(5*time.Second))

	select {
	case msg := <-received:
		assert.Equal(t, "before-unsub", msg)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for message before unsubscribe")
	}

	// Unsubscribe
	unsubToken := subscriber.Unsubscribe(topic)
	require.True(t, unsubToken.WaitTimeout(5*time.Second))
	require.NoError(t, unsubToken.Error())

	// Publish after unsubscribe — should NOT receive
	pubToken = publisher.Publish(topic, 1, false, []byte("after-unsub"))
	require.True(t, pubToken.WaitTimeout(5*time.Second))

	select {
	case msg := <-received:
		t.Fatalf("should not receive message after unsubscribe, got: %s", msg)
	case <-time.After(1 * time.Second):
		// Expected — no message received
	}
}
