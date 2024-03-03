BINARY_DIR := bin
BINARY_NAME := birdnet-go

# Extract GOOS and GOARCH from TARGETPLATFORM
ifeq ($(TARGETPLATFORM),linux/amd64)
    GOOS := linux
    GOARCH := amd64
endif
ifeq ($(TARGETPLATFORM),linux/arm64)
    GOOS := linux
    GOARCH := arm64
endif

# Common flags
CGO_FLAGS := CGO_ENABLED=1 CGO_CFLAGS="-I$(HOME)/src/tensorflow -DMA_NO_PULSEAUDIO"
LDFLAGS := -ldflags "-s -w"

# Default build for local development
build:
	$(CGO_FLAGS) go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)

# Build for Linux amd64
linux_amd64:
	GOOS=linux GOARCH=amd64 $(CGO_FLAGS) go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)

# Build for Linux arm64
linux_arm64:
	GOOS=linux GOARCH=arm64 $(CGO_FLAGS) go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)

# Windows build
windows:
	GOOS=windows GOARCH=amd64 $(CGO_FLAGS) CC=x86_64-w64-mingw32-gcc go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME).exe

# macOS Intel build
macos_intel:
	GOOS=darwin GOARCH=amd64 $(CGO_FLAGS) go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)

# macOS ARM build
macos_arm:
	GOOS=darwin GOARCH=arm64 $(CGO_FLAGS) go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)

clean:
	go clean
	rm -rf $(BINARY_DIR)/$(BINARY_NAME) $(BINARY_DIR)/$(BINARY_NAME).exe
