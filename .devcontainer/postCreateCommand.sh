#!/bin/bash
set -euo pipefail

echo "Setting up BirdNET-Go development environment..."

# Update package list
sudo apt-get update -q

# Install required runtime dependencies
sudo apt-get install -y ca-certificates libasound2 ffmpeg sox alsa-utils

# Install development tools (git is already included)
sudo apt-get install -y nano vim curl wget git dialog build-essential fish socat

# Clone TensorFlow source for compilation (headers needed for CGO)
echo "Setting up TensorFlow source..."
TFLITE_VERSION="v2.17.1"
TENSORFLOW_DIR="/home/dev-user/src/tensorflow"

if [ ! -f "$TENSORFLOW_DIR/tensorflow/lite/c/c_api.h" ]; then
    echo "Cloning TensorFlow $TFLITE_VERSION source (sparse checkout for headers only)..."
    mkdir -p /home/dev-user/src
    
    # Clone with filter to minimize download size
    git clone --branch $TFLITE_VERSION --filter=blob:none --depth 1 https://github.com/tensorflow/tensorflow.git $TENSORFLOW_DIR
    
    # Setup sparse checkout to only get header files
    git -C $TENSORFLOW_DIR config core.sparseCheckout true
    echo "**/*.h" >> $TENSORFLOW_DIR/.git/info/sparse-checkout
    
    # Apply sparse checkout
    git -C $TENSORFLOW_DIR checkout
    
    echo "✓ TensorFlow headers installed at $TENSORFLOW_DIR"
else
    echo "✓ TensorFlow headers already exist at $TENSORFLOW_DIR"
fi

# Ensure correct ownership
sudo chown -R dev-user:dev-user /home/dev-user/src

# Download and install TensorFlow Lite C library
echo "Setting up TensorFlow Lite C library..."
TFLITE_VERSION="v2.17.1"
TFLITE_LIB_DIR="/usr/lib"
ARCH=$(uname -m)

# Determine the correct architecture
if [ "$ARCH" = "x86_64" ]; then
    TFLITE_LIB_ARCH="linux_amd64.tar.gz"
    LIB_FILENAME="libtensorflowlite_c.so.${TFLITE_VERSION#v}"
