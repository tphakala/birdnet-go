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
CGO_FLAGS := CGO_ENABLED=1 CGO_CFLAGS="-I$(HOME)/src/tensorflow"
LDFLAGS := -ldflags "-s -w"

# Detect host architecture
UNAME_M := $(shell uname -m)

LABELS_FILES := $(wildcard internal/birdnet/labels/*)
LABELS_ZIP := internal/birdnet/labels.zip

# Default action
all: $(LABELS_ZIP) build

# Check required tools: go, unzip, git
check-tools:
	@which go >/dev/null || { echo "go not found. Please download Go 1.22 or newer from https://go.dev/dl/ and follow the installation instructions."; exit 1; }
	@which unzip >/dev/null || { sudo apt install -y unzip; }
	@which git >/dev/null || { sudo apt install -y git; }

# Check and clone TensorFlow if not exists
check-tensorflow:
	@if [ ! -f "$(HOME)/src/tensorflow/tensorflow/lite/c/c_api.h" ]; then \
		echo "TensorFlow Lite C API header not found. Cloning TensorFlow source..."; \
		mkdir -p $(HOME)/src; \
		git clone --branch v2.14.0 --depth 1 https://github.com/tensorflow/tensorflow.git $(HOME)/src/tensorflow; \
	else \
		echo "TensorFlow Lite C API header exists."; \
	fi

# labels.zip depends on all files in the labels directory
$(LABELS_ZIP): $(LABELS_FILES)
	@echo "Creating or updating labels.zip from contents of internal/birdnet/labels/*"
	@cd internal/birdnet/labels && zip -j $(CURDIR)/$(LABELS_ZIP) *

# Default build for local development
build: check-tensorflow
	$(CGO_FLAGS) go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)

# Build for Linux amd64
linux_amd64:
	GOOS=linux GOARCH=amd64 $(CGO_FLAGS) go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)

# Build for Linux arm64, with cross-compilation setup if on amd64
linux_arm64:
ifeq ($(UNAME_M),x86_64)
	@# Cross-compilation setup for amd64 to arm64
	CC=aarch64-linux-gnu-gcc $(CGO_FLAGS) GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)
else
	@# Native compilation for arm64
	$(CGO_FLAGS) GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)
endif

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
	rm -rf $(BINARY_DIR)/*
