"""Decide capsulectl's platform-share check inputs under capsule-engine's packs.

capsule-cli's CI has no engine, so this end-to-end run is by hand, at each
rules-checker pin bump (capsule-cli#348). The engine venv is a capsule-engine
checkout installed with its own dependencies (``pip install -e
<checkout>/python``), so the store packages are the revisions its
pyproject pins; each decision runs over a fresh store directory
(``open_store``: the CLL entry log plus capsule-emit's artifact store):

    mkdir -p /tmp/e2e
    E2E_OUT=/tmp/e2e go test ./internal/deal/ -run TestCapturePlatformCheckInputsForTheEngine
    <engine venv>/bin/python scripts/engine-e2e/decide_platform.py <capsule-engine checkout>/python /tmp/e2e

Expected under everyday 0.3.9 and seller 0.1.6 (capsule-engine c770138): email
to a platform allow; phone or home address deny (r03, s01). capsulectl's own
first-telling pause makes the first email ask.
"""

import json
import sys
import tempfile
from pathlib import Path


def main(root: Path, inputs: Path) -> None:
    sys.path.insert(0, str(root))
    from capsule_engine.guards import LocalSigner
    from capsule_engine.guards.history import open_store
    from capsule_engine.packs import build_engine, install_pack, load_pack_dir
    from capsule_engine.packs.obligation_results import obligation_results
    from capsule_engine.report.replay import action_for_check_input

    for pack_name in ("everyday", "seller"):
        pack = load_pack_dir(root / "capsule_engine" / "packs" / "catalog" / pack_name)
        print(pack.pack_id, pack.definition_digest())
        for cls in ("email", "phone", "address"):
            given = json.loads((inputs / f"{pack_name}-{cls}.input.json").read_text(encoding="utf-8"))
            with tempfile.TemporaryDirectory() as tmp:
                installed = install_pack(pack, project_dir=Path(tmp) / "project", mode="observe")
                signer = LocalSigner(key_id="e2e", secret=b"e2e-fixed-key")
                with open_store(Path(tmp) / "store", signer=signer, create=True) as store:
                    engine = build_engine(installed, history=store, signer_provider=lambda: signer)
                    action = action_for_check_input(given["record"], item_ref=given.get("item_ref"))
                    decision = engine.check(
                        action,
                        dry_run=True,
                        task_authority_record=given.get("task_authority_record"),
                        commercial_bounds_opening=given.get("commercial_bounds_opening"),
                    )
            failed = {r.obligation_id: r.result for r in obligation_results(installed.pack, decision.constraints)
                      if r.result not in ("pass", "n/a")}
            print(f"  {cls}: recipient_role={action.recipient_role} classes={action.disclosed_classes}"
                  f" -> {decision.outcome} {failed}")


if __name__ == "__main__":
    main(Path(sys.argv[1]), Path(sys.argv[2]))
