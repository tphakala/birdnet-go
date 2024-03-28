BINARY_DIR := bin
BINARY_NAME := birdnet-go
TFLITE_VERSION := v2.14.0

# Common flags
CGO_FLAGS := CGO_ENABLED=1 CGO_CFLAGS="-I$(HOME)/src/tensorflow"
LDFLAGS := -ldflags "-s -w"

# Detect host architecture
UNAME_M := $(shell uname -m)
ifeq ($(UNAME_M),x86_64)
    NATIVE_TARGET := linux_amd64
else ifeq ($(UNAME_M),aarch64)
    NATIVE_TARGET := linux_arm64
else
    $(error Unsupported architecture)
endif

LABELS_FILES := $(wildcard internal/birdnet/labels/*)
LABELS_ZIP := internal/birdnet/labels.zip

# Default action
all: $(LABELS_ZIP) $(NATIVE_TARGET)

# Check required tools: go, unzip, git
check-tools:
	@which go >/dev/null || { echo "go not found. Please download Go 1.22 or newer from https://go.dev/dl/ and follow the installation instructions."; exit 1; }
	@which unzip >/dev/null || { echo "unzip not found. Please install it using 'sudo apt-get install -y unzip'."; exit 1; }
	@which git >/dev/null || { echo "git not found. Please install it using 'sudo apt-get install -y git'."; exit 1; }

# Check and clone TensorFlow if not exists
check-tensorflow:
	@if [ ! -f "$(HOME)/src/tensorflow/tensorflow/lite/c/c_api.h" ]; then \
		echo "TensorFlow Lite C API header not found. Cloning TensorFlow source..."; \
		mkdir -p $(HOME)/src; \
		git clone --branch v2.14.0 --depth 1 https://github.com/tensorflow/tensorflow.git $(HOME)/src/tensorflow; \
	else \
		echo "TensorFlow Lite C API header exists."; \
	fi

# Download and extract TensorFlow Lite C library
download-tflite:
	@echo "Downloading TensorFlow Lite C library $(TFLITE_LIB)"
	@wget -q https://github.com/tphakala/tflite_c/releases/download/$(TFLITE_VERSION)/$(TFLITE_LIB) -P ./
	@mkdir -p tflite_c
	@if [ $(suffix $(TFLITE_LIB)) = .zip ]; then \
		unzip -o $(TFLITE_LIB) -d tflite_c; \
	else \
		tar -xzf $(TFLITE_LIB) -C tflite_c; \
	fi

# Install TensorFlow Lite C library
install-tflite:
	@sudo cp tflite_c/libtensorflowlite_c.so $(INSTALL_PATH)
	@sudo ldconfig

# Install TensorFlow Lite C library
install-tflite-windows:
	@sudo cp tflite_c/libtensorflowlite_c.dll $(INSTALL_PATH)

# labels.zip depends on all files in the labels directory
$(LABELS_ZIP): $(LABELS_FILES)
	@echo "Creating or updating labels.zip from contents of internal/birdnet/labels/*"
	@cd internal/birdnet/labels && zip -j $(CURDIR)/$(LABELS_ZIP) *

# Build for Linux amd64
linux_amd64: GOOS=linux
linux_amd64: GOARCH=amd64
linux_amd64: INSTALL_PATH=/usr/lib
linux_amd64: TFLITE_LIB=tflite_c_$(TFLITE_VERSION)_$(GOOS)_$(GOARCH).tar.gz
linux_amd64: $(LABELS_ZIP) check-tools check-tensorflow download-tflite install-tflite
	GOOS=linux GOARCH=amd64 $(CGO_FLAGS) go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)

# Build for Linux arm64, with cross-compilation setup if on amd64
linux_arm64: GOOS=linux
linux_arm64: GOARCH=arm64
linux_arm64: INSTALL_PATH=/usr/lib
linux_arm64: TFLITE_LIB=tflite_c_$(TFLITE_VERSION)_$(GOOS)_$(GOARCH).tar.gz
linux_arm64: $(LABELS_ZIP) check-tools check-tensorflow download-tflite install-tflite
ifeq ($(UNAME_M),x86_64)
	@# Cross-compilation setup for amd64 to arm64
	CC=aarch64-linux-gnu-gcc $(CGO_FLAGS) GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)
else
	@# Native compilation for arm64
	$(CGO_FLAGS) GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)
endif

# Windows build
windows: GOOS=windows
windows: GOARCH=amd64
windows: INSTALL_PATH=/usr/lib
windows: TFLITE_LIB=tflite_c_$(TFLITE_VERSION)_$(GOOS)_$(GOARCH).zip
windows: $(LABELS_ZIP) check-tools check-tensorflow download-tflite install-tflite-windows
	$(CGO_FLAGS) CC=x86_64-w64-mingw32-gcc go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME).exe

# macOS Intel build
macos_intel: GOOS=darwin
macos_intel: GOARCH=amd64
macos_intel: TFLITE_LIB=tflite_c_$(TFLITE_VERSION)_$(GOOS)_$(GOARCH).tar.gz
macos_intel: $(LABELS_ZIP) check-tools check-tensorflow download-tflite install-tflite
	$(CGO_FLAGS) go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)

# macOS ARM build
macos_arm: GOOS=darwin
macos_arm: GOARCH=arm64
macos_arm: TFLITE_LIB=tflite_c_$(TFLITE_VERSION)_$(GOOS)_$(GOARCH).tar.gz
macos_arm: $(LABELS_ZIP) check-tools check-tensorflow download-tflite install-tflite build
	$(CGO_FLAGS) go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)

dev_server: 
	$(CGO_FLAGS) air realtime

clean:
	go clean
	rm -rf $(BINARY_DIR)/* tflite_c *.tar.gz *.zip

