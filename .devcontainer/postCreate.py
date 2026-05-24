#!/usr/bin/env python3

# /// script
# requires-python = ">=3.12"
# dependencies = ["rich"]
# ///

import os
import shutil
import subprocess
import concurrent.futures
import sys
from typing import List, Union
from dataclasses import dataclass
from rich.console import Console
from rich.progress import Progress, SpinnerColumn, TextColumn


@dataclass
class Task:
    name: str
    command: Union[str, List[str]]
    dependencies: List[str] = None

    def __post_init__(self):
        self.dependencies = self.dependencies or []
        if isinstance(self.command, str):
            self.command = [self.command]


class SetupRunner:
    def __init__(self):
        self.console = Console()
        self.tasks = {}
        self.results = {}
        self.progress_tasks = {}
        self.github_token = self.check_github_auth()

    def add_task(self, name: str, command: Union[str, List[str]], dependencies: List[str] = None):
        self.tasks[name] = Task(name, command, dependencies)

    def check_github_auth(self) -> str | None:
        """Check if gh CLI is available and user is logged in, returns token if so."""
        gh_path = shutil.which("gh")

        if not gh_path:
            self.console.print(
                "[yellow]gh CLI not found in PATH, skipping GitHub authentication[/yellow]")
            return None

        try:
            # Check if user is logged in
            result = subprocess.run(
                ["gh", "auth", "status"],
                capture_output=True,
                text=True,
                check=False
            )

            if result.returncode != 0:
                self.console.print(
                    "[yellow]User not logged in to gh CLI, skipping GitHub token setup[/yellow]")
                return None

            # Get the auth token
            token_result = subprocess.run(
                ["gh", "auth", "token"],
                capture_output=True,
                text=True,
                check=True
            )

            token = token_result.stdout.strip()
            if token:
                self.console.print(
                    "[green]✓ GitHub token set from gh CLI[/green]")
                return token
            else:
                self.console.print(
                    "[yellow]gh auth token returned empty result[/yellow]")

            return None

        except subprocess.CalledProcessError as e:
            self.console.print(
                f"[yellow]Error getting GitHub token: {e.stderr}[/yellow]")
            return None  # Non-fatal, continue with other tasks

    def run_command(self, task: Task) -> bool:
        for cmd in task.command:
            try:
                new_env = os.environ.copy()
                if self.github_token:
                    new_env["GITHUB_TOKEN"] = self.github_token
                subprocess.run(cmd, shell=True, check=True,
                               stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=new_env)
            except subprocess.CalledProcessError as e:
                self.console.print(
                    f"[red]Error in {task.name} running command '{cmd}':[/red] {e.stderr.decode()}")
                return False
        return True

    def run_tasks(self):
        with Progress(
            SpinnerColumn(finished_text="[green]\N{check mark}[/green]"),
            TextColumn("[progress.description]{task.description}"),
            console=self.console
        ) as progress:
            with concurrent.futures.ThreadPoolExecutor() as executor:
                futures = {}
                pending_tasks = set(self.tasks.keys())

                for task_name in self.tasks:
                    self.progress_tasks[task_name] = progress.add_task(
                        f"Waiting: {task_name}", total=1)

                while pending_tasks:
                    for task_name in list(pending_tasks):
                        task = self.tasks[task_name]
                        deps_completed = all(
                            self.results.get(dep, False) for dep in task.dependencies)

                        if deps_completed and task_name not in futures:
                            progress.update(self.progress_tasks[task_name],
                                            description=f"Running: {task_name}")
                            futures[task_name] = executor.submit(
                                self.run_command, task)

                    if not futures:
                        continue

                    done, _ = concurrent.futures.wait(
                        futures.values(),
                        timeout=0.1,
                        return_when=concurrent.futures.FIRST_COMPLETED
                    )

                    for future in done:
                        completed_task = None
                        for task_name, fut in futures.items():
                            if fut == future:
                                completed_task = task_name
                                break

                        if completed_task:
                            success = future.result()
                            self.results[completed_task] = success
                            pending_tasks.remove(completed_task)
                            progress.update(self.progress_tasks[completed_task],
                                            description=f"{completed_task}", advance=1)
                            del futures[completed_task]
                            if not success:
                                return False

        return all(self.results.values())


def main():
    runner = SetupRunner()

    runner.add_task(
        "install kubeswitch",
        "sudo -E eget danielfoehrKn/kubeswitch --to /usr/local/bin/switcher"
    )
    runner.add_task(
        "add kubeswitch to profile and install default config",
        [
            "echo 'source <(switcher init bash)' >> ~/.bashrc",
            "echo alias s=\\'switch\\' >> ~/.bashrc",
            "echo 'complete -o default -F __start_switcher s' >> ~/.bashrc",
            "mkdir -p ~/.kube",
            f"[ ! -e ~/.kube/switch-config.yaml ] && ln -s ${os.getcwd()}/.devcontainer/defaults/switch-config.yaml ~/.kube/switch-config.yaml || true"
        ],
        ["install kubeswitch"]
    )
    runner.add_task(
        "install helmfile", [
            "sudo -E eget helmfile/helmfile --to /usr/local/bin/helmfile"]
    )

    runner.add_task(
        "install krew", '''
            cd "$(mktemp -d)" &&
            eget kubernetes-sigs/krew --to ./krew &&
            ./krew install krew
        '''
    )

    runner.add_task(
        "install krew plugins", "kubectl krew install node-shell", [
            "install krew"]
    )

    runner.add_task(
        "install dyff", "sudo -E eget homeport/dyff --to /usr/local/bin/dyff"
    )

    runner.add_task(
        "apt-get update", "sudo apt-get update -qq"
    )
    runner.add_task(
        "install parallel",
        "sudo apt-get install -y -qq parallel",
        ["apt-get update"]
    )

    success = runner.run_tasks()
    sys.exit(0 if success else 1)


if __name__ == "__main__":
    main()
