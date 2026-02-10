# Changelog and Release Guide

## Overview

This project uses automated changelog generation based on **Conventional Commits** and GitHub releases.

## Conventional Commit Format

All commit messages should follow this format:

```
<type>(<scope>): <subject>

<body>

<footer>
```

### Commit Types

- **feat**: A new feature (triggers minor version bump)
- **fix**: A bug fix (triggers patch version bump)
- **docs**: Documentation only changes
- **style**: Code style changes (formatting, missing semi-colons, etc.)
- **refactor**: Code change that neither fixes a bug nor adds a feature
- **perf**: Performance improvements
- **test**: Adding or updating tests
- **chore**: Maintenance tasks, dependency updates
- **ci**: CI/CD configuration changes
- **build**: Build system or external dependency changes

### Examples

```bash
# Feature
git commit -m "feat(controller): add automatic retry logic for failed runs"

# Bug fix
git commit -m "fix(webhook): resolve validation error for import mode"

# Documentation
git commit -m "docs(readme): update installation instructions"

# Breaking change
git commit -m "feat(api): redesign TFRun status structure

BREAKING CHANGE: Status field structure has changed, requires migration"

# Multiple changes
git commit -m "chore: update dependencies and improve error handling

- Upgrade controller-runtime to v0.19.1
- Add structured logging for better debugging
- Improve error messages in webhook validation"
```

## Creating a Release

### Method 1: Using Git Tags (Automated)

1. **Ensure you're on main branch with latest changes:**
   ```bash
   git checkout main
   git pull origin main
   ```

2. **Create and push a version tag:**
   ```bash
   # For a new feature release
   git tag -a v0.2.0 -m "Release v0.2.0"
   git push origin v0.2.0
   ```

3. **The workflow automatically:**
   - Generates changelog from commits since last tag
   - Creates a GitHub Release with the changelog
   - Updates CHANGELOG.md file
   - Categorizes changes by type (features, fixes, etc.)

### Method 2: GitHub Releases UI

1. Go to **Releases** → **Draft a new release**
2. Click **Choose a tag** → Enter new tag (e.g., `v0.2.0`)
3. Click **Generate release notes** (auto-generates from PRs)
4. Edit as needed and publish

## Semantic Versioning

We follow [Semantic Versioning](https://semver.org/) (SEMVER):

- **MAJOR** (v**1**.0.0): Breaking changes
- **MINOR** (v0.**2**.0): New features (backward compatible)
- **PATCH** (v0.0.**1**): Bug fixes (backward compatible)

### Examples:
- `v0.1.0` → `v0.1.1`: Bug fix
- `v0.1.0` → `v0.2.0`: New feature
- `v0.1.0` → `v1.0.0`: Breaking change

## Workflow Triggers

The release workflow triggers on:
- **Push of version tags** matching `v*.*.*` (e.g., v1.0.0, v0.2.1)

## PR Labels for Better Changelogs

Add labels to PRs to categorize them in changelogs:

- `feature` / `enhancement` → 🚀 Features
- `fix` / `bug` / `bugfix` → 🐛 Bug Fixes
- `docs` / `documentation` → 📝 Documentation
- `test` / `tests` → 🧪 Tests
- `chore` / `refactor` → 🔧 Maintenance
- `security` → 🔒 Security

## Manual CHANGELOG.md Updates

If you prefer manual updates, edit `CHANGELOG.md`:

```markdown
## [Unreleased]

### Added
- New feature description

### Fixed
- Bug fix description

### Changed
- Change description
```

Before release, move items under a new version heading:

```markdown
## [0.2.0] - 2026-02-10

### Added
- New feature description
```

## Tips

1. **Write clear commit messages** - They become your changelog
2. **Use PR descriptions** - Include detailed context for reviewers
3. **Tag releases consistently** - Use semantic versioning
4. **Review generated changelog** - Edit if needed before publishing

## Tools

- **Changelog Generator**: [release-changelog-builder-action](https://github.com/mikepenz/release-changelog-builder-action)
- **Format Guide**: [Conventional Commits](https://www.conventionalcommits.org/)
- **Versioning**: [Semantic Versioning](https://semver.org/)
