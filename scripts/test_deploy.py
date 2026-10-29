import os
from pathlib import Path
import subprocess
import tempfile
import unittest


UPDATE_SCRIPT = Path(__file__).with_name("update-server.sh").read_text()

class DeployTests(unittest.TestCase):
    def remote_run(self, fail, update=False):
        with tempfile.TemporaryDirectory() as temporary:
            home = Path(temporary)
            install = home / "slack-discord-bridge"
            install.mkdir()
            for name, content in [(".env", "original secrets"), ("compose.yaml", "old compose"), ("matterbridge.toml", "old config")]:
                (install / name).write_text(content)
            source = home / "upstream/deploy"
            source.mkdir(parents=True)
            (source / "compose.yaml").write_text("context: ..\nimage: slack-discord-bridge:local\n")
            (source / "matterbridge.toml").write_text("old source config" if update else "new config")
            repo = source.parent
            def git(*args, cwd=repo):
                return subprocess.check_output(["git", *args], cwd=cwd, text=True, stderr=subprocess.DEVNULL).strip()
            git("init", "-b", "master")
            git("config", "user.name", "Deploy test")
            git("config", "user.email", "deploy@example.com")
            (repo / "scripts").mkdir()
            (repo / "scripts/update-server.sh").write_text(UPDATE_SCRIPT.replace("https://github.com/KeTrevis/matterbridge.git", str(repo)))
            git("add", ".")
            git("commit", "-m", "initial")
            git("clone", str(repo), str(install / "source"), cwd=home)
            if update:
                (source / "matterbridge.toml").write_text("new config")
                git("commit", "-am", "update")
            binary = home / "bin"
            binary.mkdir()
            mock = '''#!/bin/bash
echo "$*" >> "$HOME/commands"
if [[ "$1" == inspect ]]; then
  if [[ "$FAIL" == startup ]]; then echo 'false 1'; else echo 'true 0'; fi
elif [[ "$*" == *'build bridge'* && "$*" != *'--no-build'* && "$FAIL" == build ]]; then
  exit 1
elif [[ "$*" == *'ps -q bridge'* ]]; then
  echo container-id
fi
'''
            for name, body in [("docker", mock), ("flock", "#!/bin/sh\nexit 0\n"), ("sleep", "#!/bin/sh\nexit 0\n")]:
                path = binary / name
                path.write_text(body)
                path.chmod(0o700)
            result = subprocess.run(["bash", str(install / "source/scripts/update-server.sh")], text=True,
                                    env=dict(os.environ, HOME=str(home), PATH=str(binary) + ":" + os.environ["PATH"], FAIL=fail),
                                    capture_output=True)
            self.assertEqual((install / ".env").read_text(), "original secrets")
            backup = next((install / ".backups").iterdir())
            self.assertEqual((backup / "compose.yaml").read_text(), "old compose")
            if fail:
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertEqual((install / "compose.yaml").read_text(), "old compose")
                self.assertEqual((install / "matterbridge.toml").read_text(), "old config")
                if fail == "startup":
                    self.assertIn("restauration", result.stderr)
                    self.assertIn("-f compose.yaml up -d --no-build --pull never bridge", (home / "commands").read_text())
            else:
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertEqual((install / "matterbridge.toml").read_text(), "new config")
                self.assertIn("context: source", (install / "compose.yaml").read_text())

    def test_success(self):
        self.remote_run("")

    def test_existing_checkout_pulls_update(self):
        self.remote_run("", update=True)

    def test_build_failure_keeps_running_installation(self):
        self.remote_run("build")

    def test_startup_failure_restores_previous_installation(self):
        self.remote_run("startup")


if __name__ == "__main__":
    unittest.main()
