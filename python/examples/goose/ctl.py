# SPDX-License-Identifier: Apache-2.0
"""The only door this example has to the evidence store: the ``capsulectl-deal`` plugin.

Every operation of a Goose node (a note, a sealed model call, a deal step, a checkpoint, a card)
is one subprocess call of a capsulectl deal verb, run on the node's own profile. The node's home
(``GOOSE_HOME``) is the profile's ``HOME``, so two nodes on one machine never share a profile, a
store or a key. Python never opens the store.

``GOOSE_CAPSULECTL_DEAL`` names the plugin binary; it defaults to ``capsulectl-deal`` on ``PATH``
(the plugin ``capsulectl plugin install`` installs; ``capsulectl deal VERB`` runs the same verb).
``GOOSE_CAPSULE_ENGINE`` names the ``capsule-engine`` binary that makes a deal's admission decision.
"""
from __future__ import annotations

import json
import os
import subprocess
from pathlib import Path
from typing import Any

__all__ = ["Ctl", "Refused", "engine"]

#: A verb that can run for a while (a witness round trip) still ends: no call waits longer than this.
TIMEOUT = 300


class Refused(ValueError):
    """A verb refused, or a tool call is refused: nothing was sealed."""


class Ctl:
    """One node's capsulectl-deal profile."""

    def __init__(self, home: Path, profile: str, binary: str | None = None) -> None:
        self.home, self.profile = Path(home), profile
        self.binary = binary or os.environ.get("GOOSE_CAPSULECTL_DEAL", "capsulectl-deal")

    def run(self, *args: str, stdin: bytes | None = None) -> Any:
        """Run ``capsulectl-deal --profile PROFILE ARGS...`` and return the JSON it prints (an object, or the array of ``deal list --json``).
        A refusal (a non-zero exit) raises :class:`Refused` with the verb's own words."""
        env = {**os.environ, "HOME": str(self.home), "XDG_CONFIG_HOME": str(self.home / ".config")}
        try:
            done = subprocess.run([self.binary, "--profile", self.profile, *args], input=stdin, env=env,
                                  capture_output=True, timeout=TIMEOUT, check=False)
        except FileNotFoundError:
            raise Refused(f"{self.binary} is not installed: install the capsulectl-deal plugin "
                          "(`capsulectl plugin install`) or set GOOSE_CAPSULECTL_DEAL") from None
        except subprocess.TimeoutExpired:
            raise Refused(f"capsulectl-deal {args[0]} did not finish in {TIMEOUT}s") from None
        text = done.stdout.decode("utf-8", "replace").strip()
        try:
            out = json.loads(text) if text else {}
        except ValueError:
            out = {}
        if done.returncode != 0:
            why = out.get("error") if isinstance(out, dict) else None
            raise Refused(why or done.stderr.decode("utf-8", "replace").strip() or f"capsulectl-deal {args[0]} failed")
        return out


def engine(*args: str, stdin: bytes | None = None) -> dict[str, Any]:
    """Run ``capsule-engine ARGS...`` and return the JSON object it prints."""
    binary = os.environ.get("GOOSE_CAPSULE_ENGINE", "capsule-engine")
    try:
        done = subprocess.run([binary, *args], input=stdin, capture_output=True, timeout=TIMEOUT, check=False)
    except FileNotFoundError:
        raise Refused(f"{binary} is not installed: a deal's admission decision is capsule-engine's "
                      "(set GOOSE_CAPSULE_ENGINE)") from None
    if done.returncode != 0:
        raise Refused(done.stderr.decode("utf-8", "replace").strip() or "capsule-engine failed")
    return json.loads(done.stdout)
