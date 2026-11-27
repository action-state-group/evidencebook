# A dispute

Part of the [deal skill](../SKILL.md): its [Rules](../SKILL.md#rules-never-break-these) and [Points of no return](../SKILL.md#points-of-no-return) bind every step here, and `ID`, `OFFER` and `--profile deal` are as its [Procedures](../SKILL.md#procedures) say.

When a row did not go as agreed and the agreement names a referee:

```sh
capsulectl --profile deal deal dispute --deal ID --row ROW --out dispute.json
```

`--forum` picks one of the agreement's forums (it may be left out when there
is only one); `--withdraw` takes back your open dispute on the row. The user
sends the file to the other side. What goes to the referee is the user's
to make, not yours.

The ruling is the referee's own record, which reaches you as a file. Keep it,
and write the statement that enters it in the user's book:

```sh
capsulectl --profile deal deal receive ruling.json --out naming.json
```

The user sends `naming.json` to the other side, before anything later they
send: the other side counts none of the user's later records until it holds
it. The output says when the ruling was stamped (`stamped_at`). "unknown"
means the file carries no receipt from a witness the agreement names. The
ruling still counts. A referee whose key is a party's is still flagged, but a
shared operator through a controller binding cannot be checked until a receipt
arrives.
When it says `"independence": "not independent (shared operator)"`, tell the
user that line as it is: the referee's key is a party's, or bound to one.
Never say a referee is independent. A controller binding the user holds, such
as one the referee's operator publishes, is checked with `--binding FILE`.

A ruling read from the parties' answers also says what the referee read each
side's book from (`sources`). Tell the user each source it lists as not shown,
as it is: a side that did not answer is not said to have refused, and a ruling
against it rests only on records it signed. A `fork` line means that side
signed two different records at one place. A request from the referee for the
deal is the user's to answer, never yours (`deal respond --help` says how).

To challenge a ruling the user holds, by the digest `deal receive` printed:

```sh
capsulectl --profile deal deal dispute --deal ID --challenge RULING --grounds grounds.txt --out challenge.json
```

The grounds stay with the user; only their SHA-256 is sealed. A challenge
changes no row's status. The user sends the file like a dispute.

The other side's copy (`deal report --share counterparty --bundle`), when it
arrives, is kept with `deal receive copy.json`. Rulings it carries reach the
user too, so a referee who gave each side a different ruling shows as two
records for one place.
