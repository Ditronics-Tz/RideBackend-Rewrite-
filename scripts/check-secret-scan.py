"""Verify Gitleaks locally using isolated repositories and synthetic fixtures."""
import json
import pathlib
import shutil
import subprocess
import tempfile

repo = pathlib.Path(__file__).resolve().parents[1]
scanner = shutil.which("gitleaks")
if scanner is None:
    candidate = pathlib.Path.home() / "go" / "bin" / "gitleaks.exe"
    if candidate.is_file():
        scanner = str(candidate)
if scanner is None:
    raise SystemExit("Install Gitleaks and ensure it is available on PATH first")

fixture_root = repo.parent / ".tmp" / "secret-scan-fixtures"
fixture_root.mkdir(parents=True, exist_ok=True)
for planted in (False, True):
    with tempfile.TemporaryDirectory(prefix="rida-secret-fixture-", dir=fixture_root) as directory:
        fixture = pathlib.Path(directory)
        if fixture.resolve().parent != fixture_root.resolve():
            raise SystemExit("Fixture cleanup path is outside the designated directory")
        for args in (
            ["init", "-b", "test/planted-secret"],
            ["config", "user.email", "fixture@example.invalid"],
            ["config", "user.name", "Scanner Fixture"],
        ):
            subprocess.run(["git", *args], cwd=fixture, check=True, capture_output=True)
        value = "synthetic-fixture-only-never-a-real-credential" if planted else ""
        (fixture / "credential.fixture").write_text("JWT_SECRET=" + value + "\n", encoding="utf-8")
        subprocess.run(["git", "add", "credential.fixture"], cwd=fixture, check=True, capture_output=True)
        subprocess.run(["git", "commit", "-m", "synthetic scanner fixture"], cwd=fixture, check=True, capture_output=True)
        report = fixture / "redacted-report.json"
        result = subprocess.run(
            [scanner, "git", "--redact=100", "--no-banner", "--config", str(repo / ".gitleaks.toml"),
             "--report-format", "json", "--report-path", str(report), "--log-opts=--all"],
            cwd=fixture, capture_output=True,
        )
        expected = 1 if planted else 0
        if result.returncode != expected:
            raise SystemExit(f"Scanner fixture verdict failed (expected {expected}, got {result.returncode}); output withheld")
        if planted:
            findings = json.loads(report.read_text(encoding="utf-8"))
            if not findings or any(value in json.dumps(finding) for finding in findings):
                raise SystemExit("Scanner fixture report failed redaction checks")
print("PASS: clean fixture accepted; planted-key branch rejected; report values redacted")
