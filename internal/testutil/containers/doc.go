// Package containers provides testcontainer management for integration tests.
//
// This package offers helpers for starting and managing Docker containers
// during integration testing using testcontainers-go. It includes support for:
//
//   - MySQL 8.0 database containers
//nolint:misspell // Mosquitto is the official Eclipse project name
//   - Eclipse Mosquitto MQTT broker containers
//   - nginx reverse proxy containers
//
// Container Lifecycle:
//
// Containers are typically managed using TestMain in integration test packages:
//
//	var mysqlContainer *containers.MySQLContainer
//
//	func TestMain(m *testing.M) {
//	    var err error
//	    mysqlContainer, err = containers.NewMySQLContainer()
//	    if err != nil {
//	        log.Fatal(err)
//	    }
//	    code := m.Run()
//	    mysqlContainer.Terminate()
//	    os.Exit(code)
//	}
//
// Build Tags:
//
// Integration tests using this package should use the "integration" build tag:
//
//	//go:build integration
//
// Container Reuse:
//
// For local development, enable container reuse to speed up test iterations:
//
//	export TESTCONTAINERS_REUSE=true
//	go test -tags=integration ./...
//
// Note: Reused containers require manual cleanup when done:
//
//	docker ps -a --filter "label=org.testcontainers.reuse=true" -q | xargs docker rm -f
package containers