elif [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
    TFLITE_LIB_ARCH="linux_arm64.tar.gz"
    LIB_FILENAME="libtensorflowlite_c.so.${TFLITE_VERSION#v}"
else
    echo "Warning: Unsupported architecture $ARCH for TensorFlow Lite"
    TFLITE_LIB_ARCH=""
fi

if [ ! -z "$TFLITE_LIB_ARCH" ] && [ ! -f "$TFLITE_LIB_DIR/$LIB_FILENAME" ]; then
    echo "Downloading TensorFlow Lite C library $TFLITE_VERSION for $ARCH..."
    
    # Create temporary extraction directory
    TEMP_EXTRACT_DIR=$(mktemp -d)
    
    # Download the library
    wget -q "https://github.com/tphakala/tflite_c/releases/download/$TFLITE_VERSION/tflite_c_${TFLITE_VERSION}_${TFLITE_LIB_ARCH}" -O /tmp/tflite_c.tar.gz
    
    # Extract the library to temporary directory
    tar -xzf /tmp/tflite_c.tar.gz -C "$TEMP_EXTRACT_DIR"
    
    # Find the actual library file in the extraction directory
    FOUND_LIBS=$(find "$TEMP_EXTRACT_DIR" -name "$LIB_FILENAME" -o -name "libtensorflowlite_c*.so*" | head -10)
    LIB_COUNT=$(echo "$FOUND_LIBS" | wc -l)
    
    if [ -z "$FOUND_LIBS" ] || [ "$LIB_COUNT" -eq 0 ]; then
        echo "Error: No TensorFlow Lite C library found in extracted archive"
        echo "Expected: $LIB_FILENAME or libtensorflowlite_c*.so*"
        echo "Archive contents:"
        find "$TEMP_EXTRACT_DIR" -type f | head -20
        rm -rf "$TEMP_EXTRACT_DIR" /tmp/tflite_c.tar.gz
        exit 1
    elif [ "$LIB_COUNT" -eq 1 ]; then
        ACTUAL_LIB_PATH="$FOUND_LIBS"
    else
        # Multiple matches - prefer exact filename match, otherwise pick first
        EXACT_MATCH=$(echo "$FOUND_LIBS" | grep "$LIB_FILENAME" | head -1)
        if [ ! -z "$EXACT_MATCH" ]; then
            ACTUAL_LIB_PATH="$EXACT_MATCH"
        else
            ACTUAL_LIB_PATH=$(echo "$FOUND_LIBS" | head -1)
        fi
        echo "Warning: Multiple library files found, using: $(basename "$ACTUAL_LIB_PATH")"
    fi
    
    # Get the actual filename from the found library
    ACTUAL_LIB_NAME=$(basename "$ACTUAL_LIB_PATH")
    
    # Move to system library directory
    sudo mv "$ACTUAL_LIB_PATH" "$TFLITE_LIB_DIR/$ACTUAL_LIB_NAME"
    
    # Create symbolic links for the library
    cd $TFLITE_LIB_DIR
    sudo ln -sf "$ACTUAL_LIB_NAME" libtensorflowlite_c.so.2
    sudo ln -sf libtensorflowlite_c.so.2 libtensorflowlite_c.so
    
    # Update library cache
    sudo ldconfig
    
    # Clean up
    rm -rf "$TEMP_EXTRACT_DIR" /tmp/tflite_c.tar.gz
    
    echo "✓ TensorFlow Lite C library installed at $TFLITE_LIB_DIR/$ACTUAL_LIB_NAME"
else
    if [ -f "$TFLITE_LIB_DIR/$LIB_FILENAME" ]; then
        echo "✓ TensorFlow Lite C library already exists at $TFLITE_LIB_DIR"
    fi
fi

# Return to working directory
cd /workspaces/birdnet-go

# Install Go development tools
echo "Installing Go tools..."
go install github.com/air-verse/air@latest
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
go install golang.org/x/tools/gopls@latest
go install github.com/go-delve/delve/cmd/dlv@latest
go install golang.org/x/tools/cmd/goimports@latest

# Install Task runner (used by this project instead of Make)
echo "Installing Task runner..."
sudo sh -c "$(curl --location https://taskfile.dev/install.sh)" -- -d -b /usr/local/bin

# Install Oh My Posh for enhanced shell theming
echo "Installing Oh My Posh..."
sudo wget https://github.com/JanDeDobbeleer/oh-my-posh/releases/latest/download/posh-linux-amd64 -O /usr/local/bin/oh-my-posh
sudo chmod +x /usr/local/bin/oh-my-posh

# Download a theme configuration
sudo mkdir -p /usr/local/share/oh-my-posh/themes
sudo wget https://raw.githubusercontent.com/JanDeDobbeleer/oh-my-posh/main/themes/jandedobbeleer.omp.json -O /usr/local/share/oh-my-posh/themes/jandedobbeleer.omp.json

# Set up Oh My Posh for different shells
echo "Setting up Oh My Posh shell integrations..."

# Add to bashrc
echo 'eval "$(oh-my-posh init bash --config /usr/local/share/oh-my-posh/themes/jandedobbeleer.omp.json)"' >> /home/dev-user/.bashrc

# Add to zshrc (Oh My Zsh will be installed by the feature)
echo 'eval "$(oh-my-posh init zsh --config /usr/local/share/oh-my-posh/themes/jandedobbeleer.omp.json)"' >> /home/dev-user/.zshrc

# Add to fish config
mkdir -p /home/dev-user/.config/fish
echo 'oh-my-posh init fish --config /usr/local/share/oh-my-posh/themes/jandedobbeleer.omp.json | source' >> /home/dev-user/.config/fish/config.fish

# Set ownership
sudo chown -R dev-user:dev-user /home/dev-user/.config

# Setup frontend dependencies
echo "Installing frontend dependencies..."
cd frontend && npm ci && cd ..

# Install global frontend tools that might be needed
echo "Installing global frontend analysis tools..."
# Note: ast-grep is already installed locally via npm ci in frontend/
# The sg command conflicts with system shadow-utils, so we skip global install
echo "ast-grep available via 'npx ast-grep' or 'npm run ast:*' commands"

# Verify Go linter configuration
echo "Verifying Go linting setup..."
if [ -f .golangci.yaml ]; then
    echo "✓ golangci-lint configuration found"
    golangci-lint --version
    # Test configuration (but don't fail on lint errors)
    golangci-lint config path || echo "Warning: golangci-lint config validation failed"
else
    echo "Warning: No .golangci.yaml found"
fi

# Verify frontend linting setup
echo "Verifying frontend linting setup..."
cd frontend

# Check if all required linting tools are available
echo "Checking frontend linting tools..."
npx eslint --version
npx prettier --version
npx stylelint --version
npx svelte-check --version
npx ast-grep --version || echo "Warning: ast-grep not available"

# Verify TypeScript configuration
if [ -f tsconfig.json ]; then
    echo "✓ TypeScript configuration found"
    npx tsc --version
else
    echo "Warning: No tsconfig.json found in frontend"
fi

# Test frontend linting (but don't fail on errors)
echo "Testing frontend linters..."
npm run format:check || echo "Note: Format check found issues (will be fixed during development)"
npm run typecheck || echo "Note: TypeScript check found issues (normal for initial setup)"

cd ..

# Build frontend for immediate use
echo "Building frontend for development..."
task frontend-build || echo "Warning: Frontend build failed, but you can rebuild with: task frontend-build"

# Verify installations
echo ""
echo "=== Installation Summary ==="
echo "Go version: $(go version)"
echo "Node.js version: $(node --version)"
echo "npm version: $(npm --version)"
echo "Task version: $(task --version)"
echo "golangci-lint version: $(golangci-lint --version)"
echo "Oh My Posh version: $(oh-my-posh version)"

# Verify TensorFlow setup
echo ""
echo "=== TensorFlow Setup ==="
if [ -f "/home/dev-user/src/tensorflow/tensorflow/lite/c/c_api.h" ]; then
    echo "✓ TensorFlow headers: Available"
else
    echo "✗ TensorFlow headers: Missing"
fi

if [ -f "/usr/lib/libtensorflowlite_c.so" ]; then
    echo "✓ TensorFlow Lite C library: Available"
    ls -la /usr/lib/libtensorflowlite_c.so* | head -3
else
    echo "✗ TensorFlow Lite C library: Missing"
fi

# Display available linting commands
echo ""
echo "=== Available Linting Commands ==="
echo "Go linting:"
echo "  - golangci-lint run        (comprehensive Go linting)"
echo "  - go vet ./...             (basic Go static analysis)"
echo ""
echo "Frontend linting:"
echo "  - task frontend-quality    (comprehensive frontend quality checks)"
echo "  - task frontend-lint       (ESLint + Prettier + Stylelint)"
echo "  - task frontend-test       (run frontend tests)"
echo "  - npm run typecheck        (TypeScript check only)"
echo "  - npm run ast:all          (AST-grep security/pattern checks)"
echo "  - npx ast-grep scan        (manual AST-grep usage)"
echo ""
echo "Pre-commit checks:"
echo "  - golangci-lint run        (before Go commits)"
echo "  - task frontend-quality    (before frontend commits)"

echo ""
echo "=== Available Development Commands ==="
echo "Development servers:"
echo "  - air realtime             (start Go server - frontend already built!)"
echo "  - task dev_server          (same as 'air realtime')"
echo ""
echo "Frontend development (optional):"
echo "  - task frontend-watch      (watch mode: auto-rebuilds frontend on changes)"
echo "  - task frontend-build      (one-time rebuild if needed)"
echo "  - task frontend-lint-fix   (auto-fix frontend linting issues)"
echo "  - task frontend-test       (run frontend tests)"
echo "  - task frontend-quality    (run comprehensive frontend quality checks)"
echo ""
echo "Linting:"
echo "  - golangci-lint run        (comprehensive Go linting)"
echo "  - task frontend-lint       (frontend ESLint + Prettier + Stylelint)"
echo "  - npm run ast:security     (AST-grep security scanning)"
echo ""  
echo "Build commands:"
echo "  - task                     (default build - compiles Go with embedded frontend)"
echo "  - task linux_amd64         (cross-platform Linux AMD64 build)"
echo ""
echo "Available shells (all with Oh My Posh theming):"
echo "  - bash (default)    - zsh (with Oh My Zsh)    - fish    - pwsh (PowerShell)"
echo "Switch shells: click terminal dropdown or use 'Ctrl+Shift+\`' in VS Code"
echo ""
echo "💡 Use 'Ctrl+Shift+P' and search for 'Go: Install/Update Tools' in VS Code for additional Go tools"
echo ""
echo "✅ Development environment setup complete!"
echo ""
echo "Quick Start:"
echo "   1. Open a terminal: Ctrl+Shift+\` or Terminal → New Terminal"
echo "   2. Start server: air realtime  (or: task dev_server)"
echo "   3. Open browser: http://localhost:8080"
echo ""
echo "For active frontend development, use 'task frontend-watch' in a separate terminal"
