## What and why

## Release
<!-- The "Release check" workflow enforces this. Tick the one that applies. -->
- [ ] Shipped code changed: I bumped `Version` in `main.go` and moved the notes from `## [Unreleased]` into a new `## [vX.Y.Z]` section of `CHANGELOG.md`
- [ ] Only tests, docs or workflows changed: no release needed
- [ ] Shipped code changed but should not be released yet: I added the `no-release` label

## Checked
- [ ] `go vet ./...` and `go test -count=1 ./...` pass
- [ ] Numbers and claims in docs come from a measurement I can point to
