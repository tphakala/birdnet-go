BINARY_DIR := bin
BINARY_NAME := birdnet-go

# Common flags
CGO_FLAGS := CGO_ENABLED=1 CGO_CFLAGS="-I$(HOME)/src/tensorflow -DMA_NO_PULSEAUDIO"
LDFLAGS := -ldflags "-s -w"

build:
	$(CGO_FLAGS) go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)

windows:
	GOOS=windows GOARCH=amd64 $(CGO_FLAGS) CC=x86_64-w64-mingw32-gcc go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME).exe

macos_intel:
	GOOS=darwin GOARCH=amd64 $(CGO_FLAGS) go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)

macos_arm:
	GOOS=darwin GOARCH=arm64 $(CGO_FLAGS) go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)

clean:
	go clean
	rm -rf $(BINARY_DIR)/$(BINARY_NAME) $(BINARY_DIR)/$(BINARY_NAME).exe
