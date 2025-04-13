.PHONY: all warn check-tools check-tensorflow linux_amd64 linux_arm64 windows_amd64 darwin_amd64 darwin_arm64 dev_server clean

# Show warning about Makefile deprecation
warn:
	@echo "⚠️  WARNING: This Makefile is no longer maintained and will be removed in a future release."
	@echo "⚠️  Please use Task instead with: go install github.com/go-task/task/v3/cmd/task@latest"
	@echo "⚠️  Then run commands with 'task' instead of 'make'."
	@echo "⚠️  See Taskfile.yml for available tasks."
	@echo ""

# Default action
all: warn
	@$(MAKE) $(NATIVE_TARGET)

# Make sure other important targets also show the warning
linux_amd64: warn
linux_arm64: warn
windows_amd64: warn
darwin_amd64: warn
darwin_arm64: warn
dev_server: warn
clean: warn

BINARY_DIR := bin
BINARY_NAME := birdnet-go
TFLITE_VERSION := v2.17.1

# Common flags
CGO_FLAGS := CGO_ENABLED=1 CGO_CFLAGS="-I$(HOME)/src/tensorflow"
LDFLAGS = -ldflags "-s -w \
    -X 'main.buildDate=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)' \
    -X 'main.version=$(shell git describe --tags --always)' \
    $(call get_extra_ldflags,$(TARGET))"

# Detect host OS and architecture
UNAME_S := $(shell uname -s)
UNAME_M := $(shell uname -m)

# Tailwind CSS
TAILWIND_INPUT := tailwind.input.css
TAILWIND_OUTPUT := assets/tailwind.css

# Function to determine additional linker flags based on target
define get_extra_ldflags
$(strip \
    $(if $(filter darwin%,$1), \
        -r $(call get_lib_path,$1), \
    ))
endef

# Function to determine library path based on target and host architecture
define get_lib_path
$(strip \
    $(if $(filter linux_amd64,$1), \
        $(if $(filter x86_64,$(UNAME_M)), \
            /usr/lib, \
            /usr/x86_64-linux-gnu/lib \
        ), \
    $(if $(filter linux_arm64,$1), \
        $(if $(filter aarch64,$(UNAME_M)), \
            /usr/lib, \
            /usr/aarch64-linux-gnu/lib \
        ), \
    $(if $(filter windows_amd64,$1), \
        /usr/x86_64-w64-mingw32/lib, \
    $(if $(filter darwin%,$1), \
        /opt/homebrew/lib, \
        /usr/lib \
    )))))
endef

# Function to determine library filename based on target OS
define get_lib_filename
$(strip \
    $(if $(filter windows_amd64,$1), \
        tensorflowlite_c-$(patsubst v%,%,$(TFLITE_VERSION)).dll, \
    $(if $(filter linux%,$1), \
        libtensorflowlite_c.so.$(patsubst v%,%,$(TFLITE_VERSION)), \
    $(if $(filter darwin%,$1), \
        libtensorflowlite_c.$(patsubst v%,%,$(TFLITE_VERSION)).dylib, \
    ))))
endef

# Function to determine CGO flags based on target
define get_cgo_flags
$(strip \
    CGO_ENABLED=1 \
    CGO_CFLAGS="-I$(HOME)/src/tensorflow" \
    $(if $(filter linux_arm64,$1), \
        $(if $(filter x86_64,$(UNAME_M)), \
            CC=aarch64-linux-gnu-gcc \
        ), \
    $(if $(filter windows_amd64,$1), \
        CC=x86_64-w64-mingw32-gcc \
    )))
endef

ifeq ($(UNAME_S),Linux)
    ifeq ($(UNAME_M),x86_64)
        NATIVE_TARGET := linux_amd64
        TFLITE_LIB_DIR := /usr/lib
    else ifeq ($(UNAME_M),aarch64)
        NATIVE_TARGET := linux_arm64
        TFLITE_LIB_DIR := /usr/aarch64-linux-gnu/lib
    endif
    TFLITE_LIB_EXT := .so
else ifeq ($(UNAME_S),Darwin)
    NATIVE_TARGET := darwin_$(if $(filter x86_64,$(UNAME_M)),amd64,arm64)
    TFLITE_LIB_DIR := /opt/homebrew/lib
    TFLITE_LIB_EXT := .dylib
else
    $(error Build is supported only on Linux and macOS)
endif

LABELS_DIR := internal/birdnet/data/labels

