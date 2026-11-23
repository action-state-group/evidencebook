<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Added: `deal ruling entries`, and `deal receive` of the other side's copy (x-deal-v1, #773)

- `deal ruling entries --deal ID --ruling DIGEST --copy FILE [--copy FILE] [--proof FILE ...]` (seals nothing): the adjudicator sees, from each party's copy (`deal report --share adjudicator --bundle`), whether that party entered its ruling in its deal book. The answer is one of:
  - "entered";
  - "entered, not witnessed";
  - "ruling not entered by the <party> as of its checkpoint at <time>";
  - "not shown entered".

  A party shows as not having entered only by a checkpoint after the ruling's witnessed time. The ruling's witnessed time is the time a witness the agreement names gave the adjudicator's stamp; with none, a copy without the entry shows nothing. The ruling's own time shows as "sent <time> (adjudicator's statement)", never as proof of delivery. `--proof` takes a party's stamp proof file with its witness receipts. It never says a party refused.
- `deal receive` takes the other side's copy (`deal report --share counterparty --bundle`): it takes each record the copy carries for the rows, rulings included, so a ruling the other side received reaches you too, and an adjudicator that gave each side another ruling shows as a fork.
- The share gate no longer refuses an x-deal-v1 copy for a random identifier that happens to look like a card number (#787). It takes the deal's own ids out first: the deal id, and the shelf log ids the copy's agreements name. It does so in the copy's text and in the record payloads the copy carries as base64. A log id whose last segment is 32 hex characters (`deal/…`, `case/…`) is an id too. A card number written in a copy is still refused.
