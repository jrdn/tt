#!/usr/bin/env python3
"""Tests for next_version.py: each case builds a throwaway repo and asks it.

Run with ``python3 .github/scripts/test_next_version.py`` (CI does this in the
release job of .github/workflows/test.yml). No dependencies.
"""

import os
import subprocess
import sys
import tempfile
import unittest

SCRIPT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "next_version.py")


def git(repo, *args):
    subprocess.run(
        ("git",) + args,
        cwd=repo,
        check=True,
        capture_output=True,
        text=True,
        env={
            **os.environ,
            "GIT_AUTHOR_NAME": "t",
            "GIT_AUTHOR_EMAIL": "t@example.com",
            "GIT_COMMITTER_NAME": "t",
            "GIT_COMMITTER_EMAIL": "t@example.com",
        },
    )


class NextVersionTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.repo = self.dir.name
        git(self.repo, "init", "-q", "-b", "main")
        self.addCleanup(self.dir.cleanup)

    def commit(self, message):
        git(self.repo, "commit", "-q", "--allow-empty", "-m", message)

    def tag(self, name):
        git(self.repo, "tag", name)

    def run_script(self):
        env = {k: v for k, v in os.environ.items() if k != "GITHUB_OUTPUT"}
        result = subprocess.run(
            (sys.executable, SCRIPT),
            cwd=self.repo,
            check=True,
            capture_output=True,
            text=True,
            env=env,
        )
        return result.stdout.strip()

    def test_fix_bumps_patch(self):
        self.commit("feat: initial")
        self.tag("v0.1.0")
        self.commit("fix: keep the switcher sticky")
        self.assertEqual(self.run_script(), "v0.1.1")

    def test_feat_bumps_minor(self):
        self.commit("fix: initial")
        self.tag("v0.0.8")
        self.commit("feat: add task deep links")
        self.assertEqual(self.run_script(), "v0.1.0")

    def test_bang_bumps_major(self):
        self.tag_baseline("v0.0.8")
        self.commit("feat!: drop the v1 API")
        self.assertEqual(self.run_script(), "v1.0.0")

    def test_breaking_change_footer_bumps_major(self):
        self.tag_baseline("v0.4.2")
        self.commit("refactor: rename the sync flags\n\nBREAKING CHANGE: --push is gone")
        self.assertEqual(self.run_script(), "v1.0.0")

    def test_non_release_types_alone_ship_nothing(self):
        self.tag_baseline("v0.0.8")
        self.commit("docs: explain the release flow")
        self.commit("ci: cache the go build")
        self.assertEqual(self.run_script(), "")

    def test_untagged_subject_still_ships(self):
        # The tt task-id style used before this workflow existed.
        self.tag_baseline("v0.0.7")
        self.commit("esw939r: deep links to tasks and a Copy link button")
        self.assertEqual(self.run_script(), "v0.0.8")

    def test_strongest_commit_wins(self):
        self.tag_baseline("v0.0.8")
        self.commit("fix: one")
        self.commit("feat: two")
        self.commit("chore: three")
        self.assertEqual(self.run_script(), "v0.1.0")

    def test_release_type_override(self):
        self.tag_baseline("v0.0.8")
        self.commit("chore: rework the layout\n\nRelease-Type: minor")
        self.assertEqual(self.run_script(), "v0.1.0")

    def test_skip_release(self):
        self.tag_baseline("v0.0.8")
        self.commit("fix: touch the readme [skip release]")
        self.assertEqual(self.run_script(), "")

    def test_no_commits_since_tag(self):
        self.tag_baseline("v0.0.8")
        self.assertEqual(self.run_script(), "")

    def test_first_release_without_any_tag(self):
        self.commit("feat: initial import")
        self.assertEqual(self.run_script(), "v0.1.0")

    def test_github_output_file(self):
        self.tag_baseline("v0.0.8")
        self.commit("fix: one")
        with tempfile.NamedTemporaryFile("r+", suffix=".out") as out:
            subprocess.run(
                (sys.executable, SCRIPT),
                cwd=self.repo,
                check=True,
                capture_output=True,
                text=True,
                env={**os.environ, "GITHUB_OUTPUT": out.name},
            )
            out.seek(0)
            self.assertEqual(out.read().strip(), "version=v0.0.9")

    def tag_baseline(self, name):
        self.commit("chore: baseline")
        self.tag(name)


if __name__ == "__main__":
    unittest.main(verbosity=2)
