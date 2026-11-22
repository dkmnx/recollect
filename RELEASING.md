# Releasing Recollect

The engine and the OpenCode adapter release independently. An engine tag starts
the release workflow. An adapter tag does not.

The two version numbers are independent. Engine `v0.1.1` and adapter `0.1.1`
can exist at the same time. They are different tags for different products.
Create both tags on one commit only when you intend to ship both.

## Placeholders

Replace these before you run a command.

| Placeholder | Use                                       | Example  |
| ----------- | ----------------------------------------- | -------- |
| `vX.Y.Z`    | Engine tag and engine changelog heading   | `v0.2.0` |
| `vA.B.C`    | Adapter tag and adapter changelog heading | `v0.1.2` |
| `A.B.C`     | Adapter version in npm commands           | `0.1.2`  |

## Before any release

- Confirm `main` is green in CI.
- Install `git` and `gh`. The Go toolchain and analysis tools come from
  [CONTRIBUTING.md](CONTRIBUTING.md).
- Run the checks in [CONTRIBUTING.md](CONTRIBUTING.md).
- Update the changelog for the part you release.
- Leave the install examples in `README.md` and `adapters/opencode/README.md`
  unchanged. They use `@latest` or a `vX.Y.Z` placeholder and do not need a
  version bump.

## Engine release

1. Add a `## [vX.Y.Z] - YYYY-MM-DD` section to `CHANGELOG.md`.
2. Commit the changelog.
3. Push the commit, then create and push the tag.

   ```sh
   git push origin main
   git tag -a vX.Y.Z -m "vX.Y.Z"
   git push origin vX.Y.Z
   ```

4. Watch the `Release` workflow. It runs tests and the audit, then passes the
   matching changelog section to GoReleaser with `--release-notes`. The workflow
   is defined in [release.yml](.github/workflows/release.yml) and the archive
   list in [.goreleaser.yaml](.goreleaser.yaml). A missing changelog section
   fails before anything is published.
5. Verify the run and the release.

   ```sh
   gh run list --repo dkmnx/recollect --workflow release.yml --limit 1
   gh release view vX.Y.Z --repo dkmnx/recollect
   ```

   Assets are `recollect_<version>_<os>_<arch>.tar.gz`, or `.zip` on Windows.
6. Run one archive and the install script.

   ```sh
   sh scripts/install.sh --version vX.Y.Z --dir /tmp/recollect-check
   /tmp/recollect-check/recollect version
   ```

7. Confirm the release badge shows the new tag.

## Adapter release

The adapter publishes to npm. Its version is in
`adapters/opencode/package.json`. This flow also needs `npm`, `bun`, and the
`opencode` CLI. When the minimum engine version changes, record it in the
adapter changelog and in the Requirements section of
`adapters/opencode/README.md`.

1. Add a `## [vA.B.C] - YYYY-MM-DD` section to
   `adapters/opencode/CHANGELOG.md`.
2. Bump the version without creating a tag.

   ```sh
   cd adapters/opencode
   npm version A.B.C --no-git-tag-version
   ```

   `--no-git-tag-version` stops npm from creating a local `vA.B.C` tag. Pushing
   that tag would start the engine release workflow.
3. Verify the package.

   ```sh
   bun test
   bun run typecheck
   npm publish --dry-run
   ```

4. Commit the version and the changelog.
5. Publish the package.

   ```sh
   npm publish --access public
   ```

6. Tag the published commit.

   ```sh
   git push origin main
   git tag -a opencode-recollect-vA.B.C -m "opencode-recollect vA.B.C"
   git push origin opencode-recollect-vA.B.C
   ```

7. Verify the published version.

   ```sh
   npm view opencode-recollect version
   opencode plugin add opencode-recollect@A.B.C
   ```

The adapter tag does not match the engine workflow pattern `v*`. It is a marker
until an adapter workflow exists.

## Releasing both

Create both tags on one commit only when you ship both. Use the engine tag from
the engine section and the adapter tag from the adapter section. The numbers do
not have to match.

```sh
git push origin main
git tag -a vX.Y.Z -m "vX.Y.Z"
git tag -a opencode-recollect-vA.B.C -m "opencode-recollect vA.B.C"
git push origin vX.Y.Z opencode-recollect-vA.B.C
```

The engine tag runs GoReleaser. Publish the adapter with npm as above.

## Changelog links

Keep the tag series separate.

- Engine: compare the previous engine tag to the new engine tag.
- Adapter: compare the previous adapter tag to the new adapter tag.
- First release: use the initial commit
  `9ddfd25cb1f64ff34c071785c27100c5caa5ae71`.

Use the adapter tag for new adapter links. Early adapter releases may link to
an engine tag because both tags were created on one commit. Never point an
engine entry at an adapter tag.

## Tags and versions are immutable after publish

If you push a wrong tag, cancel the `Release` run before deleting the tag.
Deleting the tag does not stop a running workflow.

```sh
gh run list --repo dkmnx/recollect --workflow release.yml --limit 1
gh run cancel <run-id> --repo dkmnx/recollect
git tag -d vX.Y.Z
git push --delete origin vX.Y.Z
```

The last command deletes a tag from the remote. It does not undo a release that
GoReleaser already published. Fix a released version with a new patch release.

- After `npm publish`, do not reuse the version. Bump the patch and tag again.
- Do not move a published tag.
