# Releasing Recollect

The engine and the OpenCode adapter release independently. An engine tag starts
the release workflow. An adapter tag does not.

## Before any release

- Confirm `main` is green in CI.
- Run the checks in [CONTRIBUTING.md](CONTRIBUTING.md).
- Update the changelog for the part you release.

## Engine release

1. Update `CHANGELOG.md` with the version and date.
2. Commit the changelog.
3. Tag and push:

```sh
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```

1. The `Release` workflow runs tests and the audit, then GoReleaser publishes
   Linux, macOS, and Windows archives with `checksums.txt`.
2. Verify the run and the release:

```sh
gh run list --repo dkmnx/recollect --limit 1
gh release view v0.1.0 --repo dkmnx/recollect
```

Assets are `recollect_<version>_<os>_<arch>.tar.gz`, or `.zip` on Windows.

1. Run one archive and the install script:

```sh
sh scripts/install.sh --version v0.1.0 --dir /tmp/recollect-check
/tmp/recollect-check/recollect version
```

1. Replace the static release badge with the dynamic one after the tag exists.

## Adapter release

The adapter publishes to npm. Its version is in
`adapters/opencode/package.json`. It requires engine `v0.1.0` or later.

1. Update `adapters/opencode/CHANGELOG.md`.
2. Bump the version without creating a tag:

```sh
cd adapters/opencode
npm version patch --no-git-tag-version
```

Use `--no-git-tag-version`. A default npm tag starts with `v` and would start
the engine release workflow.

1. Verify:

```sh
bun test
bun run typecheck
npm publish --dry-run
```

1. Commit the version and changelog.
2. Publish:

```sh
npm publish --access public
```

1. Tag the published commit:

```sh
git tag -a opencode-recollect-v0.1.1 -m "opencode-recollect v0.1.1"
git push origin opencode-recollect-v0.1.1
```

1. Verify:

```sh
npm view opencode-recollect version
opencode plugin add opencode-recollect@0.1.1
```

The adapter tag does not match the engine workflow pattern `v*`. It is a marker
until an adapter workflow exists.

## Releasing both

Create both tags on the same commit:

```sh
git tag -a v0.1.0 -m "v0.1.0"
git tag -a opencode-recollect-v0.1.0 -m "opencode-recollect v0.1.0"
git push origin v0.1.0 opencode-recollect-v0.1.0
```

The engine tag runs GoReleaser. Publish the adapter with npm as above.

## Changelog links

Keep the tag series separate.

- Engine: compare the previous engine tag to the new engine tag.
- Adapter: compare the previous adapter tag to the new adapter tag.
- First release: use the initial commit `9ddfd25cb1f64ff34c071785c27100c5caa5ae71`.

Do not point an adapter entry at an engine tag or the reverse.

## Tags and versions are immutable after publish

- Before publishing, delete a wrong tag locally and remotely.
- After `npm publish`, do not reuse the version. Bump the patch and tag again.
- Do not move a published tag.
