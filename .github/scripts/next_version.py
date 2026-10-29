#!/usr/bin/env python3
"""Work out the next release tag from the commits since the last one.

The bump follows Conventional Commits:

    <type>!: ...              or a BREAKING CHANGE: footer   -> major
    feat: ...                                                -> minor
    fix: / perf: / refactor: / revert:                       -> patch
    docs/ chore/ ci/ style/ test/ build                      -> no release alone
    <no type>, e.g. "a0ssm4f: ..."                           -> patch

so a commit that forgets the type still ships rather than silently vanishing.
Any commit may override the result with a ``Release-Type: patch|minor|major``
trailer, and ``[skip release]`` in any message opts the whole push out.

A push with no release-worthy commits prints an empty version and the caller
should not tag. Otherwise the version (``vX.Y.Z``) goes to stdout and, when the
variable is set, to ``$GITHUB_OUTPUT`` as ``version=vX.Y.Z``.

Run with ``--explain`` to print how each commit was classified.
"""

import os
import re
import subprocess
import sys

SKIP_RELEASE_RE = re.compile(r"\[skip release\]", re.IGNORECASE)
BREAKING_SUBJECT_RE = re.compile(r"^[A-Za-z]+(\([^)]*\))?!:")
BREAKING_FOOTER_RE = re.compile(r"^BREAKING[ -]CHANGE:", re.MULTILINE)
SUBJECT_TYPE_RE = re.compile(r"^([A-Za-z]+)(\([^)]*\))?(!)?:")
OVERRIDE_RE = re.compile(
    r"^Release-Type:\s*(patch|minor|major)\s*$", re.IGNORECASE | re.MULTILINE
)

# Bumps we act on, weakest first. "major" means a real 1.0-style break: while
# tt is 0.x that is an explicit decision, not something to infer often.
RANK = {"": 0, "patch": 1, "minor": 2, "major": 3}

# Types that do not ship a new image by themselves: a docs-only or CI-only push
# should not burn a version number.
NO_RELEASE_TYPES = {"docs", "chore", "ci", "style", "test", "build"}

RELEASE_TAG_RE = re.compile(r"v\d+\.\d+\.\d+")


def git(*args):
    return subprocess.run(
        ("git",) + args, check=True, capture_output=True, text=True
    ).stdout


def last_tag():
    """Highest reachable release tag, or "" when the repo has none yet."""
    out = git("tag", "--merged", "HEAD", "--sort=-v:refname", "--list", "v[0-9]*")
    for line in out.splitlines():
        tag = line.strip()
        if RELEASE_TAG_RE.fullmatch(tag):
            return tag
    return ""


def messages_since(tag):
    """Commit messages added since ``tag`` (every commit when it is "")."""
    rev_range = f"{tag}..HEAD" if tag else "HEAD"
    # --no-merges: a merge commit's subject says nothing about the change, and
    # its branch commits are already in the list.
    hashes = git("log", rev_range, "--no-merges", "--format=%H").split()
    return [git("show", "-s", "--format=%B", sha) for sha in hashes]


def classify(message):
    """Release level implied by one commit message: "", patch, minor or major."""
    if SKIP_RELEASE_RE.search(message):
        return ""
    override = OVERRIDE_RE.search(message)
    if override:
        return override.group(1).lower()

    subject, _, body = message.partition("\n")
    subject = subject.strip()
    if BREAKING_SUBJECT_RE.match(subject) or BREAKING_FOOTER_RE.search(body):
        return "major"

    match = SUBJECT_TYPE_RE.match(subject)
    if not match:
        # No conventional prefix (the tt task-id style, or free-form): treat it
        # as a change worth shipping rather than dropping it.
        return "patch"
    kind = match.group(1).lower()
    if kind == "feat":
        return "minor"
    if kind in NO_RELEASE_TYPES:
        return ""
    return "patch"  # fix, perf, refactor, revert, and anything unrecognised


def bump(version, level):
    major, minor, patch = (int(part) for part in version.split("."))
    if level == "major":
        return f"{major + 1}.0.0"
    if level == "minor":
        return f"{major}.{minor + 1}.0"
    return f"{major}.{minor}.{patch + 1}"


def main(argv):
    explain = "--explain" in argv[1:]

    tag = last_tag()
    base = tag.lstrip("v") or "0.0.0"
    level = ""
    counts = {"": 0, "patch": 0, "minor": 0, "major": 0}
    for message in messages_since(tag):
        kind = classify(message)
        counts[kind] += 1
        if explain:
            print(f"{kind or '-':<6} {message.splitlines()[0] if message else '(empty)'}")
        if RANK[kind] > RANK[level]:
            level = kind

    version = f"v{bump(base, level)}" if level else ""
    if explain or not version:
        print(
            f"last tag {tag or '(none)'}: {counts['major']} breaking, "
            f"{counts['minor']} feat, {counts['patch']} other, "
            f"{counts['']} non-release -> {version or 'no release'}",
            file=sys.stderr,
        )

    output = os.environ.get("GITHUB_OUTPUT")
    if output:
        with open(output, "a", encoding="utf-8") as handle:
            handle.write(f"version={version}\n")
    print(version)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
