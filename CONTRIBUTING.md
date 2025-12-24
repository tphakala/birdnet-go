# Contributing to BirdNET-Go

Thank you for your interest in contributing to BirdNET-Go!

## TL;DR - Quick Start

**Legal:** By contributing, you agree to [CC BY-NC-SA 4.0 license](#license-and-legal) and [privacy-by-design principles](PRIVACY.md).

**Automated Setup (Linux/macOS):**

```bash
git clone https://github.com/tphakala/birdnet-go.git && cd birdnet-go
# install Task
sh -c "$(curl --location https://taskfile.dev/install.sh)" -- -d -b /usr/local/bin
# install development tools (compilers, linters, etc)
task setup-dev
# reload profile
source ~/.profile  # If Go was just installed
# start hot reloading dev server with
air realtime
```

**Dev Container (All Platforms):**

```bash
git clone https://github.com/tphakala/birdnet-go.git && cd birdnet-go
code .
# Press F1 → "Dev Containers: Reopen in Container"
air realtime
```

**Key Rules:**

- ❌ No API v1 expansion → use `internal/api/v2/`
- ❌ No telemetry without explicit user opt-in
- ❌ No `any` types in TypeScript
- ✅ Frontend embedded in Go binary (use `air`, not Vite dev server)
- ✅ Pre-commit hooks auto-format & lint
- ✅ AI-assisted coding encouraged - use responsibly

**Need details?** Read the sections below. **Questions?** [Discord](https://discord.gg/gcSCFGUtsd)

---

## Table of Contents

- [License and Legal](#license-and-legal)
- [Getting Started](#getting-started)
- [Development Setup](#development-setup)
- [Development Workflow](#development-workflow)
- [Code Quality](#code-quality)
- [Testing](#testing)
- [AI-Assisted Development](#ai-assisted-development)
- [Submitting Changes](#submitting-changes)
- [Additional Resources](#additional-resources)

## License and Legal

### Project License

By contributing to BirdNET-Go, you agree that your contributions will be licensed under **[CC BY-NC-SA 4.0](LICENSE)**:

- ✅ **Attribution** - Give appropriate credit
- ✅ **ShareAlike** - Same license for derivatives
- ❌ **NonCommercial** - No commercial use without permission
- ✅ **No Additional Restrictions** - Don't restrict others' rights

See [LICENSE](LICENSE) for full terms.

### Privacy Policy

BirdNET-Go is **privacy-first** with zero data collection by default. Contributors must:

- Follow privacy-by-design principles
- Never add telemetry without explicit user opt-in
- Anonymize sensitive data (URLs, credentials, paths) in logs
- Document any external services in [PRIVACY.md](PRIVACY.md)

Read the full [Privacy Policy](PRIVACY.md) for details.

### Contributor Agreement

By submitting a pull request, you certify:

1. ✅ You have the right to submit under CC BY-NC-SA 4.0
2. ✅ Your contribution is original work or you have permission
3. ✅ You've read the [privacy policy](PRIVACY.md)
4. ✅ Your contribution follows privacy-by-design principles

## Getting Started

Before contributing:

1. **Read the [License](#license-and-legal)** and [Privacy Policy](PRIVACY.md)
2. **Review [ARCHITECTURE.md](ARCHITECTURE.md)** - Understand the tech stack
3. **Check [existing issues](https://github.com/tphakala/birdnet-go/issues)** - Avoid duplicates
4. **Join [Discord](https://discord.gg/gcSCFGUtsd)** - For discussions and support
5. **Read relevant CLAUDE.md files** - Development guidelines:
   - [CLAUDE.md](CLAUDE.md) - Project overview and universal rules
   - [internal/CLAUDE.md](internal/CLAUDE.md) - Go backend guidelines
   - [frontend/CLAUDE.md](frontend/CLAUDE.md) - Svelte 5 frontend guidelines
   - [internal/api/v2/CLAUDE.md](internal/api/v2/CLAUDE.md) - API v2 guidelines

   **Note:** CLAUDE.md files serve all contributors (AI-assisted or manual).

## Development Setup

**Requirements:**

- Go 1.25+
- Node.js 22.x+ (LTS)
- Build tools (gcc, git, wget)
- TensorFlow Lite C library (auto-downloaded)

### Option 1: Automated Setup (Recommended)

**Linux (apt-based) or macOS (with Homebrew)**

**Step 1: Install Task Runner**

⚠️ **Security Note:** Review scripts before running: [Task install script](https://taskfile.dev/install.sh), [Homebrew install script](https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)

**Linux:**

```bash
sh -c "$(curl --location https://taskfile.dev/install.sh)" -- -d -b /usr/local/bin
```

**macOS:**

```bash
brew install go-task
```

**Alternative (if Go already installed):**

```bash
go install github.com/go-task/task/v3/cmd/task@latest
```

**Step 2: Clone and Setup**

```bash
git clone https://github.com/tphakala/birdnet-go.git
cd birdnet-go
task setup-dev
```

The `setup-dev` task installs:

- Go 1.25.3, Node.js LTS, build tools
- golangci-lint, air, frontend dependencies
- Playwright browsers, git hooks (Husky)

**Note:** If Go is newly installed, run `source ~/.profile`

**Step 3: Start Developing**

```bash
task              # Build project
task dev_server   # Hot reload development
air realtime      # Realtime mode with hot reload
```

### Option 2: Dev Container (VS Code)

**Prerequisites:** [Docker](https://docs.docker.com/get-docker/), [VS Code](https://code.visualstudio.com/), [Dev Containers extension](https://marketplace.visualstudio.com/items?itemName=ms-vscode-remote.remote-containers)

**Setup:**

1. Clone: `git clone https://github.com/tphakala/birdnet-go.git && cd birdnet-go`
2. Open: `code .`
3. Press `F1` → "Dev Containers: Reopen in Container"
4. Wait ~5-10 minutes for setup (frontend will be built automatically)
5. Start: `air realtime` or `task dev_server`

**Alternative Workflow (Active Frontend Development):**

If you're actively developing the frontend and need live updates:

```bash
# Terminal 1: Watch and rebuild frontend on changes
task frontend-watch

# Terminal 2: Run Go server with hot reload
air realtime
```

**Note:** The devcontainer automatically builds the frontend during setup. If you need to rebuild manually, run `task frontend-build`.

**Features:**

- Pre-configured extensions (Go, Svelte, TypeScript, ESLint, Prettier, Playwright, TailwindCSS)
- Multiple shells (bash, zsh, fish, PowerShell)
- Port forwarding (8080)
- Auto-formatting on save

## Development Workflow

### Hot Reload with Air

[Air](https://github.com/air-verse/air) watches for changes and automatically rebuilds:

```bash
air realtime          # Realtime mode with hot reload
task dev_server       # Full development server
```

**What Air does:**

- Watches Go, Svelte, TypeScript, CSS files
- Rebuilds frontend (Svelte + Tailwind)
- Recompiles Go binary with embedded frontend
- Restarts server automatically

### Frontend Development

⚠️ **Important:** Frontend is **embedded** in Go binary at build time. Use `air` or `task dev_server` for development.

**DO NOT use Vite dev server** (`npm run dev`) for normal development - changes won't appear.

**Only use `task frontend-dev` for:**

- Rapid UI prototyping with HMR
- Testing components in isolation
- Running frontend tests

### Building

```bash
task                  # Default build (auto-detect platform)
task linux_amd64      # Cross-platform builds
task linux_arm64
task darwin_amd64
task darwin_arm64
task windows_amd64
task clean            # Clean artifacts
```

## Code Quality

**All code must pass linting before commit.** Pre-commit hooks run automatically.

### Go Linting

Configuration: [.golangci.yaml](.golangci.yaml)

```bash
golangci-lint run -v                    # All files
golangci-lint run -v internal/api/v2/   # Specific path
golangci-lint run --fix                 # Auto-fix
```

### Frontend Linting

Configuration: `frontend/eslint.config.js`, `frontend/.prettierrc`, `frontend/.stylelintrc.json`, `frontend/tsconfig.json`

```bash
task frontend-quality       # Comprehensive check (recommended)
task frontend-lint          # ESLint + Prettier + Stylelint
task frontend-typecheck     # TypeScript checking
npm run ast:all             # AST-grep security/pattern checks
task frontend-lint-fix      # Auto-fix issues
```

### Pre-commit Hooks

Configured automatically via [Husky](https://typicode.github.io/husky/) during `task setup-dev`:

**Go files:**

- Auto-format with `gofmt`
- Lint with `golangci-lint`
- Block commit if errors

**Frontend files:**

- Format with Prettier
- Lint with ESLint + Stylelint
- Type check TypeScript
- Block commit if errors

**Manual initialization (if needed):**

```bash
cd frontend && npm run prepare
ls -la ../.git/hooks/pre-commit  # Verify installation
```

**Troubleshooting:**

```bash
cd frontend && npm run prepare           # Reinitialize
which golangci-lint                      # Check PATH
echo $PATH                               # Verify $HOME/go/bin
```

**Bypassing (strongly discouraged):**

```bash
git commit --no-verify -m "message"
```

⚠️ Bypassed commits may be rejected during code review.

### AST-Grep Analysis

```bash
cd frontend
npm run ast:all              # All checks
npm run ast:security         # Security scanning
npm run ast:svelte5          # Svelte 5 best practices
npm run ast:fix              # Auto-fix
```

See [frontend/doc/AST-GREP-SETUP.md](frontend/doc/AST-GREP-SETUP.md) for details.

## Testing

### Go Tests

```bash
task test                    # All tests
task test-verbose            # Verbose output
task test-coverage           # With coverage
go test -race -v ./...       # Race detector
go test -v ./internal/api/v2/...  # Specific package
```

### Frontend Tests

```bash
task frontend-test              # Unit tests
task frontend-test-coverage     # With coverage
task e2e-test                   # Playwright E2E
task e2e-test-headed            # Headed mode (debug)
task e2e-test-ui                # UI mode
task e2e-report                 # View report
```

## AI-Assisted Development

BirdNET-Go **welcomes AI-assisted coding tools**. The main developer uses [Claude Code](https://claude.ai/claude-code), and all PRs receive [CodeRabbit AI](https://coderabbit.ai/) reviews.

### CLAUDE.md Guidelines

Project guidelines are in CLAUDE.md files (see [Getting Started](#getting-started)). These files work for both AI assistants and manual development.

### Responsible AI Usage

**✅ Good Use:**

- Understand codebase patterns
- Generate boilerplate and tests
- Refactor while maintaining behavior
- Write documentation
- Identify bugs and edge cases

**⚠️ Requirements:**

- Review all AI-generated code
- Understand what code does
- Test thoroughly
- Follow project guidelines
- Respect privacy (no sensitive data sharing)

**❌ Prohibited:**

- Submitting code without understanding
- Bypassing quality checks
- Sharing proprietary/sensitive data
- Unverified licensing/attribution
- Misleading/obfuscated code

### Getting Started with Claude Code

1. Install: [Claude Code guide](https://docs.claude.com/en/docs/claude-code)
2. Open BirdNET-Go repository
3. CLAUDE.md files provide automatic context
4. Ask Claude for help with specific tasks

**Questions?** Join [Discord](https://discord.gg/gcSCFGUtsd) to discuss AI-assisted development.

## Submitting Changes

### Branch Naming

```bash
git checkout -b feature/short-description   # Features
git checkout -b fix/issue-number-desc       # Bug fixes
git checkout -b docs/what-updated           # Documentation
```

### Before Committing

```bash
git pull origin main                        # Update from main
git checkout -b feature/my-feature          # Create branch
golangci-lint run -v                        # Lint Go
task frontend-quality                       # Lint frontend
task test                                   # Test Go
task frontend-test                          # Test frontend
```

### Commit Messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```text
type(scope): brief description

Longer explanation if needed.

Fixes #123
```

**Types:** `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `perf`

**Examples:**

```text
feat(api): add spectrogram generation endpoint
fix(frontend): resolve audio player freeze (BG-14)
docs(contributing): streamline documentation
refactor(analysis): optimize detection pipeline
```

### Pull Request Process

**PR Title:** `type(scope): Brief description`

**PR Template:**

```markdown
## Summary

Brief description of changes.

## Changes

- Change 1
- Change 2

## Testing

- [ ] Go tests pass (`task test`)
- [ ] Frontend tests pass (`task frontend-test`)
- [ ] Linting passes
- [ ] Manual testing completed

## Related Issues

Fixes #123
```

**Review Process:**

- CodeRabbit AI automatically reviews
- Address feedback promptly
- Keep discussions constructive
- Squash commits if requested

### Critical Constraints

- **NEVER expand API v1** - Use `internal/api/v2/`
- **Always lint before commit** - `golangci-lint run -v` and `task frontend-quality`
- **Branch from updated main** - `git pull origin main` first
- **No `any` types in TypeScript** - Properly type all parameters

## Additional Resources

### Documentation

- [User Guide](doc/wiki/guide.md)
- [Installation Guide](doc/wiki/installation.md)
- [Security Guide](doc/wiki/security.md)
- [Hardware Recommendations](doc/wiki/hardware.md)
- [AST-Grep Setup](frontend/doc/AST-GREP-SETUP.md)

### Development Guidelines

- [Architecture](ARCHITECTURE.md)
- [Go Backend Guidelines](internal/CLAUDE.md)
- [Frontend Guidelines](frontend/CLAUDE.md)
- [API v2 Guidelines](internal/api/v2/CLAUDE.md)

### Community

- [GitHub Repository](https://github.com/tphakala/birdnet-go)
- [Discord Community](https://discord.gg/gcSCFGUtsd)
- [Issue Tracker](https://github.com/tphakala/birdnet-go/issues)
- [BirdNET Upstream](https://github.com/birdnet-team/BirdNET-Analyzer)

### Quick Command Reference

```bash
# Setup
task setup-dev          # Setup environment
task --list             # List all tasks

# Development
air realtime            # Hot reload
task dev_server         # Full dev server

# Quality
golangci-lint run -v    # Go linting
task frontend-quality   # Frontend quality

# Testing
task test               # Go tests
task frontend-test      # Frontend tests
task e2e-test           # E2E tests

# Building
task                    # Default build
task clean              # Clean artifacts
```

---

**Thank you for contributing to BirdNET-Go!** 🎉

**Questions?** [Discord](https://discord.gg/gcSCFGUtsd) | [Discussions](https://github.com/tphakala/birdnet-go/discussions) | [Issues](https://github.com/tphakala/birdnet-go/issues)