# Check required tools: go, unzip, git
check-tools:
	@which go >/dev/null || { echo "go not found. Please download Go 1.22 or newer from https://go.dev/dl/ and follow the installation instructions."; exit 1; }
	@which unzip >/dev/null || { echo "unzip not found. Please install it using 'brew install unzip' on macOS or 'sudo apt-get install -y unzip' on Linux."; exit 1; }
	@which git >/dev/null || { echo "git not found. Please install it using 'brew install git' on macOS or 'sudo apt-get install -y git' on Linux."; exit 1; }
	@which wget >/dev/null || { echo "wget not found. Please install it using 'brew install wget' on macOS or 'sudo apt-get install -y wget' on Linux."; exit 1; }

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
		echo "Checking TensorFlow version..."; \
		current_version=$$(git -C $(HOME)/src/tensorflow describe --tags); \
		if [ "$$current_version" != "$(TFLITE_VERSION)" ]; then \
			echo "Switching TensorFlow source to version $(TFLITE_VERSION)..."; \
			git -C $(HOME)/src/tensorflow fetch --depth 1 origin $(TFLITE_VERSION); \
			git -C $(HOME)/src/tensorflow checkout $(TFLITE_VERSION); \
		else \
			echo "TensorFlow source tree is at version $(TFLITE_VERSION)"; \
		fi; \
	fi

# Function to ensure TensorFlow Lite symlinks are in place
define ensure_tflite_symlinks
	@if [ "$(suffix $(2))" = ".dll" ] && [ ! -f "$(1)/tensorflowlite_c.dll" ]; then \
		echo "Creating symbolic link for Windows DLL..."; \
		echo "Linking $(2) to tensorflowlite_c.dll"; \
		sudo ln -sf "$(1)/tensorflowlite_c-$(patsubst v%,%,$(TFLITE_VERSION)).dll" "$(1)/tensorflowlite_c.dll"; \
	elif [ "$(UNAME_S)" = "Linux" ] && [ ! -f "$(1)/libtensorflowlite_c.so" ]; then \
		echo "Creating symbolic links for Linux library..."; \
		cd $(1) && \
		echo "Linking $(2) to libtensorflowlite_c.so.2"; \
		sudo ln -sf $(2) libtensorflowlite_c.so.2 && \
		echo "Linking libtensorflowlite_c.so.2 to libtensorflowlite_c.so"; \
		sudo ln -sf libtensorflowlite_c.so.2 libtensorflowlite_c.so && \
		sudo ldconfig; \
	elif [ "$(UNAME_S)" = "Darwin" ] && [ ! -f "$(1)/libtensorflowlite_c.dylib" ]; then \
		echo "Creating symbolic links for macOS library..."; \
		cd $(1) && \
		echo "Linking $(2) to libtensorflowlite_c.dylib"; \
		ln -sf $(2) libtensorflowlite_c.dylib; \
	fi
endef

# Update download-tflite target
download-tflite: TFLITE_C_FILE=tflite_c_$(TFLITE_VERSION)_$(TFLITE_LIB_ARCH)
download-tflite:
	@if [ ! -f "$(TFLITE_LIB_DIR)/$(call get_lib_filename,$(TARGET))" ]; then \
		echo "TensorFlow Lite C library not found. Downloading..."; \
		echo "Current TARGET: $(TARGET)"; \
		echo "Current TFLITE_LIB_ARCH: $(TFLITE_LIB_ARCH)"; \
		wget -q https://github.com/tphakala/tflite_c/releases/download/$(TFLITE_VERSION)/$(TFLITE_C_FILE) -P ./; \
		if [ $(suffix $(TFLITE_C_FILE)) = .zip ]; then \
			unzip -o $(TFLITE_C_FILE) -d .; \
			echo "Moving $(call get_lib_filename,$(TARGET)) to $(TFLITE_LIB_DIR)/"; \
			sudo mv $(call get_lib_filename,$(TARGET)) $(TFLITE_LIB_DIR)/; \
			rm -f $(call get_lib_filename,$(TARGET)); \
		else \
			tar -xzf $(TFLITE_C_FILE) -C .; \
			if [ -f "$(TFLITE_LIB_DIR)/libtensorflowlite_c.so" ]; then \
				sudo mv "$(TFLITE_LIB_DIR)/libtensorflowlite_c.so" "$(TFLITE_LIB_DIR)/libtensorflowlite_c.so.old"; \
			fi; \
			echo "Moving $(call get_lib_filename,$(TARGET)) to $(TFLITE_LIB_DIR)/"; \
			sudo mv $(call get_lib_filename,$(TARGET)) $(TFLITE_LIB_DIR)/; \
		fi; \
		rm -f $(TFLITE_C_FILE); \
	else \
		echo "TensorFlow Lite C library already exists."; \
	fi
	$(call ensure_tflite_symlinks,$(TFLITE_LIB_DIR),$(call get_lib_filename,$(TARGET)))

# Download tflite_c library for linux_amd64
download-tflite-linux-amd64:
	wget -q https://github.com/tphakala/tflite_c/releases/download/$(TFLITE_VERSION)/tflite_c_$(TFLITE_VERSION)_linux_amd64.tar.gz -P ./
	tar -xzf tflite_c_$(TFLITE_VERSION)_linux_amd64.tar.gz -C .
	sudo mv libtensorflowlite_c.so.2.17.1 $(TFLITE_LIB_DIR)/libtensorflowlite_c.so
	rm -f tflite_c_$(TFLITE_VERSION)_linux_amd64.tar.gz

