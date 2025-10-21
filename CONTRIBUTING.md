# Contributing to BirdNET-Go

Thank you for your interest in contributing to BirdNET-Go! This document provides guidelines and instructions for setting up your development environment and contributing to the project.

## TL;DR - Quick Start (For Experienced Developers)

> "Trust me, I know what I'm doing."

**Legal:** By contributing, you agree to [CC BY-NC-SA 4.0 license](#project-license) and [privacy-by-design principles](PRIVACY.md).

**Setup (5 minutes):**

```bash
# Clone and setup everything
git clone https://github.com/tphakala/birdnet-go.git && cd birdnet-go

# Install Task (modern replacement for make)
sh -c "$(curl --location https://taskfile.dev/install.sh)" -- -d -b /usr/local/bin

# Setup development tools (Go, Node.js, tools, git hooks)
task setup-dev

source ~/.profile  # If Go was just installed

# Develop with hot reload
air realtime  # or 'task dev_server'

# Before commit (auto-runs via git hooks anyway)
# Lint Go code
golangci-lint run

# Lint frontend code
task frontend-quality

# Commit (pre-commit hook auto-formats & lints)
git checkout -b feature/my-feature
git commit -m "feat(scope): what you did"
```

**Key Rules:**

- ❌ No API v1 expansion → use `internal/api/v2/`
- ❌ No telemetry without explicit user opt-in
- ❌ No `any` types in TypeScript
- ✅ Frontend is embedded in Go binary (use `air`, not Vite dev server)
- ✅ Pre-commit hooks auto-format & lint (can't commit broken code)
- ✅ AI-assisted coding encouraged (Claude Code, CodeRabbit) - use responsibly

**Need details?** Read the full guide below. **Questions?** [Discord](https://discord.gg/gcSCFGUtsd)

---

## Table of Contents

- [Code of Conduct](#code-of-conduct)
- [License and Legal](#license-and-legal)
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
- [AI-Assisted Development](#ai-assisted-development)
- [Project Structure](#project-structure)
- [Submitting Changes](#submitting-changes)
- [Additional Resources](#additional-resources)

## Code of Conduct

This project is committed to providing a welcoming and inclusive environment for all contributors. Please be respectful and constructive in all interactions.

## License and Legal

### Project License

**IMPORTANT:** By contributing to BirdNET-Go, you agree that your contributions will be licensed under the same license as the project.

BirdNET-Go is licensed under the **[Creative Commons Attribution-NonCommercial-ShareAlike 4.0 International License (CC BY-NC-SA 4.0)](LICENSE)**.

**Key License Terms:**

- ✅ **Attribution** - You must give appropriate credit and indicate if changes were made
- ✅ **ShareAlike** - If you remix, transform, or build upon the material, you must distribute your contributions under the same license
- ❌ **NonCommercial** - You may not use the material for commercial purposes without explicit permission
- ✅ **No Additional Restrictions** - You may not apply legal terms that legally restrict others from doing anything the license permits

**What This Means for Contributors:**

1. **Your Contributions**: All code, documentation, and other contributions you submit become part of the project under CC BY-NC-SA 4.0
2. **Attribution**: You will be credited as a contributor (via git history and contributor lists)
3. **No Warranty**: Contributions are provided "as is" without warranty of any kind
4. **Commercial Use**: Contact project maintainers if you wish to use BirdNET-Go commercially

**📄 Full License:** See [LICENSE](LICENSE) file in the repository root.

### Privacy Policy

**IMPORTANT:** Contributors must read and understand the project's privacy practices.

BirdNET-Go is a **privacy-first, open-source project** that:

- 🔒 **Collects ZERO data by default** - All telemetry is completely opt-in
- 🏠 **Operates locally** - No external connections except weather/images (read-only)
- 👤 **Respects user privacy** - Privacy-by-design principles throughout
- 📋 **Full transparency** - Complete disclosure of data practices

**Key Privacy Principles:**

1. **Explicit Consent** - All telemetry requires user opt-in
2. **Data Minimization** - Only essential technical data for debugging
3. **Anonymization** - URLs, credentials, and paths are automatically anonymized
4. **User Control** - Users can enable/disable telemetry anytime

**For Contributors:**

- Follow privacy-by-design when developing features
- Never add telemetry or external connections without explicit user opt-in
- Ensure sensitive data (URLs, credentials, paths) is anonymized in logs/errors
- Document any external services or data transmission in privacy policy

**📄 Full Privacy Policy:** Read [PRIVACY.md](PRIVACY.md) for complete details on data handling.

**⚠️ Volunteer Project Notice:** BirdNET-Go is maintained by volunteers in their spare time. While we implement strong privacy protections by design, response times for privacy requests may vary based on volunteer availability.

### Contributor License Agreement

By submitting a pull request, you certify that:

1. ✅ You have the right to submit the contribution under the project's license
2. ✅ Your contribution is your original work or you have permission to submit it
3. ✅ You agree to license your contribution under CC BY-NC-SA 4.0
4. ✅ You have read and understand the project's [privacy policy](PRIVACY.md)
5. ✅ Your contribution follows privacy-by-design principles

**No separate CLA required** - Your submission of a pull request constitutes your agreement to these terms.

## Getting Started

Before contributing, please:

1. **Read the [License](#project-license)** - Understand the CC BY-NC-SA 4.0 license terms
2. **Review the [Privacy Policy](PRIVACY.md)** - Understand privacy-by-design requirements
3. Check [existing issues](https://github.com/tphakala/birdnet-go/issues) to avoid duplicates
4. Join our [Discord server](https://discord.gg/gcSCFGUtsd) for discussions and support
5. Read the relevant CLAUDE.md files for development guidelines:
   - [`CLAUDE.md`](CLAUDE.md) - Project overview and universal rules
   - [`internal/CLAUDE.md`](internal/CLAUDE.md) - Go backend guidelines
   - [`frontend/CLAUDE.md`](frontend/CLAUDE.md) - Svelte 5 frontend guidelines
   - [`internal/api/v2/CLAUDE.md`](internal/api/v2/CLAUDE.md) - API v2 guidelines

   **Note:** These CLAUDE.md files are optimized for AI-assisted coding tools (particularly Claude Code), but serve as comprehensive project guidelines for all contributors, whether using AI assistance or coding manually.

## Development Environment Setup

BirdNET-Go requires:

- **Go** 1.25 or later
- **Node.js** 22.x or later (LTS)
- **Build tools** (gcc, git, wget, etc.)
- **TensorFlow Lite** C library (auto-downloaded on first build, or installed by dev container)

### Option 1: Automated Setup with Taskfile (Recommended)

This is the fastest way to get started on **Linux (apt-based)** or **macOS (with Homebrew)**.

#### Step 1: Install Task Runner

⚠️ **Security Note:** The installation scripts below download and execute code from the internet. Review the scripts before running if security is a concern:

- [Task install script](https://taskfile.dev/install.sh)
- [Homebrew install script](https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)

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
- ✅ Install frontend dependencies (npm packages)
- ✅ Install Playwright browsers for E2E testing
- ✅ Initialize git hooks (Husky pre-commit checks)
- ✅ Verify all installations

**Git Hooks (Pre-commit Checks):**

The setup automatically configures git pre-commit hooks using [Husky](https://typicode.github.io/husky/) that will:

- **Go files:** Auto-format with `gofmt` and lint with `golangci-lint`
- **Frontend files:** Auto-format with Prettier, lint with ESLint and Stylelint
- **TypeScript:** Run type checking on staged files

These hooks **prevent commits with linting or type errors**, ensuring code quality. The hooks run automatically before each commit.

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
# Run BirdNET-Go in realtime analysis mode with Air
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
task noembed (used with Docker builds)

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

### Git Hooks (Pre-commit Automation)

Git hooks are automatically configured during `task setup-dev` using [Husky](https://typicode.github.io/husky/). The pre-commit hook (`.husky/pre-commit`) runs before every commit to ensure code quality.

**What the pre-commit hook does:**

1. **Go files:**
   - Auto-formats with `gofmt` and re-stages formatted files
   - Runs `golangci-lint run -v` on all staged Go files
   - Blocks commit if linting fails
2. **Frontend files:**
   - Runs `lint-staged` (Prettier + ESLint + Stylelint) on staged files
   - Runs TypeScript type checking on staged `.ts` and `.svelte` files
   - Blocks commit if any checks fail

**Manual initialization (if needed):**

```bash
# Initialize Husky git hooks
cd frontend
npm run prepare

# Verify hook is installed
ls -la ../.git/hooks/pre-commit
```

**Bypassing hooks (not recommended):**

```bash
# Skip pre-commit checks (strongly discouraged)
git commit --no-verify -m "message"
```

⚠️ **Note:** The `--no-verify` flag is **strongly discouraged** and bypassed commits may be rejected during code review. Fix linting issues instead of bypassing checks.

**Troubleshooting:**

```bash
# If hooks aren't running, reinitialize
cd frontend && npm run prepare

# If golangci-lint not found in hook
which golangci-lint  # Ensure it's in PATH
echo $PATH           # Check if $HOME/go/bin is in PATH
```

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

## AI-Assisted Development

BirdNET-Go **welcomes and encourages** the use of AI-assisted coding tools. The project has been successfully developed using AI assistance, and we believe these tools can help contributors write better code faster.

### AI Tools Used in This Project

**Claude Code (Primary Development Tool)**

The main developer uses [Claude Code](https://claude.ai/claude-code) with the Sonnet 4.5 model for most pull requests. Claude Code has been instrumental in:

- Writing new features with comprehensive test coverage
- Refactoring complex code while maintaining correctness
- Identifying and fixing bugs
- Generating documentation and code comments
- Ensuring consistency with project guidelines

**CodeRabbit (Automated PR Reviews)**

All pull requests receive automated code review from [CodeRabbit AI](https://coderabbit.ai/), which:

- Performs comprehensive code analysis
- Identifies potential bugs and security issues
- Suggests improvements and best practices
- Checks for code quality and consistency
- Provides actionable feedback for contributors

### CLAUDE.md Guidelines

The project includes detailed `CLAUDE.md` files that serve a dual purpose:

1. **AI-Optimized Instructions**: Structured guidelines for AI coding assistants (especially Claude Code)
2. **Human-Readable Guidelines**: Comprehensive project standards for manual development

These files are located at:

- [`CLAUDE.md`](CLAUDE.md) - Project overview and universal rules
- [`internal/CLAUDE.md`](internal/CLAUDE.md) - Go backend development standards
- [`frontend/CLAUDE.md`](frontend/CLAUDE.md) - Svelte 5 frontend development patterns
- [`internal/api/v2/CLAUDE.md`](internal/api/v2/CLAUDE.md) - API v2 endpoint guidelines

**Whether you use AI assistance or code manually**, these files contain valuable project-specific patterns, conventions, and best practices that all contributors should follow.

### Responsible AI Usage

While AI tools are encouraged, please use them responsibly:

**✅ Good AI Usage:**

- Use AI to understand codebase patterns and architecture
- Generate boilerplate code and tests
- Refactor code while maintaining existing behavior
- Write documentation and comments
- Identify potential bugs and edge cases
- Learn new programming concepts and best practices

**⚠️ Important Considerations:**

- **Review all AI-generated code** - Don't blindly accept suggestions
- **Understand what the code does** - You're responsible for your contributions
- **Test thoroughly** - AI-generated code must pass all linting and testing requirements
- **Follow project guidelines** - AI outputs must conform to CLAUDE.md standards
- **Respect privacy** - Never share private/sensitive repository data with AI tools
- **Maintain code quality** - AI assistance doesn't exempt you from quality standards

**❌ Prohibited AI Usage:**

- Submitting AI-generated code without understanding it
- Using AI to bypass code quality checks or testing requirements
- Sharing proprietary or sensitive data with external AI services
- Copying code from AI suggestions without verifying licensing/attribution
- Using AI to generate misleading or obfuscated code

### AI-Assisted Development Workflow

1. **Setup**: Configure your AI tool to use the project's CLAUDE.md files as context
2. **Development**: Use AI assistance for coding, refactoring, and documentation
3. **Review**: Carefully review all AI-generated code changes
4. **Testing**: Ensure all tests pass (`task test`, `task frontend-test`)
5. **Linting**: Verify code quality (`golangci-lint run -v`, `npm run check:all`)
6. **Commit**: Use standard git workflow (pre-commit hooks will validate)
7. **Pull Request**: Submit PR for CodeRabbit AI review and human review

### Getting Started with Claude Code

If you want to use Claude Code for contributing to BirdNET-Go:

1. **Install**: Follow [Claude Code installation guide](https://docs.claude.com/en/docs/claude-code)
2. **Open Project**: Open the BirdNET-Go repository in your editor
3. **Read Guidelines**: The CLAUDE.md files will automatically provide context to Claude
4. **Start Coding**: Ask Claude to help with specific tasks, referencing the guidelines
5. **Iterate**: Review and refine AI suggestions based on project requirements

### Questions About AI Usage?

Join our [Discord server](https://discord.gg/gcSCFGUtsd) to discuss AI-assisted development practices and share experiences with other contributors.

## Project Structure

```text
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

   ```text
   type(scope): brief description

   Longer explanation if needed.

   Fixes #123
   ```

   **Types:** `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `perf`

   **Examples:**

   ```text
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

   ```text
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
   - **CodeRabbit AI** will automatically review your PR and provide feedback
   - Address both AI and human reviewer feedback promptly
   - Keep discussions respectful and constructive
   - Squash commits if requested
   - See [AI-Assisted Development](#ai-assisted-development) for details on automated reviews

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
