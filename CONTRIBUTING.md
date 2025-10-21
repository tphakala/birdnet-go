# Contributing to BirdNET-Go

Thank you for your interest in contributing to BirdNET-Go! This document provides guidelines and instructions for setting up your development environment and contributing to the project.

## Table of Contents

- [Code of Conduct](#code-of-conduct)
- [Getting Started](#getting-started)
- [Development Environment Setup](#development-environment-setup)
  - [Option 1: Automated Setup with Taskfile (Recommended)](#option-1-automated-setup-with-taskfile-recommended)
  - [Option 2: Dev Container (VS Code)](#option-2-dev-container-vs-code)
- [Development Workflow](#development-workflow)
  - [Hot Reload Development](#hot-reload-development)
  - [Frontend Development](#frontend-development)
  - [Building the Project](#building-the-project)
- [Code Quality and Linting](#code-quality-and-linting)
- [Testing](#testing)
- [Project Structure](#project-structure)
- [Submitting Changes](#submitting-changes)
- [Additional Resources](#additional-resources)

## Code of Conduct

This project is committed to providing a welcoming and inclusive environment for all contributors. Please be respectful and constructive in all interactions.

## Getting Started

Before contributing, please:

1. Check [existing issues](https://github.com/tphakala/birdnet-go/issues) to avoid duplicates
2. Join our [Discord server](https://discord.gg/gcSCFGUtsd) for discussions and support
3. Read the relevant CLAUDE.md files for development guidelines:
   - [`CLAUDE.md`](CLAUDE.md) - Project overview and universal rules
   - [`internal/CLAUDE.md`](internal/CLAUDE.md) - Go backend guidelines
   - [`frontend/CLAUDE.md`](frontend/CLAUDE.md) - Svelte 5 frontend guidelines
   - [`internal/api/v2/CLAUDE.md`](internal/api/v2/CLAUDE.md) - API v2 guidelines

## Development Environment Setup

BirdNET-Go requires:

- **Go** 1.25.3 or later
- **Node.js** 22.x or later (LTS)
- **Build tools** (gcc, git, wget, etc.)
- **TensorFlow Lite** C library (auto-downloaded on first build, or installed by dev container)

### Option 1: Automated Setup with Taskfile (Recommended)

This is the fastest way to get started on **Linux (apt-based)** or **macOS (with Homebrew)**.

#### Step 1: Install Task Runner

⚠️ **Security Note:** The installation scripts below download and execute code from the internet. Review the scripts before running if security is a concern:

- Task install script: https://taskfile.dev/install.sh
- Homebrew install script: https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh

**Linux:**

```bash
# Install Task (required for automated setup)
# Note: The setup-dev task will also install Task if not present, but you need Task to run it
sh -c "$(curl --location https://taskfile.dev/install.sh)" -- -d -b /usr/local/bin
```

**macOS:**

```bash
# Install Homebrew if not already installed
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

# Install Task
brew install go-task
```

**Alternative:** If you already have Go installed, you can install Task via Go:

```bash
go install github.com/go-task/task/v3/cmd/task@latest
# Add $HOME/go/bin to your PATH if not already done
```

#### Step 2: Clone and Setup

```bash
# Clone the repository
git clone https://github.com/tphakala/birdnet-go.git
cd birdnet-go

# Run automated setup (installs all dependencies)
task setup-dev
```

The `setup-dev` task will automatically:

- ✅ Detect your operating system
- ✅ Install Go 1.25.3
- ✅ Install Node.js LTS
- ✅ Install build tools (gcc, git, wget, curl, etc.)
- ✅ Install ARM64 cross-compilation tools (Linux only)
- ✅ Install golangci-lint (Go linter)
- ✅ Install air (hot reload for Go)
- ✅ Install mockgen (testing tool)
- ✅ Install frontend dependencies (npm packages)
- ✅ Install Playwright browsers for E2E testing
- ✅ Verify all installations

**Note:** If Go is installed for the first time, you may need to run:

```bash
source ~/.profile  # or restart your shell
```

#### Step 3: Build and Run

```bash
# Build the project
task

# Start development server with hot reload
task dev_server

# Or use air directly for realtime mode
air realtime
```

### Option 2: Dev Container (VS Code)

For a consistent, containerized development environment with all tools pre-configured.

#### Prerequisites

- [Docker](https://docs.docker.com/get-docker/)
- [Visual Studio Code](https://code.visualstudio.com/)
- [Dev Containers extension](https://marketplace.visualstudio.com/items?itemName=ms-vscode-remote.remote-containers)

#### Setup Steps

1. **Clone the repository:**

   ```bash
   git clone https://github.com/tphakala/birdnet-go.git
   cd birdnet-go
   ```

2. **Open in VS Code:**

   ```bash
   code .
   ```

3. **Reopen in Container:**
   - Press `F1` or `Ctrl+Shift+P`
   - Select "Dev Containers: Reopen in Container"
   - Wait for the container to build and setup to complete (~5-10 minutes on first run)

4. **Start developing:**
   The container automatically runs [`.devcontainer/postCreateCommand.sh`](.devcontainer/postCreateCommand.sh) which:
   - Downloads and configures TensorFlow Lite headers and C library
   - Installs all Go development tools (air, golangci-lint, gopls, dlv)
   - Installs frontend dependencies
   - Sets up Oh My Posh for enhanced terminal experience
   - Verifies all installations

#### Dev Container Features

The dev container includes:

- **Pre-configured VS Code extensions:**
  - Go (golang.go)
  - Svelte (svelte.svelte-vscode)
  - TypeScript, ESLint, Prettier, Stylelint
  - Playwright for E2E testing
  - TailwindCSS IntelliSense
- **Multiple shell options:** bash, zsh (with Oh My Zsh), fish, PowerShell
- **Port forwarding:** Port 8080 (BirdNET-Go server)
- **Automatic linting and formatting:** Configured to format on save

## Development Workflow

### Hot Reload Development

BirdNET-Go uses [air](https://github.com/air-verse/air) for hot reloading during development. The configuration is defined in [`.air.toml`](.air.toml).

#### Using Air

```bash
# Standard development mode
air

# Realtime analysis mode
air realtime
```

**What Air Does:**

1. Watches for changes in:
   - Go source files (`*.go`)
   - Frontend files (`*.svelte`, `*.ts`, `*.js`, `*.css`)
   - Configuration files (`tailwind.config.js`, `vite.config.js`, etc.)
2. Automatically rebuilds frontend (Svelte + Tailwind CSS)
3. Recompiles Go binary with embedded frontend
4. Restarts the server automatically

**Important:** The frontend is **embedded** in the Go binary during build. Air handles the full rebuild pipeline automatically.

#### Using Task for Development

```bash
# Full development server (recommended)
task dev_server

# Frontend-only rebuild
task frontend-build

# Frontend development mode (use with caution - see warning below)
task frontend-dev
```

### Frontend Development

⚠️ **Important:** The frontend is **embedded into Go binaries** at build time. Do **NOT** use the Vite dev server (`npm run dev` or `task frontend-dev`) for normal development, as changes won't be reflected in the Go server.

**Why Frontend is Embedded:**

- BirdNET-Go serves the frontend as static files embedded in the Go binary
- This ensures single-binary deployment without external frontend dependencies
- Air automatically rebuilds and embeds the frontend on every change

**Frontend Build Process:**

1. Svelte 5 components are compiled to static files
2. Vite bundles JavaScript, CSS, and assets
3. Go's `embed` directive embeds the `frontend/dist` directory
4. Result: Self-contained binary with embedded UI

**When to Use Vite Dev Server:**

Only use `task frontend-dev` when:

- Doing rapid UI prototyping with hot module replacement
- Testing Svelte component behavior in isolation
- Running frontend tests

For all other development, use `air` or `task dev_server` which handles the full build pipeline.

### Building the Project

```bash
# Default build (auto-detects platform)
task

# Build for specific platforms
task linux_amd64
task linux_arm64
task darwin_amd64
task darwin_arm64
task windows_amd64

# Build without embedded models (smaller binary)
task noembed

# Clean build artifacts
task clean
```

## Code Quality and Linting

**All code must pass linting before being committed.**

### Go Linting

Configuration: [`.golangci.yaml`](.golangci.yaml)

```bash
# Run all Go linters
golangci-lint run -v

# Run on specific files
golangci-lint run -v internal/api/v2/media.go

# Run on entire v2 API
golangci-lint run -v ./internal/api/v2/...

# Auto-fix issues where possible
golangci-lint run --fix
```

**Pre-commit requirement:** All Go code must pass `golangci-lint run -v` with zero errors.

### Frontend Linting

Configuration files:

- ESLint: `frontend/eslint.config.js`
- Prettier: `frontend/.prettierrc`
- Stylelint: `frontend/.stylelintrc.json`
- TypeScript: `frontend/tsconfig.json`

```bash
# Comprehensive quality check (recommended)
task frontend-quality

# Individual checks
task frontend-lint           # ESLint + Prettier + Stylelint
task frontend-typecheck      # TypeScript type checking
npm run ast:all              # AST-grep security/pattern checks

# Auto-fix issues
task frontend-lint-fix       # Fix ESLint, Prettier, Stylelint issues
npm run check:fix            # Format + lint auto-fix
```

**Pre-commit requirement:** All frontend code must pass `npm run check:all` with zero errors.

### AST-Grep Code Analysis

BirdNET-Go uses [ast-grep](https://ast-grep.github.io/) for advanced code pattern matching and security analysis.

```bash
# Run all AST-grep checks
cd frontend
npm run ast:all

# Individual checks
npm run ast:security         # Security pattern scanning
npm run ast:svelte5          # Svelte 5 best practices
npm run ast:migration        # Svelte 4 → 5 migration checks
npm run ast:best-practices   # General best practices

# Auto-fix issues
npm run ast:fix
```

See [`frontend/doc/AST-GREP-SETUP.md`](frontend/doc/AST-GREP-SETUP.md) for complete ast-grep usage guide.

## Testing

### Go Tests

```bash
# Run all tests
task test

# Run with verbose output
task test-verbose

# Run with coverage
task test-coverage

# Run with race detector
go test -race -v ./...

# Run specific package tests
go test -v ./internal/api/v2/...
```

### Frontend Tests

```bash
# Unit tests
task frontend-test

# Unit tests with coverage
task frontend-test-coverage

# E2E tests with Playwright
task e2e-test

# E2E tests in headed mode (for debugging)
task e2e-test-headed

# E2E tests with UI mode
task e2e-test-ui

# Open E2E test report
task e2e-report
```

## Project Structure

```
birdnet-go/
├── cmd/                    # Viper CLI commands
├── internal/               # Private Go packages
│   ├── api/
│   │   └── v2/            # API v2 endpoints (all new APIs go here)
│   ├── analysis/          # Audio analysis logic
│   ├── conf/              # Configuration management
│   └── httpcontroller/    # HTTP handlers
├── pkg/                    # Public Go packages
├── frontend/               # Svelte 5 UI (embedded at build time)
│   ├── src/
│   │   ├── lib/           # Reusable components and utilities
│   │   └── routes/        # SvelteKit routes
│   ├── dist/              # Built frontend (embedded by Go)
│   └── doc/               # Frontend documentation
├── assets/                 # Static assets
├── Taskfile.yml           # Task runner configuration
├── .air.toml              # Air hot reload configuration
├── .golangci.yaml         # Go linting configuration
└── .devcontainer/         # Dev container configuration
```

**Key Files:**

- [`Taskfile.yml`](Taskfile.yml) - Build and development tasks
- [`.air.toml`](.air.toml) - Hot reload configuration
- [`.golangci.yaml`](.golangci.yaml) - Go linting rules
- [`CLAUDE.md`](CLAUDE.md) - Project development guidelines

## Submitting Changes

### Commit Guidelines

1. **Branch Naming:**

   ```bash
   # Feature branches
   git checkout -b feature/short-description

   # Bug fixes
   git checkout -b fix/issue-number-description

   # Documentation
   git checkout -b docs/what-you-updated
   ```

2. **Before Committing:**

   ```bash
   # Update from main
   git pull origin main
   git checkout -b feature/my-feature

   # Run linters
   golangci-lint run -v              # Go code
   task frontend-quality             # Frontend code

   # Run tests
   task test                         # Go tests
   task frontend-test                # Frontend tests
   ```

3. **Commit Messages:**

   Follow conventional commits format:

   ```
   type(scope): brief description

   Longer explanation if needed.

   Fixes #123
   ```

   **Types:** `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `perf`

   **Examples:**

   ```
   feat(api): add spectrogram generation endpoint
   fix(frontend): resolve audio player freeze issue (BG-14)
   docs(contributing): add air hot reload documentation
   refactor(analysis): optimize detection pipeline
   test(api): add integration tests for v2 endpoints
   ```

4. **Creating Commits:**
   - Make atomic commits (one logical change per commit)
   - Write clear, descriptive commit messages
   - Reference issue numbers when applicable

### Pull Request Process

1. **Open a Pull Request:**
   - Provide clear description of changes
   - Link to related issues
   - Include screenshots for UI changes
   - Ensure CI checks pass

2. **PR Title Format:**

   ```
   type(scope): Brief description of changes
   ```

3. **PR Description Template:**

   ```markdown
   ## Summary

   Brief description of what this PR does.

   ## Changes

   - Change 1
   - Change 2
   - Change 3

   ## Testing

   - [ ] Go tests pass (`task test`)
   - [ ] Frontend tests pass (`task frontend-test`)
   - [ ] Linting passes (Go + Frontend)
   - [ ] Manual testing completed

   ## Related Issues

   Fixes #123
   Relates to #456

   ## Screenshots (if applicable)

   [Add screenshots here]
   ```

4. **Review Process:**
   - Address reviewer feedback promptly
   - Keep discussions respectful and constructive
   - Squash commits if requested

### Critical Constraints

- **NEVER expand API v1** - All new endpoints must go in `internal/api/v2/`
- **Always lint before commit** - Use `golangci-lint run -v` (Go) and `npm run check:all` (Frontend)
- **Branch from updated main** - Always `git pull origin main` before creating feature branches
- **No `any` types in TypeScript** - All parameters must be properly typed

## Additional Resources

### Documentation

- [User Guide](doc/wiki/guide.md) - Complete usage documentation
- [Installation Guide](doc/wiki/installation.md) - Detailed installation instructions
- [Security Guide](doc/wiki/security.md) - Security best practices
- [Hardware Recommendations](doc/wiki/hardware.md) - Optimal hardware setup
- [AST-Grep Setup](frontend/doc/AST-GREP-SETUP.md) - Advanced code analysis guide

### Development Guidelines

- [Go Backend Guidelines](internal/CLAUDE.md)
- [Svelte 5 Frontend Guidelines](frontend/CLAUDE.md)
- [API v2 Guidelines](internal/api/v2/CLAUDE.md)

### Project Resources

- [GitHub Repository](https://github.com/tphakala/birdnet-go)
- [Discord Community](https://discord.gg/gcSCFGUtsd)
- [Issue Tracker](https://github.com/tphakala/birdnet-go/issues)
- [BirdNET Upstream](https://github.com/birdnet-team/BirdNET-Analyzer) - Original BirdNET project

### Useful Commands Reference

```bash
# Development
task setup-dev              # Setup development environment
air                         # Start hot reload server
air realtime                # Start with realtime analysis
task dev_server             # Full development server

# Building
task                        # Default build
task clean                  # Clean artifacts
task linux_amd64            # Cross-platform build

# Quality Checks
golangci-lint run -v        # Go linting
task frontend-quality       # Frontend quality checks
npm run ast:all             # AST-grep analysis

# Testing
task test                   # Go tests
task frontend-test          # Frontend tests
task e2e-test               # E2E tests

# Utilities
task --list                 # List all available tasks
task format-md              # Format markdown files
```

---

**Thank you for contributing to BirdNET-Go!** 🎉

If you have questions or need help, please:

- Join our [Discord server](https://discord.gg/gcSCFGUtsd)
- Open a [GitHub discussion](https://github.com/tphakala/birdnet-go/discussions)
- Check existing [issues](https://github.com/tphakala/birdnet-go/issues)
