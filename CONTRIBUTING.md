# Contributing to OnionForge

Thanks for your interest in improving OnionForge! Bug reports, documentation fixes and pull requests are all welcome.

## Ground rules

OnionForge is deliberately small. Before starting larger work, please open an issue to discuss it. Changes are judged against these priorities, in order:

1. correctness
2. persistence: never lose or silently replace an onion identity
3. security: no open proxy, no key leakage, least privilege
4. simplicity and maintainability
5. small image size
6. developer experience

## Development setup

Requirements: Go (see `go.mod`), Docker with Compose v2, and Bash.

```bash
git clone https://github.com/emon5122/onionforge.git
cd onionforge

go test ./...                       # unit tests
tests/run.sh                        # unit + Docker integration suites
tests/run.sh proxy persistence      # selected suites
ONIONFORGE_TEST_TOR=1 tests/run.sh  # also round-trip through the real Tor network
```

`tests/run.sh` builds the image as `emon5122/onionforge:test`. To reuse an image you've already built, set `ONIONFORGE_SKIP_BUILD=1`.

## Pull requests

- Keep each PR focused on one change, and add or update tests for it.
- Run `gofmt`, `go vet ./...` and `tests/run.sh` before pushing.
- Update `README.md` when you change behavior or configuration. `DOCKERHUB.md` is the shorter Docker Hub overview; keep it in sync.
- Use [Conventional Commits](https://www.conventionalcommits.org) for commit messages and PR titles. Releases and the changelog are generated from them by release-please:
  - `feat: …` adds a feature (minor version)
  - `fix: …` fixes a bug (patch version)
  - `docs: …`, `test: …`, `ci: …`, `chore: …`, `refactor: …` don't trigger a release
  - `feat!: …` or a `BREAKING CHANGE:` footer marks a breaking change

## Compatibility

The on-disk identity layout in `/var/lib/tor` and the meaning of existing `onionforge.yml` keys are compatibility-sensitive. Changes to either need a clear migration story and must be called out as breaking.

## Reporting security issues

See [SECURITY.md](SECURITY.md). Please don't open public issues for vulnerabilities.
