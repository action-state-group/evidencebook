# SPDX-License-Identifier: Apache-2.0
"""``body_evidence.preimage`` (evidencebook#260), as Go's
``dealprofile/preimage.go``."""
from __future__ import annotations

import hashlib
import re

__all__ = ["preimage_matches"]

_HEX64 = re.compile(r"[0-9a-f]{64}\Z")


def preimage_matches(preimage: str, payment_hash: str) -> bool:
    """Whether ``preimage``, the body_evidence preimage of a Lightning payment
    the user received, is the preimage of ``payment_hash``, the step's
    settlement payment_ref value (``ln.payment_hash``): SHA-256 over the 32
    octets the hex names. Both are 64 lowercase hex characters, or it is
    False."""
    if not _HEX64.match(preimage) or not _HEX64.match(payment_hash):
        return False
    return hashlib.sha256(bytes.fromhex(preimage)).hexdigest() == payment_hash
