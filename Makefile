BINARY_DIR := bin
BINARY_NAME := birdnet-go
TFLITE_VERSION := v2.14.0

# Common flags
CGO_FLAGS := CGO_ENABLED=1 CGO_CFLAGS="-I$(HOME)/src/tensorflow"
LDFLAGS := -ldflags "-s -w -X 'main.buildDate=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)'"

# Detect host OS and architecture
UNAME_S := $(shell uname -s)
UNAME_M := $(shell uname -m)

ifeq ($(UNAME_S),Linux)
    NATIVE_TARGET := linux_$(if $(filter x86_64,$(UNAME_M)),amd64,arm64)
    TFLITE_LIB_DIR := /usr/lib
    TFLITE_LIB_EXT := .so
else ifeq ($(UNAME_S),Darwin)
    NATIVE_TARGET := darwin_$(if $(filter x86_64,$(UNAME_M)),amd64,arm64)
    TFLITE_LIB_DIR := /usr/local/lib
    TFLITE_LIB_EXT := .dylib
else
    $(error Unsupported operating system)
endif

LABELS_FILES := $(wildcard internal/birdnet/data/labels/*)
LABELS_DIR := internal/birdnet/data/labels
LABELS_ZIP := internal/birdnet/data/labels.zip

# Default action
all: $(LABELS_ZIP) $(NATIVE_TARGET)

# Check required tools: go, unzip, git
check-tools:
	@which go >/dev/null || { echo "go not found. Please download Go 1.22 or newer from https://go.dev/dl/ and follow the installation instructions."; exit 1; }
	@which unzip >/dev/null || { echo "unzip not found. Please install it using 'brew install unzip' on macOS or 'sudo apt-get install -y unzip' on Linux."; exit 1; }
	@which git >/dev/null || { echo "git not found. Please install it using 'brew install git' on macOS or 'sudo apt-get install -y git' on Linux."; exit 1; }

# Check and clone TensorFlow if not exists
check-tensorflow:
	@if [ ! -f "$(HOME)/src/tensorflow/tensorflow/lite/c/c_api.h" ]; then \
		echo "TensorFlow Lite C API header not found. Cloning TensorFlow source..."; \
		mkdir -p $(HOME)/src; \
		git clone --branch $(TFLITE_VERSION) --filter=blob:none --depth 1 --no-checkout https://github.com/tensorflow/tensorflow.git $(HOME)/src/tensorflow; \
		git -C $(HOME)/src/tensorflow config core.sparseCheckout true; \
		echo "**/*.h" >> $(HOME)/src/tensorflow/.git/info/sparse-checkout; \
		git -C $(HOME)/src/tensorflow checkout; \
	else \
		echo "TensorFlow Lite C API header exists."; \
	fi

# Download and extract TensorFlow Lite C library
download-tflite: TFLITE_C_FILE=tflite_c_$(TFLITE_VERSION)_$(TFLITE_LIB_ARCH)
download-tflite:
	@if [ ! -f "$(TFLITE_LIB_DIR)/libtensorflowlite_c$(TFLITE_LIB_EXT)" ]; then \
		echo "TensorFlow Lite C library not found. Downloading..."; \
		wget -q https://github.com/tphakala/tflite_c/releases/download/$(TFLITE_VERSION)/$(TFLITE_C_FILE) -P ./; \
		if [ $(suffix $(TFLITE_C_FILE)) = .zip ]; then \
			unzip -o $(TFLITE_C_FILE) -d .; \
		else \
			tar -xzf $(TFLITE_C_FILE) -C .; \
		fi; \
		rm -f $(TFLITE_C_FILE); \
		sudo mkdir -p $(TFLITE_LIB_DIR); \
		sudo cp libtensorflowlite_c* $(TFLITE_LIB_DIR)/; \
		if [ "$(UNAME_S)" = "Linux" ]; then \
			sudo ldconfig; \
		fi; \
	else \
		echo "TensorFlow Lite C library already exists."; \
	fi

# labels.zip depends on all files in the labels directory
$(LABELS_ZIP): $(LABELS_FILES)
	@echo "Creating or updating labels.zip from contents of $(LABELS_DIR)/*"
	@cd $(LABELS_DIR) && zip -j $(CURDIR)/$(LABELS_ZIP) *

# Build for Linux amd64
linux_amd64: TFLITE_LIB_ARCH=linux_amd64.tar.gz
linux_amd64: $(LABELS_ZIP) check-tools check-tensorflow download-tflite
	GOOS=linux GOARCH=amd64 $(CGO_FLAGS) go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)

# Build for Linux arm64, with cross-compilation setup if on amd64
linux_arm64: TFLITE_LIB_ARCH=linux_arm64.tar.gz
linux_arm64: $(LABELS_ZIP) check-tools check-tensorflow download-tflite
ifeq ($(UNAME_M),x86_64)
	@# Cross-compilation setup for amd64 to arm64
	CC=aarch64-linux-gnu-gcc $(CGO_FLAGS) GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)
else
	@# Native compilation for arm64
	$(CGO_FLAGS) GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)
endif

# Windows build
windows_amd64: TFLITE_LIB_DIR="/usr/x86_64-w64-mingw32/lib"
windows_amd64: TFLITE_LIB_ARCH=windows_amd64.zip
windows_amd64: $(LABELS_ZIP) check-tools check-tensorflow download-tflite
	$(CGO_FLAGS) GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME).exe

# macOS Intel build
darwin_amd64: TFLITE_LIB_ARCH=darwin_amd64.tar.gz
darwin_amd64: $(LABELS_ZIP) check-tools check-tensorflow download-tflite
	$(CGO_FLAGS) GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)

# macOS ARM build
darwin_arm64: TFLITE_LIB_ARCH=darwin_arm64.tar.gz
darwin_arm64: $(LABELS_ZIP) check-tools check-tensorflow download-tflite
	$(CGO_FLAGS) GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)

dev_server: REALTIME_ARGS=""
dev_server:
	$(CGO_FLAGS) air realtime $(REALTIME_ARGS)

clean:
	go clean
	rm -rf $(BINARY_DIR)/* tflite_c *.tar.gz *.zip
