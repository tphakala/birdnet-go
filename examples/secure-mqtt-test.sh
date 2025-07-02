#!/bin/bash
# Example script to test secure MQTT connections

echo "=== Secure MQTT Connection Test Examples ==="
echo ""

# Example 1: Basic TLS connection
echo "1. Testing basic TLS connection (port 8883):"
echo "   Set broker to: tls://mqtt.example.com:8883"
echo ""

# Example 2: Self-signed certificate
echo "2. For self-signed certificates:"
echo "   - Enable 'Skip Certificate Verification' in UI"
echo "   - Or set in config: tls.insecureSkipVerify: true"
echo ""

# Example 3: Custom CA certificate
echo "3. For custom CA certificates:"
echo "   - Set CA certificate path in UI"
echo "   - Or set in config: tls.caCert: /path/to/ca.crt"
echo ""

# Example 4: Test via API
echo "4. Test connection via API:"
cat << 'EOF'
curl -X POST http://localhost:8080/api/v1/mqtt/test \
  -H "Content-Type: application/json" \
  -d '{
    "enabled": true,
    "broker": "tls://test.mosquitto.org:8883",
    "topic": "birdnet/test",
    "username": "",
    "password": "",
    "tls": {
      "insecureSkipVerify": false
    }
  }'
EOF
echo ""

echo "=== Configuration Examples ==="
echo ""
echo "Add to your config.yaml:"
cat << 'EOF'
realtime:
  mqtt:
    enabled: true
    broker: tls://mqtt.example.com:8883
    topic: birdnet/detections
    username: your-username
    password: your-password
    tls:
      insecureskipverify: false  # Set to true for self-signed certs
      cacert: ""                 # Path to CA certificate
      clientcert: ""             # Path to client certificate
      clientkey: ""              # Path to client key
EOF