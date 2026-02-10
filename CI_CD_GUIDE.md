# CI/CD Quick Reference

## 🚀 Overview

This project has **fully automated CI/CD** with semantic versioning and Docker image publishing.

## Workflows

### 1. **Pull Request** (Any branch → main)

**Triggers:** Opening/updating a PR

**What Happens:**
- ✅ Run unit tests with coverage
- ✅ Run linting (golangci-lint)
- ✅ Validate code formatting
- ✅ Run `go vet` static analysis

**Requirements:** All checks must pass before merging

---

### 2. **Merge to Main** (Automatic Versioning)

**Triggers:** Push/merge to `main` branch

**What Happens:**
1. ✅ Run all tests and linting
2. 🤖 **Semantic Release** analyzes commits:
   - Determines next version (based on commit types)
   - Generates changelog from commits
   - Creates git tag (e.g., `v1.2.3`)
   - Creates GitHub release with notes
   - Updates CHANGELOG.md
3. 🐳 Builds multi-platform Docker images
4. 📦 Pushes to Docker Hub with version tags

**Docker Tags Created:**
```
vimalvi/orchestrator-api:1.2.3
vimalvi/orchestrator-api:1.2
vimalvi/orchestrator-api:1
vimalvi/orchestrator-api:latest
vimalvi/orchestrator-api:main
```

---

## Commit Message → Version Mapping

| Commit Message Example | Version Change | Docker Tags |
|------------------------|----------------|-------------|
| `fix: resolve bug` | `1.0.0` → `1.0.1` | `:1.0.1`, `:1.0`, `:1`, `:latest` |
| `feat: add feature` | `1.0.0` → `1.1.0` | `:1.1.0`, `:1.1`, `:1`, `:latest` |
| `feat!: breaking change` | `1.0.0` → `2.0.0` | `:2.0.0`, `:2.0`, `:2`, `:latest` |
| `docs: update README` | No release | (no tags created) |
| `chore: update deps` | No release | (no tags created) |

---

## Developer Workflow

### Standard Development

```bash
# 1. Create feature branch
git checkout -b feat/my-feature

# 2. Make changes and commit using conventional format
git add .
git commit -m "feat(controller): add retry mechanism"

# 3. Push and create PR
git push origin feat/my-feature

# 4. Wait for CI checks ✅

# 5. Merge PR to main
# → Automatic version & release created! 🎉
```

### Commit Message Format

```bash
# Bug fix (patch: 1.0.0 → 1.0.1)
git commit -m "fix: prevent nil pointer dereference"

# New feature (minor: 1.0.0 → 1.1.0)
git commit -m "feat: add webhook validation"

# Breaking change (major: 1.0.0 → 2.0.0)
git commit -m "feat!: redesign API structure

BREAKING CHANGE: Response format changed from XML to JSON"

# No release
git commit -m "docs: update installation guide"
```

---

## Files & Configuration

| File | Purpose |
|------|---------|
| [.github/workflows/ci.yml](.github/workflows/ci.yml) | CI/CD pipeline (tests + Docker) |
| [.github/workflows/semantic-release.yml](.github/workflows/semantic-release.yml) | Automatic versioning |
| [.releaserc.json](.releaserc.json) | Semantic release config |
| [CHANGELOG.md](CHANGELOG.md) | Auto-generated changelog |
| [SEMANTIC_VERSIONING.md](SEMANTIC_VERSIONING.md) | Detailed guide |

---

## Setup Requirements

### GitHub Repository Secrets

Add these in **Settings** → **Secrets and variables** → **Actions**:

| Secret | Description |
|--------|-------------|
| `DOCKER_USERNAME` | Docker Hub username |
| `DOCKER_PASSWORD` | Docker Hub access token |

### Permissions

The workflows need:
- ✅ Read/Write permissions for contents
- ✅ Read/Write permissions for pull requests
- ✅ Read/Write permissions for issues

Set in **Settings** → **Actions** → **General** → **Workflow permissions**

---

## Quick Commands

```bash
# Run tests locally
make test

# Build locally
make build

# Build Docker image locally
make docker-build

# Check what version would be released (dry-run)
# (requires local semantic-release setup)
npx semantic-release --dry-run
```

---

## Examples

### Creating a Patch Release

```bash
git commit -m "fix(webhook): correct validation logic for import mode"
git push origin main
# → Creates v1.0.1 automatically
```

### Creating a Minor Release

```bash
git commit -m "feat(controller): add support for parallel execution"
git push origin main
# → Creates v1.1.0 automatically
```

### Creating a Major Release

```bash
git commit -m "feat!: change configuration format to YAML

BREAKING CHANGE: Configuration files must now use YAML instead of JSON.
Migration guide available in docs/migration.md"
git push origin main
# → Creates v2.0.0 automatically
```

### Multiple Changes

```bash
git commit -m "feat: add retry logic and improve error handling

- Implement exponential backoff for failed operations
- Add detailed error messages with context
- Update documentation for error codes

Closes #42"
git push origin main
# → Creates v1.1.0 with full changelog
```

---

## Troubleshooting

### No Release Created
- ✅ Check commit message format (must be conventional)
- ✅ Commits of type `docs`, `chore`, `test`, `ci` don't trigger releases
- ✅ View logs in **Actions** tab

### CI Failed
- ✅ Check test failures in **Actions** → **CI/CD Pipeline**
- ✅ Run `make test` locally to reproduce
- ✅ Ensure code is formatted: `make fmt`

### Docker Push Failed
- ✅ Verify `DOCKER_USERNAME` and `DOCKER_PASSWORD` secrets are set
- ✅ Check Docker Hub rate limits
- ✅ Verify repository exists on Docker Hub

---

## Links

- 📚 [Semantic Versioning Guide](SEMANTIC_VERSIONING.md)
- 📝 [Changelog](CHANGELOG.md)
- 🔧 [Actions Workflows](.github/workflows/)
- 🐳 [Docker Hub](https://hub.docker.com/r/vimalvi/orchestrator-api)
