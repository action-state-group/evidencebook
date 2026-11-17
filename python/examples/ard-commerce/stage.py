#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""The stage the three Goose windows share: one private relay and one catalogue server on localhost
(see :mod:`parties`). Usually started by the first party that needs it; ``python stage.py <demo home>``
runs it by hand. It keeps no keys and no records: only the relay's events and the files under
``<demo home>/stage``."""
from __future__ import annotations

import sys
import time
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

from parties import Stage  # noqa: E402


def main(argv: list[str]) -> int:
    home = Path(argv[1] if len(argv) > 1 else "~/.config/ard-commerce-demo").expanduser()
    stage = Stage(home)
    print(f"stage: catalogue {stage.url}, relay ws://127.0.0.1:{stage.relay.url.rsplit(':', 1)[1]}", flush=True)
    try:
        stage.serve_forever()
    except KeyboardInterrupt:
        pass
    time.sleep(0)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
