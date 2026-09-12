import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
BASH = shutil.which("bash")


def yaml_config(path):
    result = subprocess.run([
        "ruby", "-ryaml", "-rjson", "-e",
        "puts YAML.safe_load(File.read(ARGV[0]), permitted_classes: [], aliases: true).to_json", str(path),
    ], text=True, capture_output=True, check=True)
    return json.loads(result.stdout)


class InstallerTests(unittest.TestCase):
    def test_repository_override_and_current_default(self):
        declaration = next(line for line in (ROOT / "deploy/install.sh").read_text().splitlines()
                           if line.startswith("GITHUB_REPO="))
        for override, expected in ((None, "nianzs/sub2api"), ("", "nianzs/sub2api"),
                                   ("example/rehearsal", "example/rehearsal")):
            env = dict(os.environ)
            env.pop("GITHUB_REPO", None)
            if override is not None:
                env["GITHUB_REPO"] = override
            # Evaluate the real declaration, without invoking installation or its Bash 4 UI.
            script = declaration + '\nprintf "%s\\n" "$GITHUB_REPO"\n'
            result = subprocess.run(["sh", "-c", script],
                                    text=True, capture_output=True, env=env)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.strip(), expected)

    def test_systemd_does_not_force_image_model(self):
        values = {}
        for line in (ROOT / "deploy/sub2api.service").read_text().splitlines():
            if line.startswith("Environment="):
                key, value = line.removeprefix("Environment=").split("=", 1)
                values[key] = value
        self.assertNotIn("SUB2API_IMAGES_MAIN_MODEL", values)


class ReleaseTests(unittest.TestCase):
    def test_tool_fixtures_are_not_ignored_by_repository_rules(self):
        result = subprocess.run(["git", "check-ignore", "--no-index", "--quiet",
                                 "tools/tests/test_operations_config.py"], cwd=ROOT)
        self.assertEqual(result.returncode, 1)

    def test_tag_version_reaches_linked_binary_without_version_artifact(self):
        with tempfile.TemporaryDirectory() as directory:
            env = dict(os.environ, GOPROXY="off", GOSUMDB="off", GOWORK="off",
                       GOTOOLCHAIN="local", GOFLAGS="", GOCACHE="/tmp/sub2api-operations-go-cache")
            for name in (".goreleaser.yaml", ".goreleaser.simple.yaml"):
                config = yaml_config(ROOT / name)
                self.assertEqual(config["release"]["prerelease"], "auto")
                build = next(value for value in config["builds"] if value["id"] == "sub2api")
                for version in ("9.8.7", "9.8.7-rc.2"):
                    with self.subTest(config=name, version=version):
                        flags = [value.replace("{{.Version}}", version).replace("{{.Commit}}", "fixture")
                                 .replace("{{.Date}}", "2026-09-12") for value in build["ldflags"]]
                        binary = Path(directory) / "version-test"
                        result = subprocess.run(["go", "build", "-o", str(binary), "-ldflags", " ".join(flags),
                                                 str(ROOT / "tools/tests/release_version_fixture.go")],
                                                text=True, capture_output=True, env=env)
                        self.assertEqual(result.returncode, 0, result.stderr)
                        reported = subprocess.check_output([str(binary)], text=True).strip()
                        self.assertEqual(reported, version)

    def test_drift_workflow_is_independent_read_only_and_credential_free(self):
        path = ROOT / ".github/workflows/codex-source-drift.yml"
        self.assertTrue(path.exists(), "dedicated drift workflow is missing")
        workflow = yaml_config(path)
        self.assertEqual(workflow["permissions"], {"contents": "read"})
        jobs = workflow["jobs"]
        self.assertEqual(len(jobs), 1)
        job = next(iter(jobs.values()))
        for step in job["steps"]:
            if step.get("uses", "").startswith("actions/checkout@"):
                self.assertIs(step["with"]["persist-credentials"], False)
        # The entire command surface is the checker/test CLI, not arbitrary git maintenance.
        commands = [step["run"] for step in job["steps"] if "run" in step]
        self.assertIn("python3 tools/check_codex_source_drift.py --network", commands)
        for command in commands:
            self.assertTrue(command.startswith("python3 "), command)
        self.assertNotIn("secrets.", json.dumps(workflow))


class RunnerTests(unittest.TestCase):
    def test_failed_route_inspection_cannot_masquerade_as_no_routes(self):
        for ipv6 in ("0", "1"):
            env = dict(os.environ, PATH=str(ROOT / "tools/tests/fixtures/runner-bin") + os.pathsep + os.environ["PATH"],
                       OFFLINE_FIXTURE_INSIDE="1", OFFLINE_FIXTURE_ROUTE_IPV6=ipv6)
            result = subprocess.run(["sh", str(ROOT / "tools/run-codex-fingerprint-offline.sh"),
                                     "--inside", "net:[parent]", "user:[parent]", "/never-executed", "^TestFixture$"],
                                    text=True, capture_output=True, env=env)
            self.assertEqual(result.returncode, 83, result.stderr)

    def test_timeout_and_namespace_failures_are_not_success(self):
        with tempfile.TemporaryDirectory() as directory:
            env = dict(os.environ, PATH=str(ROOT / "tools/tests/fixtures/runner-bin") + os.pathsep + os.environ["PATH"],
                       OFFLINE_FIXTURE_LOG=str(Path(directory) / "commands"))
            for variable, code in (("OFFLINE_FIXTURE_TIMEOUT_RC", 73), ("OFFLINE_FIXTURE_UNSHARE_RC", 74)):
                env[variable] = str(code)
                result = subprocess.run(["sh", str(ROOT / "tools/run-codex-fingerprint-offline.sh"),
                                         str(ROOT / "tools/tests/fixtures/runner-bin/elf-placeholder"), "^TestFixture$"],
                                        text=True, capture_output=True, env=env)
                self.assertEqual(result.returncode, code, result.stderr)
                del env[variable]

    def test_capture_compatibility_and_error_propagation(self):
        for args in (["/dev/null"], ["--strict", "/dev/null"]):
            result = subprocess.run(["sh", str(ROOT / "tools/run-codex-fingerprint-offline.sh"), *args],
                                    text=True, capture_output=True)
            self.assertEqual(result.returncode, 1, result.stderr)

    def test_binary_mode_is_explicitly_linux_only_here(self):
        if os.uname().sysname == "Linux":
            self.skipTest("macOS platform-rejection test")
        result = subprocess.run(["sh", str(ROOT / "tools/run-codex-fingerprint-offline.sh"),
                                 "/nonexistent/test", "^TestFixture$"], text=True, capture_output=True)
        self.assertEqual(result.returncode, 2)
        self.assertIn("Linux", result.stderr)

    def test_wsl_launcher_exists_and_parses_when_powershell_available(self):
        path = ROOT / "tools/test-codex-fingerprint-offline.ps1"
        self.assertTrue(path.exists(), "WSL launcher is missing")
        pwsh = shutil.which("pwsh")
        if not pwsh:
            self.skipTest("PowerShell unavailable; no WSL execution claimed")
        command = "$e=$null; $t=$null; [void][System.Management.Automation.Language.Parser]::ParseFile($args[0],[ref]$t,[ref]$e); if($e.Count){$e; exit 1}"
        result = subprocess.run([pwsh, "-NoProfile", "-Command", command, str(path)],
                                text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)


if __name__ == "__main__":
    unittest.main()
