<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Added: `deal ruling entries`, and `deal receive` of the other side's copy (x-deal-v1, #773)

- `deal ruling entries --deal ID --ruling DIGEST --copy FILE [--copy FILE] [--proof FILE ...]` (seals nothing): the adjudicator sees, from each party's copy (`deal report --share adjudicator --bundle`), whether that party entered its ruling in its deal book. The answer is one of:
  - "entered";
  - "entered, not witnessed";
  - "ruling not entered by the <party> as of its checkpoint at <time>";
  - "not shown to the adjudicator".

  A party shows as not having entered only by a checkpoint after the ruling's witnessed time. The ruling's witnessed time is the time a witness the agreement names gave the adjudicator's stamp; with none, a copy without the entry shows nothing. The ruling's own time shows as "sent <time> (adjudicator's statement)", never as proof of delivery. `--proof` takes a party's stamp proof file with its witness receipts. It never says a party refused.
- `deal receive` takes the other side's copy (`deal report --share counterparty --bundle`): it takes each record the copy carries for the rows, rulings included, so a ruling the other side received reaches you too, and a judge that gave each side another ruling shows as a fork.
