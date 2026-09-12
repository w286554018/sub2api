import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
import zlib


TOOLS = Path(__file__).resolve().parents[1]
WATCHED = "codex-rs/core/src/client.rs"


def write_object(git, kind, data):
    raw = kind.encode() + b" " + str(len(data)).encode() + b"\0" + data
    sha = hashlib.sha1(raw).hexdigest()
    path = git / "objects" / sha[:2] / sha[2:]
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(zlib.compress(raw))
    return sha


def fixture_commit(git, watched=b"identity", unrelated=b"docs", missing=False, mode="100644"):
    tree = write_object(git, "tree", (mode.encode() + b" client.rs\0"
                        + bytes.fromhex(write_object(git, "blob", watched))) if not missing else b"")
    for folder in ("src", "core", "codex-rs"):
        tree = write_object(git, "tree", b"40000 " + folder.encode() + b"\0" + bytes.fromhex(tree))
    # Rebuild the root with a second, unrelated entry in git's tree sort order.
    subtree = zlib.decompress((git / "objects" / tree[:2] / tree[2:]).read_bytes()).split(b"\0", 1)[1]
    tree = write_object(git, "tree", b"100644 README\0" + bytes.fromhex(write_object(git, "blob", unrelated)) + subtree)
    return write_object(git, "commit", (
        f"tree {tree}\nauthor Fixture <fixture@example.invalid> 1 +0000\n"
        "committer Fixture <fixture@example.invalid> 1 +0000\n\nfixture\n").encode())


class DriftTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.repo = self.root / "source"
        self.git = self.repo / ".git"
        (self.git / "objects").mkdir(parents=True)
        (self.git / "refs" / "heads").mkdir(parents=True)
        (self.git / "HEAD").write_text("ref: refs/heads/main\n")
        (self.git / "config").write_text("[core]\nrepositoryformatversion = 0\nbare = false\n")
        self.baseline = fixture_commit(self.git)
        self.manifest = self.root / "references.json"
        self.config = {"repository": "openai/codex", "baseline": self.baseline, "paths": [WATCHED]}
        self.manifest.write_text(json.dumps(self.config))
        (self.git / "refs" / "heads" / "main").write_text(self.baseline + "\n")

    def run_drift(self, candidate="HEAD", *extra):
        result = subprocess.run(
            [sys.executable, str(TOOLS / "check_codex_source_drift.py"),
             "--repo", str(self.repo), "--manifest", str(self.manifest), "--candidate", candidate, *extra],
            text=True, capture_output=True)
        return result

    def test_unchanged_and_unrelated_changes_pass_without_repository_writes(self):
        candidate = fixture_commit(self.git, unrelated=b"changed docs")
        before = {str(p): p.read_bytes() for p in self.git.rglob("*") if p.is_file()}
        result = self.run_drift(candidate)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("unchanged", result.stdout)
        after = {str(p): p.read_bytes() for p in self.git.rglob("*") if p.is_file()}
        self.assertEqual(after, before)

    def test_modified_deleted_or_mode_changed_watched_file_fails(self):
        for opts in ({"watched": b"changed"}, {"missing": True}, {"mode": "100755"}):
            candidate = fixture_commit(self.git, **opts)
            result = self.run_drift(candidate)
            self.assertEqual(result.returncode, 1, result.stderr)
            self.assertIn(WATCHED, result.stdout)

    def test_bad_baseline_missing_paths_and_invalid_manifest_fail_closed(self):
        for config in (
            dict(self.config, baseline="0" * 40), dict(self.config, baseline="main"),
            dict(self.config, paths=[]), dict(self.config, paths=["missing"]),
            dict(self.config, paths=["../escape"]), dict(self.config, paths=[WATCHED, WATCHED]),
            dict(self.config, paths="not-a-list"), dict(self.config, repository="https://evil.invalid"),
        ):
            self.manifest.write_text(json.dumps(config))
            result = self.run_drift()
            self.assertEqual(result.returncode, 2, result.stderr)
            self.assertNotIn("Traceback", result.stderr)

    def test_missing_both_sides_is_not_no_drift(self):
        self.config["baseline"] = fixture_commit(self.git, missing=True)
        self.manifest.write_text(json.dumps(self.config))
        result = self.run_drift(self.config["baseline"])
        self.assertEqual(result.returncode, 2, result.stderr)

    def test_reference_injection_and_implicit_network_rejected(self):
        self.assertEqual(self.run_drift("--help").returncode, 2)
        result = subprocess.run([sys.executable, str(TOOLS / "check_codex_source_drift.py")],
                                text=True, capture_output=True)
        self.assertEqual(result.returncode, 2)
        self.assertIn("--network", result.stderr)

    def test_recursive_tree_completeness_is_required(self):
        path = TOOLS / "check_codex_source_drift.py"
        self.assertTrue(path.exists(), "read-only drift checker is missing")
        spec = importlib.util.spec_from_file_location("drift", path)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        for value in ({}, {"truncated": True, "tree": []},
                      {"truncated": False, "tree": [{"path": WATCHED, "type": "blob"}]}):
            with self.assertRaises(ValueError):
                module.api_tree_entries(value)


if __name__ == "__main__":
    unittest.main()
