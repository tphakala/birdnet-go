#!/bin/bash
set -euxo pipefail

echo "Setting up BirdNET-Go development environment..."

# Update package list
sudo apt-get update -q

# Install required runtime dependencies
sudo apt-get install -y ca-certificates libasound2 ffmpeg sox alsa-utils

# Install development tools (git is already included)
sudo apt-get install -y nano vim curl wget git dialog build-essential fish

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
sudo npm install -g @ast-grep/cli

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
sg --version || echo "Warning: ast-grep (sg) not available globally"

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

# Verify installations
echo ""
echo "=== Installation Summary ==="
echo "Go version: $(go version)"
echo "Node.js version: $(node --version)"
echo "npm version: $(npm --version)"
echo "Task version: $(task --version)"
echo "golangci-lint version: $(golangci-lint --version)"
echo "Oh My Posh version: $(oh-my-posh version)"

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
echo ""
echo "Pre-commit checks:"
echo "  - golangci-lint run        (before Go commits)"
echo "  - task frontend-quality    (before frontend commits)"

echo ""
echo "✅ Development environment setup complete!"
echo ""
echo "=== Available Development Commands ==="
echo "Development servers:"
echo "  - task dev_server          (full development server with frontend build + live reload)"
echo "  - air realtime             (Go live reload server with realtime analysis)"
echo ""
echo "Frontend development:"
echo "  - task frontend-build      (build Svelte to static files for embedding)"
echo "  - task frontend-lint-fix   (auto-fix frontend linting issues)"
echo "  - task frontend-test       (run frontend tests)"
echo "  - task frontend-quality    (run comprehensive frontend quality checks)"
echo ""
echo "Linting:"
echo "  - golangci-lint run        (comprehensive Go linting)"
echo "  - task frontend-lint       (frontend ESLint + Prettier + Stylelint)"
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
