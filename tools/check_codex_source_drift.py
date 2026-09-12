#!/usr/bin/env python3
"""Read-only source drift gate. No fetch, checkout, credentials, or repository writes."""
import argparse
import json
import os
from pathlib import Path, PurePosixPath
import re
import subprocess
import sys
import unittest
import urllib.error
import urllib.parse
import urllib.request


SHA = re.compile(r"[0-9a-f]{40}")
MAX_API_BYTES = 16 * 1024 * 1024


def require(condition, message):
    if not condition:
        raise ValueError(message)


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, "duplicate JSON member")
        result[key] = value
    return result


def valid_sha(value):
    return isinstance(value, str) and SHA.fullmatch(value) is not None


def load_manifest(path):
    with open(path, encoding="utf-8") as stream:
        value = json.load(stream, object_pairs_hook=unique_object)
    require(isinstance(value, dict), "manifest must be an object")
    require(value.get("repository") == "openai/codex", "only the public openai/codex source is supported")
    require(valid_sha(value.get("baseline")), "baseline must be a pinned full commit SHA")
    paths = value.get("paths")
    require(isinstance(paths, list) and bool(paths), "watched paths must be a nonempty list")
    seen = set()
    for path in paths:
        require(isinstance(path, str) and re.fullmatch(r"[A-Za-z0-9_./-]+", path)
                and not path.startswith(("/", "-")), "invalid watched path")
        require(all(part not in ("", ".", "..") for part in path.split("/")), "unsafe watched path")
        require(str(PurePosixPath(path)) == path and path not in seen, "duplicate or noncanonical watched path")
        seen.add(path)
    return value


def git_read(repo, *args):
    # Ignore ambient Git credentials/config/overrides and never create optional locks.
    env = {key: value for key, value in os.environ.items()
           if not key.startswith("GIT_") and key in ("PATH", "SYSTEMROOT", "SystemRoot", "WINDIR", "TMPDIR", "TEMP", "TMP")}
    env.update(GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull,
               GIT_OPTIONAL_LOCKS="0", GIT_TERMINAL_PROMPT="0", GIT_NO_LAZY_FETCH="1", LC_ALL="C")
    result = subprocess.run(
        ["git", "--no-optional-locks", "-c", "core.fsmonitor=false",
         "-C", str(repo), *args], env=env, capture_output=True, timeout=30)
    require(result.returncode == 0, "local git read failed (reference/object unavailable); no fetch attempted")
    return result.stdout


def resolve_local(repo, ref):
    require(isinstance(ref, str) and re.fullmatch(r"[A-Za-z0-9_./-]+", ref)
            and not ref.startswith("-"), "invalid candidate reference")
    sha = git_read(repo, "rev-parse", "--verify", "--end-of-options", ref + "^{commit}").decode().strip()
    require(valid_sha(sha), "reference did not resolve to a full commit SHA")
    return sha


def local_entries(repo, commit, paths):
    entries = {}
    # Literal pathspecs prevent wildcard expansion; blob comparison also detects file mode drift.
    raw = git_read(repo, "ls-tree", "-z", commit, "--", *[f":(literal){path}" for path in paths])
    for entry in raw.split(b"\0"):
        if not entry:
            continue
        metadata, path = entry.split(b"\t", 1)
        mode, kind, sha = metadata.decode("ascii").split()
        entries[path.decode("utf-8")] = (mode, kind, sha)
    return entries


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise ValueError("unexpected redirect from public source API")


def api_json(endpoint):
    require(endpoint.startswith("/") and "://" not in endpoint, "invalid API endpoint")
    request = urllib.request.Request("https://api.github.com/repos/openai/codex" + endpoint,
                                    headers={"Accept": "application/vnd.github+json",
                                             "User-Agent": "sub2api-read-only-source-drift"})
    # No environment proxy, netrc, token, or authentication handler is used.
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    with opener.open(request, timeout=30) as response:
        require(response.status == 200, "public source API did not return 200")
        data = response.read(MAX_API_BYTES + 1)
    require(len(data) <= MAX_API_BYTES, "public source API response exceeds limit")
    value = json.loads(data, object_pairs_hook=unique_object)
    require(isinstance(value, dict), "public source API returned invalid JSON")
    return value


