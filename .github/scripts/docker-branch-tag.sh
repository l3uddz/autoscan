#!/usr/bin/env bash
# Prints the Docker tag for a branch name. build.yml publishes the branch image
# with this tag, and cleanup.yml removes that tag when the branch is
# deleted. Both workflows use this script, so they always agree. The script
# runs in a checkout, because it reads the branches on origin.
set -euo pipefail

branch="${1:?usage: docker-branch-tag.sh <branch>}"

# to_tag prints the Docker tag for a branch name. A Docker tag allows only
# letters, digits, "_", "." and "-", cannot start with "." or "-", and has at
# most 128 characters. A cut tag ends with a hash of the full branch name, so
# that two long branches with the same start get different tags.
to_tag() {
  local tag="${1//[^A-Za-z0-9_.-]/-}"
  if [[ "$tag" == [.-]* ]]; then
    tag="_$tag"
  fi
  if (( ${#tag} > 128 )); then
    tag="${tag:0:119}-$(printf '%s' "$1" | sha256sum | cut -c1-8)"
  fi
  printf '%s\n' "$tag"
}

tag="$(to_tag "$branch")"

# Releases own "latest" and the version tags, which start with a digit.
# A branch must not publish or remove one of them.
if [[ "$tag" == "latest" || "$tag" =~ ^[0-9] ]]; then
  echo "branch '$branch' maps to the reserved Docker tag '$tag'" >&2
  exit 1
fi

# Two branches, such as fix/a and fix-a, must not share one tag. Otherwise
# one branch replaces the image of the other, and the cleanup of one removes
# the image of the other.
heads="$(git ls-remote --heads origin)"
while read -r _ ref; do
  other="${ref#refs/heads/}"
  if [[ -n "$other" && "$other" != "$branch" && "$(to_tag "$other")" == "$tag" ]]; then
    echo "branches '$branch' and '$other' both map to the Docker tag '$tag'" >&2
    exit 1
  fi
done <<< "$heads"

echo "$tag"
