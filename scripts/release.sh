#!/usr/bin/env bash
#
# Cut a new release and block until the Release workflow succeeds.
#
# Creating the GitHub release fires .github/workflows/release.yml (binary
# uploads + AUR publish). This script creates that release at the tip of
# origin/master and watches the run, exiting non-zero if it fails. The git tag
# is the sole source of version truth.
#
# Invoked by `make release`; version is selected via environment:
#   BUMP=patch|minor|major   bump the latest vX.Y.Z tag
#   TAG=v1.2.3               use an explicit tag
#
set -euo pipefail

BUMP="${BUMP:-}"
TAG="${TAG:-}"

SEMVER_RE='^v[0-9]+\.[0-9]+\.[0-9]+$'

die() { echo "release: $*" >&2; exit 1; }

# Resolve BUMP/TAG into the concrete tag to create (echoes it).
resolve_tag() {
	[[ -n "$TAG" && -n "$BUMP" ]] && die "set either TAG= or BUMP=, not both"
	[[ -z "$TAG" && -z "$BUMP" ]] && die "usage: make release BUMP=patch|minor|major  (or TAG=vX.Y.Z)"

	if [[ -n "$TAG" ]]; then
		[[ "$TAG" =~ $SEMVER_RE ]] || die "invalid tag '$TAG' — expected vX.Y.Z"
		echo "$TAG"
		return
	fi

	local latest major minor patch
	latest=$(git tag -l 'v*.*.*' --sort=-v:refname | grep -E "$SEMVER_RE" | head -n1 || true)
	[[ -n "$latest" ]] || die "no existing vX.Y.Z tag to bump from — pass TAG=vX.Y.Z"
	IFS=. read -r major minor patch <<<"${latest#v}"
	case "${BUMP,,}" in
		patch) patch=$((patch + 1)) ;;
		minor) minor=$((minor + 1)); patch=0 ;;
		major) major=$((major + 1)); minor=0; patch=0 ;;
		*) die "BUMP must be patch, minor, or major (got '$BUMP')" ;;
	esac
	echo "Bumping $latest -> v${major}.${minor}.${patch} ($BUMP)" >&2
	echo "v${major}.${minor}.${patch}"
}

# Fail fast on anything that would make the release tag the wrong commit or
# collide with an existing one.
preflight() {
	local tag=$1
	if ! git diff --quiet || ! git diff --cached --quiet; then
		die "working tree is dirty — commit or stash first"
	fi
	git rev-parse -q --verify "refs/tags/$tag" >/dev/null 2>&1 \
		&& die "tag $tag already exists locally"
	git ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null 2>&1 \
		&& die "tag $tag already exists on origin"
	[[ -z "$(git rev-list origin/master..master 2>/dev/null)" ]] \
		|| die "local master has unpushed commits — push first (the release tags the tip of origin/master)"
}

# Poll for the workflow run the release just triggered. Release events set the
# run's headBranch to the tag name.
find_run() {
	local tag=$1 run_id
	for _ in $(seq 1 30); do
		run_id=$(gh run list --workflow=release.yml --event=release --limit 20 \
			--json databaseId,headBranch \
			--jq "map(select(.headBranch==\"$tag\")) | .[0].databaseId" 2>/dev/null || true)
		if [[ -n "$run_id" && "$run_id" != "null" ]]; then
			echo "$run_id"
			return
		fi
		sleep 5
	done
	die "no Release workflow run found for $tag; inspect: gh run list --workflow=release.yml"
}

main() {
	git fetch -q --tags origin master

	local tag run_id target
	tag=$(resolve_tag)
	preflight "$tag"

	target=$(git rev-parse --short origin/master)
	echo "==> Creating release $tag at $target (tip of origin/master); fires the Release workflow"
	gh release create "$tag" --target master --generate-notes --title "$tag"

	echo "==> Waiting for the Release workflow run to appear"
	run_id=$(find_run "$tag")

	echo "==> Watching run $run_id"
	exec gh run watch "$run_id" --exit-status --interval 15
}

main "$@"