def api_commit(ref):
    require(isinstance(ref, str) and re.fullmatch(r"[A-Za-z0-9_./-]+", ref)
            and not ref.startswith("-"), "invalid candidate reference")
    value = api_json("/commits/" + urllib.parse.quote(ref, safe=""))
    sha = value.get("sha")
    commit = value.get("commit")
    tree = commit.get("tree") if isinstance(commit, dict) else None
    require(valid_sha(sha) and isinstance(tree, dict) and valid_sha(tree.get("sha")),
            "incomplete public commit response")
    if valid_sha(ref):
        require(sha == ref, "public API returned a different commit than the pin")
    return sha, tree["sha"]


def api_tree_entries(value):
    require(isinstance(value, dict) and value.get("truncated") is False
            and isinstance(value.get("tree"), list), "missing or truncated public source tree")
    result = {}
    for entry in value["tree"]:
        require(isinstance(entry, dict), "invalid public tree entry")
        path, mode, kind, sha = (entry.get(key) for key in ("path", "mode", "type", "sha"))
        require(isinstance(path, str) and path not in result and valid_sha(sha)
                and mode in ("100644", "100755", "040000", "120000", "160000")
                and kind in ("blob", "tree", "commit"), "incomplete or duplicate public tree entry")
        result[path] = (mode, kind, sha)
    return result


def compare_entries(baseline, candidate, paths):
    changed = []
    for path in paths:
        before = baseline.get(path)
        require(before is not None and before[0] in ("100644", "100755") and before[1] == "blob",
                f"baseline watched file missing or not regular: {path}")
        if before != candidate.get(path):
            changed.append(path)
    return changed


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", type=Path, default=Path(__file__).with_name("codex-source-references.json"))
    source = parser.add_mutually_exclusive_group()
    source.add_argument("--repo", type=Path, help="existing complete local upstream checkout; never fetched")
    source.add_argument("--network", action="store_true", help="explicit anonymous HTTPS GETs to public GitHub")
    parser.add_argument("--candidate", help="commit or ref; defaults to local HEAD / public main")
    parser.add_argument("--selftest", action="store_true")
    args = parser.parse_args()
    if args.selftest:
        suite = unittest.defaultTestLoader.discover(str(Path(__file__).with_name("tests")), pattern="test_source_drift.py")
        return 0 if unittest.TextTestRunner(verbosity=2).run(suite).wasSuccessful() else 1
    if not args.repo and not args.network:
        parser.error("choose an existing --repo or explicitly enable --network")
    try:
        manifest = load_manifest(args.manifest)
        baseline = manifest["baseline"]
        paths = manifest["paths"]
        if args.repo:
            require(resolve_local(args.repo, baseline) == baseline, "baseline did not resolve exactly")
            candidate = resolve_local(args.repo, args.candidate or "HEAD")
            before = local_entries(args.repo, baseline, paths)
            after = local_entries(args.repo, candidate, paths)
        else:
            print("Read-only network check: anonymous GitHub HTTPS GETs; no credentials or repository writes.", flush=True)
            baseline, tree = api_commit(baseline)
            before = api_tree_entries(api_json(f"/git/trees/{tree}?recursive=1"))
            candidate, tree = api_commit(args.candidate or "main")
            after = api_tree_entries(api_json(f"/git/trees/{tree}?recursive=1"))
        changed = compare_entries(before, after, paths)
        print(f"baseline={baseline} candidate={candidate}")
        for path in changed:
            print(f"DRIFT {path}")
        if changed:
            print("Manual source/contract review required; the gate made no changes.")
            return 1
        print(f"unchanged: {len(paths)} watched source files")
        return 0
    except (OSError, ValueError, UnicodeError, subprocess.SubprocessError, urllib.error.URLError) as exc:
        print(f"source drift check unavailable: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
