#!/bin/bash

# Start the server in background
echo "Starting BirdNET-Go server..."
go run cmd/server/main.go > server.log 2>&1 &
SERVER_PID=$!

# Wait for server to start
echo "Waiting for server to start..."
sleep 5

# Test the API endpoint
echo "Testing species summary API..."
time curl -s "http://localhost:8080/api/v2/analytics/species/summary" > /dev/null

# Kill the server
kill $SERVER_PID

# Show relevant logs
echo ""
echo "Performance logs:"
grep -E "(GetSpeciesSummary|GetBatch|fetchAndStore|Slow fetch)" server.log | head -50