# Download assets
download-assets:
	@echo "Downloading latest versions of Leaflet, htmx, Alpine.js, and Tailwind CSS"
	@mkdir -p assets
	@curl -sL https://unpkg.com/leaflet/dist/leaflet.js -o assets/leaflet.js
	@curl -sL https://unpkg.com/leaflet/dist/leaflet.css -o assets/leaflet.css
	@curl -sL https://unpkg.com/htmx.org -o assets/htmx.min.js
	@curl -sL https://unpkg.com/alpinejs -o assets/alpinejs.min.js
	@echo "Assets downloaded successfully"

# Create Tailwind CSS
generate-tailwindcss:
	@echo "Creating Tailwind CSS with DaisyUI"
	npm -D install daisyui
	npx --yes tailwindcss@latest -i $(TAILWIND_INPUT) -o $(TAILWIND_OUTPUT) --minify
	@echo "Tailwind CSS processed successfully"

# Build for Linux amd64
linux_amd64: TFLITE_LIB_ARCH=linux_amd64.tar.gz
linux_amd64: TARGET=linux_amd64
linux_amd64: warn check-tools check-tensorflow
	$(eval TFLITE_LIB_DIR := $(call get_lib_path,$(TARGET)))
	$(eval CGO_FLAGS := $(call get_cgo_flags,$(TARGET)))
	@echo "Building for Linux AMD64 with library path: $(TFLITE_LIB_DIR)"
	@$(MAKE) download-tflite TFLITE_LIB_DIR=$(TFLITE_LIB_DIR) TFLITE_LIB_ARCH=$(TFLITE_LIB_ARCH) TARGET=$(TARGET)
	GOOS=linux GOARCH=amd64 $(CGO_FLAGS) go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)

# Build for Linux arm64
linux_arm64: TFLITE_LIB_ARCH=linux_arm64.tar.gz
linux_arm64: TARGET=linux_arm64
linux_arm64: warn check-tools check-tensorflow
	$(eval TFLITE_LIB_DIR := $(call get_lib_path,$(TARGET)))
	$(eval CGO_FLAGS := $(call get_cgo_flags,$(TARGET)))
	@echo "Building for Linux ARM64 with library path: $(TFLITE_LIB_DIR)"
	@$(MAKE) download-tflite TFLITE_LIB_DIR=$(TFLITE_LIB_DIR) TFLITE_LIB_ARCH=$(TFLITE_LIB_ARCH) TARGET=$(TARGET)
	GOOS=linux GOARCH=arm64 $(CGO_FLAGS) go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)

# Build for Windows amd64
windows_amd64: TFLITE_LIB_ARCH=windows_amd64.zip
windows_amd64: TFLITE_LIB_EXT=.dll
windows_amd64: TARGET=windows_amd64
windows_amd64: warn check-tools check-tensorflow
	$(eval TFLITE_LIB_DIR := $(call get_lib_path,$(TARGET)))
	$(eval CGO_FLAGS := $(call get_cgo_flags,$(TARGET)))
	@echo "Building for Windows AMD64 with library path: $(TFLITE_LIB_DIR)"
	@$(MAKE) download-tflite TFLITE_LIB_DIR=$(TFLITE_LIB_DIR) TFLITE_LIB_ARCH=$(TFLITE_LIB_ARCH) TARGET=$(TARGET)
	GOOS=windows GOARCH=amd64 $(CGO_FLAGS) go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME).exe

# macOS Intel build
darwin_amd64: TFLITE_LIB_ARCH=darwin_amd64.tar.gz
darwin_amd64: TARGET=darwin_amd64
darwin_amd64: warn check-tools check-tensorflow
	@$(MAKE) download-tflite TFLITE_LIB_DIR=$(TFLITE_LIB_DIR) TFLITE_LIB_ARCH=$(TFLITE_LIB_ARCH) TARGET=$(TARGET)
	$(CGO_FLAGS) GOOS=darwin GOARCH=amd64 CGO_LDFLAGS="-L/opt/homebrew/lib -ltensorflowlite_c" go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)

# macOS ARM build
darwin_arm64: TFLITE_LIB_ARCH=darwin_arm64.tar.gz
darwin_arm64: TARGET=darwin_arm64
darwin_arm64: warn check-tools check-tensorflow
	@$(MAKE) download-tflite TFLITE_LIB_DIR=$(TFLITE_LIB_DIR) TFLITE_LIB_ARCH=$(TFLITE_LIB_ARCH) TARGET=$(TARGET)
	$(CGO_FLAGS) GOOS=darwin GOARCH=arm64 CGO_LDFLAGS="-L/opt/homebrew/lib -ltensorflowlite_c" go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)

dev_server: REALTIME_ARGS=""
dev_server: warn
	$(CGO_FLAGS) air realtime $(REALTIME_ARGS)

clean: warn
	go clean
	rm -rf $(BINARY_DIR)/* tflite_c *.tar.gz *.zip
