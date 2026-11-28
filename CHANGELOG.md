# Changelog

## Unreleased

## v0.27.0 (2026-11-26)

Every pull request merged since v0.26.0:

- [#610](https://github.com/action-state-group/evidencebook/pull/610): feat(capsulectl): build, test and release capsulectl-deal from this module
- [#660](https://github.com/action-state-group/evidencebook/pull/660): fix(capsulectl): round 1 review of the deal move
- [#688](https://github.com/action-state-group/evidencebook/pull/688): feat(deal): deal admit seals capsule-engine's admission decision for an x-deal-v1 deal (#626)
- [#690](https://github.com/action-state-group/evidencebook/pull/690): feat: Cadence.HistoryCard2, the shelf's history_card/2 staple (u539) (#626)
- [#687](https://github.com/action-state-group/evidencebook/pull/687): feat(capsulectl): deal node append and records, a node's own book on the deal shelf (#624)
- [#694](https://github.com/action-state-group/evidencebook/pull/694): feat(capsulectl): deal init --signing-key-file and --checkpoint-key-file keep an existing key (#624)
- [#696](https://github.com/action-state-group/evidencebook/pull/696): feat(capsulectl): deal node seal-model-call, a model call as an effect-boundary record of a node book (#624)
- [#695](https://github.com/action-state-group/evidencebook/pull/695): feat(deal): the staple builds history_card/2 only (u539) (#626)
- [#699](https://github.com/action-state-group/evidencebook/pull/699): docs(design): check and note on an x-deal-v1 deal, proposed (#626)
- [#698](https://github.com/action-state-group/evidencebook/pull/698): feat(capsulectl): deal node referee receive, a referee's Result checked and sealed into a node book (#624)
- [#700](https://github.com/action-state-group/evidencebook/pull/700): feat(capsulectl): deal node request, respond and reply-check, the evidence-request exchange on a node book (#624)
- [#701](https://github.com/action-state-group/evidencebook/pull/701): feat(deal): deal note on an x-deal-v1 deal, over a local plain event list (#626)
- [#702](https://github.com/action-state-group/evidencebook/pull/702): feat(capsulectl): deal node checkpoint and bundle, a node book's checkpoint and Evidence Bundle (#624)
- [#705](https://github.com/action-state-group/evidencebook/pull/705): fix(capsulectl): card register syncs the statement file before linking it to --out (#697)
- [#706](https://github.com/action-state-group/evidencebook/pull/706): docs(capsulectl): card register sync note in the test, changelog no longer claims the directory is synced (#697)
- [#707](https://github.com/action-state-group/evidencebook/pull/707): feat(deal): deal check and approval on an x-deal-v1 deal (#626)
- [#711](https://github.com/action-state-group/evidencebook/pull/711): feat(dealprofile): CheckActs holds a settled amount to the checked one (#626)
- [#712](https://github.com/action-state-group/evidencebook/pull/712): docs(capsulectl): write-path §13, deal perform, settle and statement on a v1 deal (#626)
- [#715](https://github.com/action-state-group/evidencebook/pull/715): feat(deal): deal perform and deal statement on an x-deal-v1 deal (#626)
- [#720](https://github.com/action-state-group/evidencebook/pull/720): ci(capsulectl): split the race tests across ten jobs (#717)
- [#717](https://github.com/action-state-group/evidencebook/pull/717): feat(dealprofile): export xdealv1.ChangesDetails (#626)
- [#721](https://github.com/action-state-group/evidencebook/pull/721): feat(deal): deal settle on an x-deal-v1 deal (#626)
- [#725](https://github.com/action-state-group/evidencebook/pull/725): feat(dealprofile): StatusProvider.ReportSnapshot, the report with its payment checks from one read (#626)
- [#726](https://github.com/action-state-group/evidencebook/pull/726): feat: BundleRequest.DisclosureRecordedElsewhere, a bundle that leaves the book unchanged (#626)
- [#723](https://github.com/action-state-group/evidencebook/pull/723): feat(deal): deal settle --sent asks the check again before money leaves (#718)
- [#727](https://github.com/action-state-group/evidencebook/pull/727): feat(deal): deal report on an x-deal-v1 deal, your own copy (#626)
- [#731](https://github.com/action-state-group/evidencebook/pull/731): feat(deal): shared copies of an x-deal-v1 report, on record before they are written (#626)
- [#733](https://github.com/action-state-group/evidencebook/pull/733): feat(deal): deal list shows x-deal-v1 deals (#626)
- [#734](https://github.com/action-state-group/evidencebook/pull/734): feat(deal): deal report --bundle on an x-deal-v1 deal (#626)
- [#736](https://github.com/action-state-group/evidencebook/pull/736): feat(deal): deal settlement on an x-deal-v1 deal (#626)
- [#738](https://github.com/action-state-group/evidencebook/pull/738): test(deal): goldens of capsule-cli rc47's verify --bundle on deal copies (#735)
- [#741](https://github.com/action-state-group/evidencebook/pull/741): test(report): pass --use-mock-keychain --password-store=basic to headless Chrome
- [#742](https://github.com/action-state-group/evidencebook/pull/742): fix(xdealv1): hold a refund to its cancel check's cancelled_amount_minor (#719)
- [#740](https://github.com/action-state-group/evidencebook/pull/740): build(capsulectl,report): require capsule-cli rc48 and capsule-viewer go v0.20.0 (#739)
- [#745](https://github.com/action-state-group/evidencebook/pull/745): feat(deal): deal offer decline on an x-deal-v1 offer (#626)
- [#748](https://github.com/action-state-group/evidencebook/pull/748): feat: serve a Terms Pack by the digest of its definition and report over the answer (#606)
- [#752](https://github.com/action-state-group/evidencebook/pull/752): fix(deal): deal settle and deal perform take the check id deal check prints (#626)
- [#757](https://github.com/action-state-group/evidencebook/pull/757): fix(dealprofile): the x-deal-v1 payment check runs on the payment's own legs beside a refund (#722)
- [#756](https://github.com/action-state-group/evidencebook/pull/756): feat(rust): serve a Terms Pack by the digest of its definition, with grants, nested fields and set proofs (#746)
- [#754](https://github.com/action-state-group/evidencebook/pull/754): feat(capsulectl): serve a Terms Pack by digest and report over the answer (#753)
- [#761](https://github.com/action-state-group/evidencebook/pull/761): test(deal): the unpriced-card test ignores the deal id when it looks for the amount (#730)
- [#768](https://github.com/action-state-group/evidencebook/pull/768): feat(report): carry deal receipts into a settlement report (#764)
- [#769](https://github.com/action-state-group/evidencebook/pull/769): feat(report): Page.IgnoredReceipts lists the receipts no deal cites (#764)
- [#765](https://github.com/action-state-group/evidencebook/pull/765): build(capsulectl,report): move capsulectl to capsule-cli rc49, capsule-viewer go v0.22.0 and the library at ae9169d4; report to rc49
- [#763](https://github.com/action-state-group/evidencebook/pull/763): chore(release): crates/evidencebook 0.0.26
- [#770](https://github.com/action-state-group/evidencebook/pull/770): feat(capsulectl): book report build --receipts carries deal receipts into a settlement report (#764, #767)
- [#776](https://github.com/action-state-group/evidencebook/pull/776): test(deal): the receipt page check takes the deal-format verdicts from the plugin (#735)
- [#749](https://github.com/action-state-group/evidencebook/pull/749): feat(deal): x-deal-v1 is the default deal version (#626)
- [#657](https://github.com/action-state-group/evidencebook/pull/657): docs(dealprofile): x-deal-v1 is the default, x-deal-v0 is frozen (#626)
- [#760](https://github.com/action-state-group/evidencebook/pull/760): fix(deal): deal audit-host reads x-deal-v1 deals (#758)
- [#766](https://github.com/action-state-group/evidencebook/pull/766): feat(deal): agree to your own x-deal-v1 offer with a party that keeps no records (#762)
- [#784](https://github.com/action-state-group/evidencebook/pull/784): feat(report): Receipt.NotClosed marks a deal that had not closed when the page was built (#778)
- [#786](https://github.com/action-state-group/evidencebook/pull/786): feat(capsulectl): book report build --receipts takes not_closed (#778)
- [#789](https://github.com/action-state-group/evidencebook/pull/789): ci(capsulectl): run on report/testdata/presentation changes (#778)
- [#772](https://github.com/action-state-group/evidencebook/pull/772): feat(deal): a card checkout's payment on x-deal-v1, under the merchant's order number (#771)
- [#793](https://github.com/action-state-group/evidencebook/pull/793): fix(dealprofile): a date in a check's text is not a phone number (#791)
- [#777](https://github.com/action-state-group/evidencebook/pull/777): feat(xdealv1): checkpoint the adjudicator's case book, one per deal (#615)
- [#781](https://github.com/action-state-group/evidencebook/pull/781): feat(xdealv1): CheckCase, CheckCaseRecord and SealRuling, an adjudicator's case rules (#779)
- [#783](https://github.com/action-state-group/evidencebook/pull/783): feat(xdealv1): SharedOperator marks a ruling "not independent (shared operator)" from a controller binding (#780)
- [#785](https://github.com/action-state-group/evidencebook/pull/785): feat(xdealv1): EntryOf, the entry check over a party's copy (#773, #779)
- [#792](https://github.com/action-state-group/evidencebook/pull/792): fix(deal): on x-deal-v1, an answer that was not applied authorizes nothing (#790)
- [#796](https://github.com/action-state-group/evidencebook/pull/796): feat(deal): deal ruling, the adjudicator's verb, and deal dispute --challenge (#773)
- [#759](https://github.com/action-state-group/evidencebook/pull/759): feat(deal): the deal skill teaches x-deal-v1; the x-deal-v0 procedures move to a legacy file (#626)
- [#800](https://github.com/action-state-group/evidencebook/pull/800): feat: window books, a shelf split into one book per time window (#689)
- [#801](https://github.com/action-state-group/evidencebook/pull/801): feat(deal): deal ruling entries, deal receive of a copy, and share gate fixes (#773, #787)
- [#795](https://github.com/action-state-group/evidencebook/pull/795): feat: Cadence.Receipts, the receipts a shelf holds for a checkpoint (#788)
- [#804](https://github.com/action-state-group/evidencebook/pull/804): docs(xdealv1): RulingAt comes from Shelf.RecordTimeByKey (#803)
- [#806](https://github.com/action-state-group/evidencebook/pull/806): feat(report): View.Coverage states the range a report over time windows covers (#689)
- [#813](https://github.com/action-state-group/evidencebook/pull/813): feat(report): Receipts.Gate, the caller's own check of each carried receipt (#735)
- [#810](https://github.com/action-state-group/evidencebook/pull/810): feat(deal): deal checkpoint proof, a deal book's stamp proof with its witness receipts (#788)
- [#809](https://github.com/action-state-group/evidencebook/pull/809): feat(deal): capsulectl-deal reads its own deal formats on capsule-cli rc50 (#735)
- [#816](https://github.com/action-state-group/evidencebook/pull/816): build(capsulectl,report): release capsulectl v0.1.0-rc50; report to capsule-cli rc50 (#802)
- [#811](https://github.com/action-state-group/evidencebook/pull/811): feat(capsulectl): book stores split by time window, and reports over a range of windows (#689)
- [#815](https://github.com/action-state-group/evidencebook/pull/815): fix(deal): an adjudicator's profile is made with one key; a store keyed otherwise is refused before sealing (#814)
- [#817](https://github.com/action-state-group/evidencebook/pull/817): fix(deal): the share gate reads a card number by its network prefix and the checksum (#787)
- [#820](https://github.com/action-state-group/evidencebook/pull/820): feat: VerifyWitnessReceiptTime, a verified receipt's registration time (#819)
- [#824](https://github.com/action-state-group/evidencebook/pull/824): fix(xdealv1): SharedOperator labels a party's own key with no stamped time (#822)
- [#823](https://github.com/action-state-group/evidencebook/pull/823): feat(deal): a receipt says when the checkpoints stopped (capsule-cli#600)
- [#821](https://github.com/action-state-group/evidencebook/pull/821): feat(capsulectl): a new book store is split by month windows (#805)
- [#822](https://github.com/action-state-group/evidencebook/pull/822): feat(deal): a received ruling's stamped time and 'not independent (shared operator)' (#819)
- [#825](https://github.com/action-state-group/evidencebook/pull/825): feat: a window's grace, for a Close over an earlier window (#807)
- [#829](https://github.com/action-state-group/evidencebook/pull/829): fix: a refused window policy leaves no shelf file (#827)
- [#832](https://github.com/action-state-group/evidencebook/pull/832): feat: a bundle recorded elsewhere hands back its disclosure entry (#807)
- [#831](https://github.com/action-state-group/evidencebook/pull/831): docs(deal): the skill teaches receiving a ruling and challenging it
- [#826](https://github.com/action-state-group/evidencebook/pull/826): feat(deal): capsulectl-deal emits parent-log-witness/v1 in place of x-cadence-witness/v0 (#737)
- [#828](https://github.com/action-state-group/evidencebook/pull/828): feat(capsulectl): a window's grace in capsulectl-book (#807)
- [#840](https://github.com/action-state-group/evidencebook/pull/840): build(capsulectl): pin capsule-cli v0.1.0-rc52; a pack lacking keys pins with a notice (capsule-cli#627)
- [#836](https://github.com/action-state-group/evidencebook/pull/836): feat: a JSONL book continues in a windowed store, root package (#808)
- [#837](https://github.com/action-state-group/evidencebook/pull/837): feat(capsulectl): book window convert, a JSONL book continues in a store split by window (#808)
- [#841](https://github.com/action-state-group/evidencebook/pull/841): feat(deal): a buyer's deal seals the task authority the user confirmed (#839)
- [#846](https://github.com/action-state-group/evidencebook/pull/846): feat: a shelf whose windows are its journal, with deal books placed in them (#797)
- [#847](https://github.com/action-state-group/evidencebook/pull/847): feat(capsulectl): a deal store whose journal is split by window (#797)
- [#848](https://github.com/action-state-group/evidencebook/pull/848): feat(report): the coverage banner names each placed deal and states the report's limit (#797)
- [#849](https://github.com/action-state-group/evidencebook/pull/849): fix: a placed deal's coverage line names the deal book once; packfile doc comments (#797)
- [#851](https://github.com/action-state-group/evidencebook/pull/851): build(capsulectl): pin capsule-cli v0.1.0-rc53 and report v0.8.0 (evidencebook-skills#248)
- [#853](https://github.com/action-state-group/evidencebook/pull/853): test(capsulectl): a windowed deal store fixture with ended months, and the CLI path over it (#852)
- [#855](https://github.com/action-state-group/evidencebook/pull/855): feat(python): the x-deal-v1 holder's case book rules (#775)
- [#854](https://github.com/action-state-group/evidencebook/pull/854): feat(rust): register a Standard History card on a SCITT service and check card_registered (#618)
- [#859](https://github.com/action-state-group/evidencebook/pull/859): feat(python): an adjudicator's case rules and the witness receipt check (#775)
- [#856](https://github.com/action-state-group/evidencebook/pull/856): feat(rust): the x-deal-v1 case book rules (case log id, proof binding, adjudicator receive) (#774)
- [#864](https://github.com/action-state-group/evidencebook/pull/864): docs(capsulectl): witness vs countersign for a deal, and the default witness stated once (#862)
- [#868](https://github.com/action-state-group/evidencebook/pull/868): docs: unpin the Go install lines so they cannot go stale
- [#873](https://github.com/action-state-group/evidencebook/pull/873): fix(capsulectl): deal compose seals only a refusal's signed members (#872)
- [#874](https://github.com/action-state-group/evidencebook/pull/874): docs(design): what an on-path party can do with witness_receipts (#872)
- [#870](https://github.com/action-state-group/evidencebook/pull/870): feat(report)!: open settlement receipts with capsule-viewer's receipt modules (#869)
- [#863](https://github.com/action-state-group/evidencebook/pull/863): docs(design): the adjudicator gathers four books, and where it runs (#861)
- [#879](https://github.com/action-state-group/evidencebook/pull/879): refactor(rust): cut checkpoints with checkpointed-local-log's cut_checkpoint
- [#833](https://github.com/action-state-group/evidencebook/pull/833): feat(goose): the Goose loop reference node runs on capsulectl-deal, its deals on x-deal-v1 (#624)
- [#877](https://github.com/action-state-group/evidencebook/pull/877): feat(deal): the adjudicator gathers four books (#861)
- [#889](https://github.com/action-state-group/evidencebook/pull/889): fix(goose): the node's offers carry terms.price_minor, so its deals have a value band on the card (#888)
- [#881](https://github.com/action-state-group/evidencebook/pull/881): feat(capsulectl): countersign an x-deal-v1 deal (#865)
- [#885](https://github.com/action-state-group/evidencebook/pull/885): feat(deal): the other party's half for an x-deal-v1 keep copy (#850)
- [#900](https://github.com/action-state-group/evidencebook/pull/900): docs(dealprofile): agreeing to an adjudicator agrees to answer its full request (#898)
- [#886](https://github.com/action-state-group/evidencebook/pull/886): feat(capsulectl): your own x-deal-v1 copy carries the other party's half, and deal verify checks it (#850)
- [#902](https://github.com/action-state-group/evidencebook/pull/902): feat(xdealv1): agreed_at no later than the first witnessed time (agent-action-capsule#696)
- [#913](https://github.com/action-state-group/evidencebook/pull/913): docs(dealprofile): cite public sources, not internal ruling ids (#901)
- [#915](https://github.com/action-state-group/evidencebook/pull/915): test(deal): the slow remote checker test bounds the wait, not the check's own work (#905)
- [#916](https://github.com/action-state-group/evidencebook/pull/916): docs(design): cite rulings by date and public issue, not internal ids (#914)
- [#910](https://github.com/action-state-group/evidencebook/pull/910): feat(capsulectl): refuse a /3 or /4 card whose deal was agreed after it was witnessed (agent-action-capsule#696)
- [#896](https://github.com/action-state-group/evidencebook/pull/896): feat(deal): an x-deal-v1 verdict's received_history, in Go, Rust and Python (#666)
- [#911](https://github.com/action-state-group/evidencebook/pull/911): feat(deal): the Deal API's deal.open as a root-module package (#899)
- [#912](https://github.com/action-state-group/evidencebook/pull/912): docs(capsulectl): install a release of the two launchers, at the core's version (#895)
- [#897](https://github.com/action-state-group/evidencebook/pull/897): feat(deal): deal check --history, a verified History in the sealed rules check (#666)
- [#887](https://github.com/action-state-group/evidencebook/pull/887): fix(deal): an x-deal-v1 holder holds a counterparty agreement only in the other role (#884)
- [#908](https://github.com/action-state-group/evidencebook/pull/908): feat: the front door's record and report calls, Book.RecordAct and Book.EvaluatePack (#895)
- [#918](https://github.com/action-state-group/evidencebook/pull/918): refactor(capsulectl): the deal.open verbs call evidencebook's package deal (#899)
- [#927](https://github.com/action-state-group/evidencebook/pull/927): test(report): an Example of the front door's three calls, rendered and sealed (#895)
- [#893](https://github.com/action-state-group/evidencebook/pull/893): feat(capsulectl): answer an adjudicator in full and rule over four books (#861)
- [#924](https://github.com/action-state-group/evidencebook/pull/924): feat(deal): deal check --history reads a stapled history_card/2 (#666)
- [#931](https://github.com/action-state-group/evidencebook/pull/931): refactor(rust): one copy of the inclusion path and consistency proof parts, shared by the substrate and the shelf
- [#936](https://github.com/action-state-group/evidencebook/pull/936): docs(design): cite public records, not chat links, and check for them (#861)
- [#934](https://github.com/action-state-group/evidencebook/pull/934): feat: a sent request's record keeps the commitment held for its responder, in Go and Rust
- [#922](https://github.com/action-state-group/evidencebook/pull/922): build(capsulectl): require the evidencebook library at main cc061f0e (#895)
- [#926](https://github.com/action-state-group/evidencebook/pull/926): feat(capsulectl): one release carries the core and both plugins, at one version (#895)
- [#939](https://github.com/action-state-group/evidencebook/pull/939): fix(capsulectl): release-build.sh finds GNU tar or says what to install, before it builds (#895)
- [#892](https://github.com/action-state-group/evidencebook/pull/892): fix(deal): read the branch the holder named when the other side forks at its first record (#878)
- [#880](https://github.com/action-state-group/evidencebook/pull/880): feat(capsulectl): book report build --receipts opens receipts with capsule-viewer's modules; Mesh receipts carried (#869)
- [#935](https://github.com/action-state-group/evidencebook/pull/935): feat(capsulectl): deal report --html writes an x-deal-v1 deal's receipt page with the binary's own verify report
- [#921](https://github.com/action-state-group/evidencebook/pull/921): feat(goose): the node's signed settlement disclosure for loop step 06 (#920)
- [#933](https://github.com/action-state-group/evidencebook/pull/933): feat(capsulectl): deal tick --now stamps at once, whatever the schedule
- [#942](https://github.com/action-state-group/evidencebook/pull/942): feat(deal): refusals name the package's own calls, with Code and Args (#899)
- [#943](https://github.com/action-state-group/evidencebook/pull/943): feat(xdealv1): DecideCase reports whether a disputed row is decided by the disputed side's own word
- [#946](https://github.com/action-state-group/evidencebook/pull/946): test(capsulectl): fix main's red checks (#941): the refused edited-receipt page; a pin that names no anchor
- [#951](https://github.com/action-state-group/evidencebook/pull/951): fix(capsulectl): the x-deal-v1 page shows local times and one witness line (capsule-viewer#423, #424)
- [#932](https://github.com/action-state-group/evidencebook/pull/932): ci: pull requests run fast checks; heavy suites run on main and nightly
- [#949](https://github.com/action-state-group/evidencebook/pull/949): test: assert respond flood progress, not wall-clock latency (#938)
- [#945](https://github.com/action-state-group/evidencebook/pull/945): refactor(capsulectl): word package deal's coded refusals in the CLI's terms (#899)
- [#960](https://github.com/action-state-group/evidencebook/pull/960): feat(capsulectl): deal tick bounds the time it spends delivering pending checkpoints (#937)
- [#957](https://github.com/action-state-group/evidencebook/pull/957): feat(deal): a History card carries a cll witness's anchor receipt, and verify checks it (#891)
- [#959](https://github.com/action-state-group/evidencebook/pull/959): fix(canary): sign the witness read with the log's checkpoint key
- [#954](https://github.com/action-state-group/evidencebook/pull/954): fix(capsulectl): an x-deal-v1 payment counts as an earlier payment to its merchant; Everyday Rules limits tested on v1 (#890)
- [#964](https://github.com/action-state-group/evidencebook/pull/964): feat(deal): x-deal-v0 copies carry no witness chain (u225, #958)
- [#970](https://github.com/action-state-group/evidencebook/pull/970): refactor(deal): deal check --history reads a stapled history_card/2 only (#962)
- [#969](https://github.com/action-state-group/evidencebook/pull/969): fix(capsulectl): an x-deal-v0 history act with a broken link to its check marks the rules history incomplete (#955)
- [#965](https://github.com/action-state-group/evidencebook/pull/965): fix(deal): a witness that timed out reads as slow, never as a consent ask
- [#971](https://github.com/action-state-group/evidencebook/pull/971): build(deps): draw the receipt page with capsule-viewer 8fe749b (capsule-viewer#437)
- [#972](https://github.com/action-state-group/evidencebook/pull/972): build(deps): repin capsulectl to capsule-engine 4a490697 (Everyday 0.3.10) and capsule-cli 16ec5bc9 (DeliverWitnessBy)
- [#974](https://github.com/action-state-group/evidencebook/pull/974): build(deps): the receipt page names capsuleviewer.receipt.wording.en/v1 (capsule-viewer#438)
- [#975](https://github.com/action-state-group/evidencebook/pull/975): feat(deal): the deal-book acts as library calls (#899 phase 2a)
- [#952](https://github.com/action-state-group/evidencebook/pull/952): feat(capsulectl): deal ruling check reports whether a dispute is decided by the disputed side's own word; deal ruling --decided-by rules only on that

### Added: the adjudicator gathers four books (#861)

- **The full adjudicator copy.** `ReportCopy.Full` makes `StatusProvider.ReportWithBook` add the whole deal book (`records`) and the copy of the other side's book the party holds (`received`) to an adjudicator's copy. `VerifyFullCopy` checks it, and `VerifyReportBundle` applies the same checks to any copy that carries `records`. An adjudicator's request names it with the derivation `deal_adjudicator_copy/1` (`DerivationAdjudicatorCopy`).
- **One book's facts.** `Cadence.BookEvidence` hands over one book's facts with their stamp proofs, link openings and receipts, and no status opening. `VerifyBookEvidence` checks them and returns the record times they give, never earlier than the shelf's own. `HandoverTimes` also times a received record (`ReceivedRecordTime`), and `StampProofRecomputes` checks a stamp proof against a book's record ids.
- **Gathering a case.** `GatherCase` reads a dispute, the case-opening copies and up to two full answers into one case. Each side is read from its own book and from the copy the other side held. Where the two differ, the copy the other side held governs, and both are kept. No branch is read past a fork the held copy does not reach. Each record says what timed it (`CaseRecordTimes`).
- **The ruling's `sources`.** A ruling from a gathered case names, per side, the last record read from each source, or null for a source not shown, and the position of any fork. It is an OPTIONAL member, so a reader from before it refuses such a ruling. `SealRuling` refuses a gathered ruling against a side of which the case shows no record (none read, no source shown, no fork kept as evidence).
- **Fixed.** `SealRuling` now seals a ruling whose remedy has an amount. Before, it failed to canonicalize the number.

### Fixed: `deal audit-host` reads x-deal-v1 deals (#758)

- **The bug.** `deal audit-host`, the daily check of host actions, read deals only through the x-deal-v0 path. So every consequential action on an x-deal-v1 deal was listed as `unrecorded`, even when the deal held it.
- **The fix.** It now also reads each x-deal-v1 deal's own records, as stored and writing nothing. A payment from `deal settle --sent` on covers the host's pay record, matched by deal id, or by amount, currency, merchant and time, exactly as on x-deal-v0. A payment or refund whose send failed covers nothing, and neither does a refund received, which is the other side's act. Each deal in a store holding both versions covers its own actions.

### Changed: `book report coverage` prints each placed deal's line and the report's limit (#797)

- On a report over a deal store's windows, `book report coverage` names a deal placed in a window with no answer, or answered at a tree the chain does not prove, and prints each covered deal's line under `deals`: its answer covers its whole book, records after its window included. `limits` now ends with the report's limit (each answer proves its rows are in the holder's log; the rows' field values are the holder's word; no proof shows that no row was left out), in place of the line "No check shows that an answer holds every row of its window."

### Added: `BundleRequest.DisclosureRecordedElsewhere` (#626)

- A bundle built with `DisclosureRecordedElsewhere` (with `At`, a checkpoint the book already has) leaves the book unchanged: no disclosure record and no new checkpoint.
- It is for a host that records the disclosure outside the book, as an x-deal-v1 deal book must, since a disclosure record in it is not an x-deal-v1 record and every later report of the book would refuse it.
- Without `At` it is refused.

### Added: Bundle.DisclosureEntry, the disclosure record a host records elsewhere (#807)

- `Bundle.DisclosureEntry`: a bundle built with `DisclosureRecordedElsewhere` now hands back the disclosure record the book would have appended (naming the bundle's digest and anchor, citing every disclosed record; those records and the anchor are the source book's), so a host records the hand-over where it keeps it, such as the current window's book for a bundle of an earlier window in its grace (evidencebook#807).

### Added: `Cadence.Receipts` (#788)

- `Cadence.Receipts` gives the witness receipts a shelf holds for one of its signed cadence checkpoints. A party sends them beside its stamp proof; x-deal-v1's entry check reads them to see that a witness registered the party's checkpoint. A receipt carries no record content: only the witness's URL, its signed receipt, and the entry hash with its position in the witness's log (leaf index and tree size).

### Added: `book report build --receipts` takes `{"exchange_capsule_id", "not_closed": true}` for a deal that had not closed when the page was built ([#778](https://github.com/action-state-group/evidencebook/issues/778))

- A deal that had not closed (an invoiced exchange with no settlement or delivery recorded yet) has no receipt, and `held` would say its holder chose to keep one off the page. `not_closed` says the deal had not closed when the page was built; capsule-viewer draws "No receipt: this deal had not closed when this page was built." (capsule-viewer#358). Exactly one of `receipt`, `held` and `not_closed` per entry; a deal with no entry still reads "Receipt not carried with this page" (a page built wrong).
- `capsulectl/go.mod` requires `report` v0.6.0, the release with `Receipt.NotClosed` (evidencebook#784) and capsule-viewer go v0.23.0, whose page runtime draws the line.

### Added: `capsulectl-book book report build --receipts` carries deal receipts into a settlement report (#764)

- `book report build --receipts FILE` takes a JSON list of `{"exchange_capsule_id", "receipt": <the bundle the node served>}` or `{"exchange_capsule_id", "held": true}` (the holder's share policy keeps it off the page) and puts the deal view, the module that opens a deal's receipt, in the page with each receipt, so capsule-viewer's deal-line "Open receipt" control (capsule-viewer#351) has a receipt to open. `report.Build` maps each entry to the one deal whose claims cite its exchange capsule (the part of a claim id before `::`); a deal with no entry reads "Receipt not carried with this page", an id no deal cites is ignored and listed as `ignored_receipts` in the build output, and one the claims of two deals cite is refused, as is a bundle the deal view does not draw. That a bundle is the receipt of its capsule's deal is the caller's word until agent-action-capsule#674 and capsule-viewer#353. Nothing is carried without the flag, which cannot be used with `--permalink` or `--diagnostic`; a composed receipt is not carried yet.
- `capsulectl/go.mod` requires `report` v0.5.0, the release with `View.Receipts` and `Page.IgnoredReceipts` (evidencebook#768, #769), which also brings the x-deal-v1 refusal `book report build` lacked ([#767](https://github.com/action-state-group/evidencebook/issues/767)): an x-deal-v1 bundle is refused with the pointer to its v1 deal report, and a test holds it. capsule-viewer go v0.22.0 refreshes the page runtime.
- The deal view's files and module builder moved to `capsulectl/internal/deal/assets`, which `capsulectl-deal` and `capsulectl-book` both link; neither launcher links the other's commands or hooks, and the launcher boundary test now says so by exact package.

### Changed: `capsulectl book report build --receipts` opens receipts with capsule-viewer's receipt modules (#869)

- capsulectl-book requires report v0.9.0. A settlement report built with `--receipts` carries capsule-viewer's receipt modules (`capsuleviewer.receipt.unilateral/v0` and `.seller/v0`, from capsule-viewer's Go package `receipt`, capsule-viewer#383) instead of evidencebook's deal view. A receipt there reads as a shared copy, with the verifier's top line.
- A Mesh node's receipt can now be carried. It is a producer selection, which `capsulectl deal verify --bundle` calls INCOMPLETE because its interval coverage is withheld as `selection_not_checkable`. Each carried receipt's check admits that INCOMPLETE, and no other: every other claim must pass and the deal section must be read and pass. The report admits it on the page's own verdict (report v0.9.0). Before, every Mesh receipt was refused.
- capsulectl and report require capsule-viewer go v0.27.2. A receipt's top line says "Checked in this browser by …" when the page's own check ran, and the buyer's receipt shows no Amount card when no signed field carries an amount (capsule-viewer#391). A browser without Ed25519 draws no receipt, only the can't-check line (#392). The page checks a capsulectl-deal copy's standalone report itself, so those receipts draw, and says so ("This page checked it itself: …"); a copy whose report fails reads as not verified (capsule-viewer#398, #410). An x-deal-v1 copy, whose report a page can't yet check, draws only a line saying so and how to check it (capsule-viewer#405). The committed report pages, capsulectl's copies of them and their snapshots change in the viewer's version, commit and embedded runtime.

### Changed: `capsulectl-deal` moves into this repository (#610)

- The `capsulectl-deal` plugin (`capsulectl deal ...`: the deal verbs and a deal profile's SQLite store) moved here from evidencebook-deal with its commit history, to `capsulectl/cmd/capsulectl-deal`, `capsulectl/internal/deal`, `capsulectl/skills/deal` and `capsulectl/examples`. `capsulectl/` is now one module with two launchers, `capsulectl-book` and `capsulectl-deal`, built from one source tree, tested by one CI workflow (eight race shards and two plain shards of `capsulectl/scripts/test.sh`, the deal pages, portability, studio replay and presentation pass jobs, and the nightly full-case race run) and released together: one `capsulectl/vX.Y.Z` tag publishes both launchers, `deal-view-` and `deal-skill-` archives, and one `SHA256SUMS`, with the asset names evidencebook-deal used. Neither launcher links the other's package, so each keeps its own commands, hooks and wording, and `capsulectl` stays core-only. The deal changelog up to the move is `capsulectl/CHANGELOG-deal.md`; its issue and PR numbers are evidencebook-deal's.
- `capsulectl/go.mod` requires capsule-cli `v0.1.0-rc46` (was rc45): the first release that hands `SetDealV1ReportVerifier` the x-deal-v1 extension. The core-only `capsulectl verify` still reports the x-deal-v1 report rule `NOT_CHECKED`; `capsulectl-deal verify --bundle` runs it.
- A deal release's notes link is now this repository's `capsulectl/vX.Y.Z` release, and the release workflow refuses a version a shared copy's producer string cannot carry.

### Docs: installing a capsulectl-book and capsulectl-deal release (#895)

- capsulectl/README.md gains "Install a release": the one `capsulectl/$V` release at the core's version, which core pre-releases have none, the downloads, the checksum and attestation checks, the two archives unpacked beside the launchers, and `capsulectl plugin install`.

### Changed: one capsulectl release carries the core and both plugins (#895)

- A `capsulectl/vX` release is now the one capsulectl release a person installs. It carries the core `capsulectl-vX-{os-arch}`, which is capsule-cli's published build of that version carried unchanged, together with the `capsulectl-book` and `capsulectl-deal` launchers and their archives. It has one version and one `SHA256SUMS`. The launchers stay plugins that the core installs.
- `capsulectl/scripts/release-core-assets.sh` adds the core. It checks the core against its release's `SHA256SUMS` and its attestation, bound to capsule-cli's release workflow, the tag and the commit `go.mod` resolves. It then runs the core and installs both launchers with it. The release workflow runs the script after the plugins' attestation, and the `capsulectl` CI dry run runs it against the core `go.mod` requires.
- The release notes, `capsulectl/README.md` "Install a release" and `capsulectl/RELEASING.md` give one download set and two provenance checks: the core with `capsulectl-vX.sigstore.json` from capsule-cli, and the plugins with `capsulectl-book-vX.sigstore.json` from this repository. Releases made before this change carry no core.

### Changed: `capsulectl` requires the evidencebook library at cc061f0e (#895)

- `capsulectl/go.mod` requires the `evidencebook` library at cc061f0e (was 7ad15a2d), the main of 2026-11-25. Both launchers are then built against the x-deal-v1 receive role check (#887) and the front door's calls (#908). The old pin already carried the `deal` package (#911). The capsule-cli requirement is unchanged: it stays at main 0043a944, which carries v0.1.0-rc54 and `cli.StapleVerdict` (capsule-cli#638).

### Changed: `capsulectl` pins capsule-cli v0.1.0-rc52, and a wording pack that lacks keys is pinned with a notice (capsule-cli#627)

- `capsulectl/go.mod` requires capsule-cli `v0.1.0-rc52` (was rc51). `profile update --wording` no longer refuses a pack that lacks keys of the core's or of a plugin's built-in pack (an older plugin on a newer core): it pins the pack, prints a `wording_notice`, and the whole built-in pack is used until the pack is updated; `doctor --install-check` adds a `warnings` entry. A pack whose `card.terms.*` entries could hide a term is still refused (evidencebook#834).

### Changed: `capsulectl` pins capsule-cli v0.1.0-rc53 and report v0.8.0 (evidencebook-skills#248)

- `capsulectl/go.mod` requires capsule-cli `v0.1.0-rc53` (was rc52; capsule-viewer/go v0.23.1, so `presentation build` draws an `eu-ai-act-obligations-report/v2` report, capsule-cli#630) and `report` `v0.8.0` (was v0.7.0). The report module's own changes since v0.7.0 are the coverage banner that names each placed deal (#848), `capsulectl-deal` emitting `parent-log-witness/v1` (#826) and the rc50 pins (#816); the last two already shipped in capsulectl rc52. New in `capsulectl/v0.1.0-rc53` since rc52: the window-split deal store (#847), the buyer's confirmed task authority (#841) and `book window convert` (#837).
- `capsulectl/go.mod` also requires the `evidencebook` library at 8ddb2abe (was 3550482e), which carries a placed deal's coverage line and findings naming the deal book once (#849).

### Changed: capsulectl moves to capsule-cli v0.1.0-rc49, capsule-viewer go v0.22.0 and the library at ae9169d4; report to capsule-cli rc49 (#765)

- `capsulectl/go.mod` requires capsule-cli v0.1.0-rc49 (the release capsulectl/v0.1.0-rc49 pairs with), capsule-viewer go v0.22.0 (the receipts viewer `report/go.mod` took in #768) and the evidencebook library at ae9169d4. capsulectl rc49 ships the Terms Pack verbs of #754 (`book result derived-inputs`, `book report derive`, packs in `book/derivations`), which live in capsulectl's own module; the library bump brings the x-deal-v1 refund-leg payment check fix (#757), the derivation proofs test vectors (#756) and the receipts carried into a settlement report (#764).
- `report/go.mod` moves to capsule-cli rc49. capsulectl's copies of the two pages `book report build` writes (`capsulectl/testdata/presentation/4-monthly-outcome` and `5-monthly-compliance`: `page.html`, `snapshot.json`, `build-snapshot.json`) follow the report module's pages, which the presentation pass reads (it holds the viewer's version text).

### Released: capsulectl-book and capsulectl-deal `capsulectl/v0.1.0-rc50` (#802)

This release pairs with capsule-cli `v0.1.0-rc50` (`capsulectl/go.mod` requires it, with `report` v0.7.0 and capsule-viewer go v0.23.0; `report/go.mod` moves to capsule-cli rc50 too) and is cut from main at f31db8ca. capsule-cli rc50 removed the deal-format verify code from its core, so this is the first capsulectl built on it: capsulectl-deal carries its own copies (#809). x-deal-v1 is the default deal version (#749).

Every pull request merged since `capsulectl/v0.1.0-rc49` that changes capsulectl, `report` or the deal skill:

- [#749](https://github.com/action-state-group/evidencebook/pull/749): feat(deal): x-deal-v1 is the default deal version (#626)
- [#759](https://github.com/action-state-group/evidencebook/pull/759): feat(deal): the deal skill teaches x-deal-v1; the x-deal-v0 procedures move to a legacy file (#626)
- [#760](https://github.com/action-state-group/evidencebook/pull/760): fix(deal): deal audit-host reads x-deal-v1 deals (#758)
- [#766](https://github.com/action-state-group/evidencebook/pull/766): feat(deal): agree to your own x-deal-v1 offer with a party that keeps no records (#762)
- [#770](https://github.com/action-state-group/evidencebook/pull/770): feat(capsulectl): book report build --receipts carries deal receipts into a settlement report (#764, #767)
- [#772](https://github.com/action-state-group/evidencebook/pull/772): feat(deal): a card checkout's payment on x-deal-v1, under the merchant's order number (#771)
- [#776](https://github.com/action-state-group/evidencebook/pull/776): test(deal): the receipt page check takes the deal-format verdicts from the plugin (#735)
- [#784](https://github.com/action-state-group/evidencebook/pull/784): feat(report): Receipt.NotClosed marks a deal that had not closed when the page was built (#778)
- [#786](https://github.com/action-state-group/evidencebook/pull/786): feat(capsulectl): book report build --receipts takes not_closed (#778)
- [#792](https://github.com/action-state-group/evidencebook/pull/792): fix(deal): on x-deal-v1, an answer that was not applied authorizes nothing (#790)
- [#796](https://github.com/action-state-group/evidencebook/pull/796): feat(deal): deal ruling, the adjudicator's verb, and deal dispute --challenge (#773)
- [#801](https://github.com/action-state-group/evidencebook/pull/801): feat(deal): deal ruling entries, deal receive of a copy, and share gate fixes (#773, #787)
- [#806](https://github.com/action-state-group/evidencebook/pull/806): feat(report): View.Coverage states the range a report over time windows covers (#689)
- [#809](https://github.com/action-state-group/evidencebook/pull/809): feat(deal): capsulectl-deal reads its own deal formats on capsule-cli rc50 (#735)
- [#810](https://github.com/action-state-group/evidencebook/pull/810): feat(deal): deal checkpoint proof, a deal book's stamp proof with its witness receipts (#788)
- [#813](https://github.com/action-state-group/evidencebook/pull/813): feat(report): Receipts.Gate, the caller's own check of each carried receipt (#735)

### Changed: capsulectl and report move to capsule-cli v0.1.0-rc48 and capsule-viewer go v0.20.0 (#739)

- `capsulectl-book result build` seals a Result whose claims carry the optional member `reason` (AAC Evidence Result section 4.4; the vendored schema and vectors are capsule-cli#612's), and a Result carrying it round-trips through `disclose` and `verify --bundle`.
- The report page draws the reason: the runtime's generic Result page shows a claim's `reason` with the pack's `row.<ref>.reason.<code>` words, or the core words for the four core codes (capsule-viewer#345, #348). Only the `report` package's `View.ContractReport` draws that page today; `capsulectl-book report build` does not select it yet. The committed presentation pages in `report/testdata/presentation` carry the new runtime.
- `capsulectl` requires capsule-cli `v0.1.0-rc48` (was rc47) and capsule-viewer go `v0.20.0` (was v0.19.2); `report` requires capsule-cli `v0.1.0-rc48` (was rc45, whose Result schema refuses `reason`, so `report.Build` could not take such a Result) and capsule-viewer go `v0.20.0`.

### Fixed: `capsulectl/scripts/release-build.sh` checks for GNU tar before it builds (#895)

- The archives need GNU tar. The script now uses `TAR` when it is set, then `tar` if that is GNU tar, then `gtar`. If `TAR` names a tar that is not GNU tar, or neither `tar` nor `gtar` is GNU tar, it refuses before the build and says what to do (macOS: `brew install gnu-tar`, and unset `TAR` or set `TAR=gtar`). Before, macOS's tar failed at the first archive, after the launchers were built, unless `TAR=gtar` was set, which was mentioned only in a comment.

### Added: `capsulectl-book` serves a Terms Pack by digest and reports over the answer (#753, #606)

- `book respond` and `book doctor` read the packs the holder opted in to in the profile's `book/derivations/*.json` (one file per pack: `document`, `recipients`, `forms`, `fields`, `proofs`, read exactly; `proofs` is on unless `false`). A pack not listed stays refused `derivation_unsupported`; a bad file (or a member that is `null`) stops every answer and names itself, and `respond`'s help says so; two files holding one document must declare it alike. `doctor` lists each served digest, its grants, and a pack with no recipient or no proofs.
- `book result derived-inputs --response ID` prints what the holder answered as the book recorded it: `{response, derivation, anchor, proven, records: [{digest, leaf_index, fields}]}`. `proven` means each row's digest was checked in the holder's log at a signed checkpoint; the field values are the holder's word, and no proof shows that no row was left out.
- `book report derive --response ID --pack DIGEST --result FILE --out FILE [--allow-unproven]` seals the Evidence Result an evaluator produced over that answer and writes the Evidence Bundle with `contract-report/v1`, which `book report build` renders and `capsulectl verify --bundle` checks. The Result is the requester's; the holder signed its answer. `--result -` reads stdin. It refuses an existing `--out` before sealing, takes the conduct packs and the profile key as `close` does, and reports `result_record_id` when a failure comes after the seal.
- `capsulectl/go.mod` moves to the evidencebook library at the commit of #748.

### Added: `capsulectl-book` keeps a book store split by time window, and reports over a range of windows (#689)

- `capsulectl book window init --period day|week|month|quarter|year` (default month) creates a profile's book store as one book per window, instead of `store init`. Windows are calendar periods in UTC, and a store's window is fixed when it is created; the command prints `window: month (calendar months, UTC), fixed for this store`. `store init` still creates one JSONL book and leaves a windowed store as it is; an existing JSONL book is unchanged. On a windowed store every write lands in the current window, `respond` answers from the window a request pins (recording the answer in the current one), the profile's Terms Packs and disclosure policy apply in every window, `close` and `reconcile` take a period inside the current window, and `book migrate` is refused.
- `book window list`, `book window roll` (close ended windows so last period's final checkpoint exists without a new record), `book window chain --from --to --out` (the `window_chain/v1` a requester or auditor is handed) and `book window publish` (deliver each closed window's final checkpoint to the profile's witness; the core's `cll checkpoint status|publish` cover the profile's own log until capsule-cli#622).
- `book report derive --chain <file> --response <id> ...` seals one Result over a holder's answers, one per window, and writes the bundle only when its coverage has no gap; `book report coverage --bundle --holder-checkpoint-key` checks it and names every gap; `book report build --holder-checkpoint-key` states the covered range on the page and writes no page over a gap. Without witness receipts a chain shows continuity only as the holder signed it, and no check shows that an answer holds every row of its window.

### Added: `capsulectl book window convert`, a JSONL book continues in a store split by window (#808)

- `book window convert --period P [--grace G] --yes` closes a profile's JSONL book for good and creates a windowed store that continues it. The book stays at `book/log.jsonl` and its records never move. The first window starts at the first whole second after the close and is partial (`partial` in `book window list`). The notice states that the conversion is one-way, the same key continues, and it is not a clear. It refuses a reproduced book, an unmigrated `cll.jsonl`, a store already windowed, and a run without `--yes`. A rerun reports the conversion already made.
- After the conversion, `close` and `reconcile` over a period that begins before it are refused with a hint. The period in progress at the conversion cannot be closed afterwards in either book: close the last whole period before converting, or convert at a period boundary. A record of the old book that a verb names is refused with a hint.
- `book window chain` from the first window carries the book as the chain's predecessor (chain version 3). Where the profile names a witness, the book's final checkpoint is queued in `book/witness.json`, and `book window publish` delivers it. With no witness, the conversion says the predecessor is unwitnessed. The core owns the partial first window's log.

### Changed: a new `capsulectl-book` store is split by month windows (#805)

- `store init` on a book profile with no store now creates a windowed store with month windows (calendar months in UTC, fixed for good) and prints `capsulectl: window: month (calendar months, UTC), fixed for this store` on stderr. `book window init --period day|week|quarter|year` fixes another window instead, and `--period none` one JSONL book for the store's whole history. A reproduced scratch book (`SOURCE_DATE_EPOCH`) and a profile whose pre-book cll.jsonl still holds entries or a checkpoint stay JSONL, and store init says so; an existing store is unchanged.
- The book plugin declares a windowed store's window logs to the core (capsule-cli's `OwnsLogID`, capsule-cli#622), so, with capsule-cli v0.1.0-rc51 or later, `cll checkpoint status` and `cll checkpoint publish` work on the current window's checkpoints and `cll proof verify` on any window log's; `book window publish` delivers closed windows' final checkpoints.

### Added: a window's grace in capsulectl-book (#807)

- A windowed book store keeps a grace for a Close over an earlier window (evidencebook#807): `book window init --grace` fixes it with the window (whole days such as `7d`, or a duration such as `36h`, shorter than the period; default 7 days for month, quarter and year windows, 1 day for week, none for day), and `store init` and `book window init` print it (`window: month (calendar months, UTC), closes 7 days after it ends, fixed for this store`). `close` and `reconcile` over a period inside a window in its grace work in that window's book, and `close` names the window it sealed in; its `--bundle-out` there is anchored at that window's checkpoint, and its disclosure record goes in the current window, since a window in grace takes only Closes. `book window list` shows each window's `closes_at` and `in_grace`; `book window roll` and `publish` queue and deliver each window's boundary checkpoint to the witness with its final one; `book window chain` over a window in grace says when it closes.

### Fixed: `capsulectl book card register --out` syncs the statement file before linking it (#697)

- The statement file's contents are flushed to disk before it is linked to `--out`, so a crash or power loss after the link no longer leaves an empty or partial file at `--out`. The directory is not synced, so after a crash the file may still be missing.

### Changed: x-deal-v1 checkpoints the adjudicator's case book, one per deal (#615)

- x-deal-v1: the adjudicator's case book is one per deal and adjudicator, at a log id both parties compute (`xdealv1.CaseLogID`), and a holder refuses a ruling on any other log, so a ruling counts only from that log and a second, different ruling at one place is a fork the status reads (`conflicting_final_rulings`). A case book's stamp proof binds to the adjudicator key the agreement names, so the adjudicator checkpoints with that key; a forked case book makes no checkpoint claim. A ruling is admitted from the adjudicator whoever carries it, so a party can forward the ruling it received with its naming statement. An adjudicator holding a deal's case book admits the parties' deal-book records, to check each named its ruling. `xdealv1.HeldRuling` finds a held ruling by the record digest a statement names, and reports one not held as unavailable. PROFILE.md §4 "The case book", §5. Rust and Python have these rules too (#774, #775). evidencebook#615.

### Added: an adjudicator's case rules in `xdealv1` (#779)

- `xdealv1.CheckCase` reads a dispute and one or both parties' adjudicator copies as a case for one adjudicator. It returns the deal, the row, both sides' keys, what was read of each side's book, the agreement, the forum, the rules digest and the case log. It checks:
  - the dispute is signed by a party of the deal and names the adjudicator;
  - each copy is an adjudicator copy whose agreement names the adjudicator;
  - the copies are one per side, of one offer;
  - with two copies, each agreement names the other copy's signer, and both name the same adjudicator terms and forums;
  - with one copy, the other side's key is that agreement's `counterparty_key`;
  - the dispute is read against its own agreement when that side's copy is given, and otherwise its forum against the agreement's forums.
- `xdealv1.CheckCaseRecord` reads a later withdrawal or challenge of a case. A withdrawal must be of the case's row, and a challenge must name a ruling the case book holds.
- `xdealv1.SealRuling` seals a ruling once. The body must be the case's: its row, keys, read and rules digest. The same body again is the existing record. Once a row has a final ruling, it takes no other ruling (`ErrRulingConflict`): the adjudicator never replaces a final ruling, and a challenge of it records disagreement.
- capsulectl-deal's `deal ruling` and a hosted adjudicator call these functions, so both apply the same rules. Rust and Python: #774, #775.

### Added: x-deal-v1 reports whether a disputed row is decided by the disputed side's own word

- `xdealv1.DecideCase` reads a gathered case (`GatherCase`) for its row and reports one of two things. It reports `Decisive` when the side the dispute is raised against (the side that did not sign the dispute) has itself stated `not_met` on the row, in a record it signed, and has not stated otherwise since. Otherwise it reports `NeedsPerson`, with the reason. The decisive result names the deciding record (digest, record id, seq, record time and what timed it).
- Anything else needs a person:
  - no statement on the row, a later statement other than `not_met`, or a latest record stating two values on the row;
  - none of that side's records read;
  - a fork in its book, or the copy of its book the other side holds not read (GatherCase finds a fork against that copy);
  - a sealed ruling's `read` that does not reach that side's book.
- Free text, such as a perform record's `observed`, decides nothing, and neither does the other side's word. Given a sealed ruling's `read` for that side, the check reads only that far, so checking a sealed ruling again gives the answer it was sealed on.
- It only reports: the ruling is still sealed by `SealRuling`, and the ruling record does not carry the decision (no wire change).
- The rule is Ethan's (2026-11-25), with Sherlock2's tightenings (buzz 4ec88700 and 319f98a4).
- Rust and Python port it with `GatherCase` (#875, #876).
- `GatherCase` also reads, for each side, the records of that side the other side's opening copy carries. A record there that differs from a source at its seq, or two of them at one seq, is a fork like any other: it shows in `fork_at` and in the ruling's `sources`, and when no held copy settles it, the book read and the ruling's `read` stop before it. A record past the end of the book read is kept in the new `CaseSide.ShownPast`. Before this, a fork shown only in the other side's opening copy was missed (#944).

### Added: the other party's half in an x-deal-v1 keep copy (#850)

- `ForeignBundle` builds an Evidence Bundle of a book log the caller holds but does not own, from the records it received and the owner's own signed checkpoint, through the same producer a book's own bundle uses: every record up to the checkpoint is carried and only the root is disclosed.
- `xdealv1.HeldCounterpartyHalf`, `MakeCounterpartyHalf` and `VerifyCounterpartyHalf` build and check the bundle extension `x-deal-v1-counterparty-half`: the counterparty's agreement, as such a bundle of their deal book, with the stamp proof that covers it. It is the object the History card reads for "stamped by both", so a receipt made from the copy and the card read one rule. It shows their signed, stamped agreement, not that they confirmed a later record. PROFILE §10 says who may carry it (the holder's own copy only) and what a reader checks.

A date inside a check's text ("2 nights from 2026-12-10") is no longer refused as a raw phone number: a phone-shaped run that is only ISO dates set apart by white space passes the personal-data scan, and a real number beside a date, or a date glued to digits or phone punctuation, is still refused (evidencebook#791).

### Added

- Package `deal` seals a deal's acts and what each side says of them in the holder's deal book (#899): `Store.Perform` (deal.perform, authorized over the deal's journal and gated on the other side's stamp proof), `Store.Statement` (deal.statement), `Store.Settle` for a payee leg (deal.settle), `Store.Dispute`, `Store.WithdrawDispute` and `Store.Challenge` (deal.dispute) and `Store.Status` (deal.status). `Store.Authorize` and `Store.DealJournal` run x-deal-v1's authorization over a deal's journal records, and `Store.SettlementAt` reads a deal-book record's settlement member. A call whose deal book another write moved after it read the book is refused with `CodeRecordRetry`, and the same call made again decides again. Each call made again with the same input returns the record already sealed. `Config.ReceiveProfile` gives the first-act gate its profile, and `Open` creates the `deal_v1_events` and `deal_v1_sends` tables capsulectl-deal writes.
- `xdealv1.StatusProvider.Status` gives a deal book's status value now, without a stamp. `xdealv1.MerchantOrderType`, `xdealv1.NormalOrigin`, `xdealv1.CardLike` and `xdealv1.CardPrefix` give the profile's private payment reference type and its checks, which `Store.Settle` holds a `payment_ref` to.
- Seven more `deal.Code` refusals for these calls: `CodeNoAgreement`, `CodeDisputeOpen`, `CodeChallengeUnknownRuling`, `CodeSettleWhichRow`, `CodeSettlePayer`, `CodeSettleRefunded` and `CodeSettleNegative`.

### Added: `capsulectl deal admit`, sealing capsule-engine's admission decision for an x-deal-v1 deal (#626)

- `deal admit --deal ID --input FILE` takes the output of `capsule-engine admission evaluate --admit FILE` and seals its admit body, exactly as printed, in the profile's journal as an x-deal-v1 `admit` record. It then prints the record's digest. Before this, no command wrote the admit that `deal agree` must cite, so an x-deal-v1 deal could not be agreed outside the tests. The refusal follows the engine's `admits_deal` (an ASK, a NO, or a LIMIT whose tier does not hold the deal) and says to answer the asks in capsule-engine. A body that is not exactly the engine's is refused, and so is one that disagrees with the verdict, tier, rows or pack it came with. It cannot tell which counterparty or amount the decision was made for, so pass the output of the run on the deal's own counterparty and amount. Each deal has one admission decision: a second one is refused, and so is a deal already agreed (to decide again, start a new deal). A re-run with the same input prints the same digest. One engine output is sealed for one deal only, since its admit's nonce is drawn for one record. The record carries no deal id (x-deal-v1 PROFILE §3), so the plugin keeps the binding itself.
- `deal agree` cites the deal's own admission decision when `--admit` is left out. It refuses an `--admit` that `deal admit` sealed for another deal, or that is not the deal's own.

### Added: package `deal`, the Deal API's `deal.open` (#899)

- New package `github.com/action-state-group/evidencebook/deal` seals an x-deal-v1 deal's opening records on the holder's own shelf. A `Store` opened over a shelf and a handle on its SQLite file offers these calls:
  - `Offer`: an offer or a counter-offer, sent whole.
  - `CancelOffer` and `DeclineOffer`.
  - `Admit`: seals capsule-engine's admission decision, read by `ParseAdmission`, which refuses a decision an agreement cannot cite.
  - `Agree`: the agreement that opens the deal book, returned with its stamp proof.
  - `NoteReceived`: records what `Shelf.Receive` kept of the other side's records.
  - `SealInBook`: the one writer of deal-book records.
- The package also has the read calls `Deal`, `HeldOffer`, `OwnBook`, `NoLayer`, `Repair` and `OfferPair`, and `MarkWritten`. They are there for capsulectl-deal's other verbs and are not a stable API yet.
- Every call can be run again after a failure without sealing a record twice.
- The store keeps its state in the tables capsulectl-deal already uses (`deal_versions`, `deal_sent`, `deal_v1`, `deal_v1_offers`, `deal_v1_admits`), with the same schemas, so an existing deal store opens unchanged. capsulectl-deal moves onto these calls in a following change.
- `xdealv1.SealRuling` now appends through the same place-taking helper as the deal-book writer. Its behaviour is unchanged.

### Changed: a placed deal's coverage line and findings name the deal book once (#797)

- `DealCoverage.Line` reads "deal/…, placed in window 2026-09, is answered over its whole book, …", in place of "deal deal/…, placed in window …, answered over its whole book, …", which named the deal twice. The coverage and chain findings for a placed deal read the same way: "deal/… of window 2026-09 has no answer", in place of "deal deal/… of window 2026-09 has no answer", and likewise for two answers, an unproven tree and a deal whose tree does not extend its close.

### Added: a card checkout's payment on x-deal-v1, under the merchant's order number (#771)

- **`x-deal-v1.merchant_order`** is the profile's one private payment-ref type (x-deal-v1 PROFILE §5): the merchant's order number, trimmed, and the checkout's origin in normal form (scheme and host, lowercased, punycode). `deal settle --payment-ref-type x-deal-v1.merchant_order` takes `--payment-ref-origin ORIGIN`. No other `x-` type is accepted.
- **When the checkout shows the order number before you pay,** the pay check's `payment_ref` is that number, and it binds the payment as a Lightning hash does: a different `--payment-ref` is refused.
- **When it does not,** the pay check says `"payment_ref_after_payment": true` and carries no `payment_ref`; the order number is given with `deal settle --status settled`. `deal report` (`row_notes`, beside the report) and `deal settlement` (the row's `notes`) then say the payment reference was recorded after payment. A checkout that failed may leave the order number out.
- **No card data in a reference:** a `payment_ref` or order number that is a card number (13 to 19 digits, spaces and hyphens set aside, with a card network's prefix and the card checksum) is refused, at `deal check` and at `deal settle`. When the merchant's order number is what looks like a card, the payment is recorded with no reference: the pay check says `"payment_ref_after_payment": true`, `deal settle --status settled` takes `--no-payment-ref`, and the row says no payment reference was recorded.
- On a deal whose other side keeps no signed records, the payment row says the record is one-sided. When both sides' legs carry the merchant order, the payment check reads them `unjoined`, and the row says why.

### Added: a deal with a party that keeps no signed records of its own, on x-deal-v1 (#762)

- **An offer whose `to` is null** is to a party that runs no deal tool, such as a shop. It stays with you, and there is nothing to send. You agree to it yourself with `deal agree`, at once, once your admission decision and intent are sealed.
  - The agreement says the other side runs no layer: `counterparty_layer: false`, with no counterparty key, and `seen` null on every record of your book (x-deal-v1 PROFILE §4.7).
  - Your first payment or act goes ahead without the other side's stamp proof, and is marked so.
  - The deal reads as any other: settlement (the payment check says why it cannot run: there is no key for the other side), report, bundle, `deal list` ("counterparty: keeps no signed records") and the Standard History card, which counts the deal from its first act.
- **`deal receive` refuses an offer whose `to` is null,** because no one can answer it.
- **`deal agree` on an offer you received** now refuses one that names another key, or no key, instead of sealing an agreement the other side would refuse.
- An offer to no key that an earlier version kept with `deal receive` stays listed as `offer_received`, and can now be neither agreed to nor declined, since no one can answer it.

### Changed: capsulectl-deal reads its own deal formats on capsule-cli v0.1.0-rc50 (#735)

- **`capsulectl book report --receipts` refuses a receipt the deal plugin's `capsulectl deal verify --bundle` does not call VALID**, naming the failing findings, through report v0.7.0's `Receipts.Gate` (#813): capsule-cli rc50's page gate reads no deal section. Only `--receipts` needs the deal plugin installed.
- `capsulectl/go.mod` requires capsule-cli v0.1.0-rc50, whose core no longer checks deal formats (capsule-cli#609), and report v0.7.0. capsulectl-deal now holds those checks itself, copied from capsule-cli v0.1.0-rc47 and credited: the deal report (x-deal-v0 and x-deal-v1, with the x-deal-v1 report rule), a stripped copy, the cadence chain (x-cadence-witness/v0) and its receipts, a sale's threads (x-deal-sale/v0) and a History selection (x-history-selection/v0). It reads them through the core's extension reader seam (`cli.ExtensionReader`), so the core's own result is never weakened.
- **`capsulectl deal verify --bundle FILE [--witness-directory FILE]`** is the reader verb: the core's `verify --bundle` with the deal reader. `capsulectl verify --bundle` in the core alone now checks only the neutral parts of a deal copy, and leaves its deal section uninterpreted. The plugin's page gates, its nested verdicts (a sale's threads, a History's deals, a composed page's members) and its share record go through the reader too. The plugin's page writers refuse a copy whose deal section does not verify: `deal report --html` (with `--share` too), `deal note --kind act --html`, `deal close --html` and `deal compose --html`; an x-deal-v1 `deal report --bundle` (with `--share` too) refuses a copy whose standalone report does not verify. The core's generic verbs (`capsulectl presentation list`, `presentation build`, `bundle --html`) read no deal section, so they can build a page for a copy whose deal, or nested deal, fails: a History whose nested deal fails is INVALID through `capsulectl deal verify`, but `presentation build` writes its page.
- **`capsulectl presentation build`, `presentation list` and `bundle` no longer check deal sections; check a deal copy with `capsulectl deal verify --bundle` or the deal plugin's page verbs.** On capsule-cli rc47 those core verbs refused a page for a copy whose deal section failed; on rc50 they build it, and the page lists each deal kind as "not interpreted by this page's checks" (pinned by `scripts/check-deal-pages.sh`). An optional reader for the core's page verbs is capsule-cli#624.
- **One difference from rc47, by the seam's design:** a copy with a valid cadence chain beside a failing direct witness receipt now reads INVALID. rc47 let the cadence claim replace the direct one and read VALID; the core now keeps its own failing claim beside the reader's (`reader_witnesses`). Every other copy of the rc47 goldens (#738) reads as rc47 read it.
- The x-deal-v1 report rule is the plugin's own, no longer registered with the core (`SetDealV1ReportVerifier` is gone from capsule-cli).
- Every line that told the user to check a deal copy with the core's `capsulectl verify --bundle` now names `capsulectl deal verify --bundle`: the receipt page's verify command, the receipt email's lines, the `deal report --bundle` help, and the deal skill's SKILL.md and README.md.

### Changed: package `deal`'s refusals name its own calls (#899)

- A refusal that tells the caller what to call next names this package's call (`Store.Admit`, `Store.NoteReceived`, `Store.CancelOffer` and the rest), where it named a capsulectl-deal command before; a caller that matched the old text should match `Code` instead.
- `InputError` and `ConflictError` carry `Code`, of the new type `Code`, and `Args` on those refusals: 16 constants (`CodeAgreeNoAdmit`, `CodeDeclineAgain` and the rest), each with the values its message names, so a caller can tell the refusals apart without matching text, or word them in its own terms. The constants are stable; their values are for the caller's own process.

### Changed: the deal skill teaches x-deal-v1 (#626, B6)

- **SKILL.md teaches the x-deal-v1 flow.** A deal between two parties, each with its own book, in order:
  - offer, and `deal receive` on the other side;
  - admission on each side (`deal admit`);
  - the user's intent with the opening;
  - the agreement exchange;
  - the final review (`deal check`), and the user's answer with the card they saw;
  - the payment (`deal settle --sent`, then `--status`) or another act (`deal perform`);
  - statements;
  - reading the deal once a checkpoint covers it.

  Booking (a commit check and `deal perform --act book`, or a payment), signature (a commit check and `deal perform --act sign`), sale, disclosure, cancellation and refund, dispute, later evidence, and what "settled" means without the person's own signature each have their v1 form. The command reference lists the x-deal-v1 verbs.
- **The x-deal-v0 procedures move, unchanged, to `LEGACY-X-DEAL-V0.md`.** It covers finishing an x-deal-v0 deal already open and a counterparty that runs only x-deal-v0. Cancel-by date and sale threads are there only.
- **The demo is a two-party x-deal-v1 purchase** (`demo/lamp`, `scripts/run-demo.sh`). It needs no capsule-engine install: its admission decisions are the engine's real output. The x-deal-v0 demo stays as `scripts/run-demo-x-deal-v0.sh`, and the Go tests run both.
- The `--deal` help of `deal check`, `deal note`, `deal report` and `deal settlement` names `deal offer` and `deal receive` as where a deal id comes from.

### Changed: `capsulectl deal standard-history staple` builds `history_card/2` only (#626)

- Offer staples carry `history_card/2` only: the profile's History card as a sampled chain of its cadence checkpoints, with no deal counts, at any length (`Cadence.HistoryCard2`, #690). `--derivation` stays, with `history_card/2` as its default and only value. `history_card/1` and `standard_history/1` are refused with the reason, because they are no longer stapled, and the standard card stays behind a signed ask (`deal respond`, unchanged). `--deal` is removed, and a staple no longer seals an answered record. The output keeps its shape, `derivation` plus `staple` (`request`, `reply`), with no `deal_id`. This requires evidencebook at b09f2037 (#690).
- A caller passing `--derivation history_card/1` or `standard_history/1`, or `--deal`, must drop them.

### Fixed: `deal settle` and `deal perform` take the check id `deal check` prints (#626)

On an x-deal-v1 deal, `deal check` prints a `check_id`, but `--check` on `deal settle` and `deal perform` took only the check record's digest, which no command prints. So a person could not pay or act on a v1 deal. `--check` now takes the printed `check_id`, or the digest as before. Found in the hand run before the default flip (#749).

### Added: `capsulectl deal check` and `deal note --kind approval` on an x-deal-v1 deal (#626, B1b part 2)

- `deal check` on an x-deal-v1 deal runs the same check as on an x-deal-v0 deal, with the same card, rules and materiality, over the deal's local steps. It seals the x-deal-v1 journal `check` (with a fresh nonce) and its `verdict`, and on a pass the automatic `standing_intent` approval, each with the deal's id. An act relying on that approval is not relied on until the person's signatures x-deal-v1 §8 lists are held. A check's companion, the payee's fingerprint, stays a local step: an x-deal-v1 `counterparty_profile` record has no member for it.
- A pay check on an x-deal-v1 deal records `payment_ref`, the reference fixed before paying that the payment will be made under (a Lightning payment hash, an ACP order id, the x402 authorization nonce), and is refused without one: the payer leg cites the check that recorded it. `payment_ref` on any other check is refused, and so is an `offer` check (an offer on an x-deal-v1 deal is `deal offer`). An x-deal-v0 deal refuses `payment_ref`.
- The pinned rules checker reads an x-deal-v0-shaped record of the check, rebuilt in memory from the local step and never stored, so the same input gives the same rules input on both versions.
- `deal note --kind approval` on an x-deal-v1 deal needs `--shown-card` for the user's (or the agent card's) answer to a check, so every such approval carries the card commitment the authorization reads. A `confirm_limits` approval answers an intent and keeps its `--said`.
- A check refuses when the deal's journal holds a record carrying the deal's id, of a kind these verbs write, that no local step names, since such a record would reach the authorization but not the card. The authorization reads the deal's own records and those bound to it, so an unbound record of another deal reaches neither. The refusal says the deal can no longer be checked and a new deal starts over.
- An x-deal-v1 deal's disclosures count in the cross-deal counterparty memory as an x-deal-v0 deal's do, under the same rule: only what was told with authority counts as told.

### Changed: x-deal-v1 is the default deal version (#626, B5)

- **New deals are x-deal-v1.** With no `deal set-version`, `deal offer` starts a deal, and `deal init` makes a new store in the layout x-deal-v1 needs.
- **x-deal-v0 is the legacy version.** It is frozen and kept only for a counterparty that runs x-deal-v0 alone, and will be removed once those have moved (evidencebook#656). `deal set-version x-deal-v0` selects it.
  - Without it, `deal open` and `deal sale new`, which start an x-deal-v0 deal or sale, refuse and say so. A buyer's thread of an x-deal-v0 sale already open (`deal open --sale`) follows the sale's version. On x-deal-v1, a sale to several buyers is one offer from each buyer, one agreement, and `deal offer decline` to the others.
- **Deals already open keep their version.** An x-deal-v0 deal finishes under x-deal-v0, and its report, export and bundle read as before.
- **An older store is never moved silently.** `deal offer` on it says to run `deal set-version x-deal-v1 --migrate` once (one-way; a backup is kept), and open deals keep working after the move.
- **The History card given on a signed ask** (`deal standard-history card`) now counts x-deal-v1 deals by default (`standard_history/3`). `standard_history/1` still counts your x-deal-v0 deals when you ask for it by name, and the card says so when the store holds any. On an older store that holds no x-deal-v1 deal, it names both ways on. The stapled card on offers is unchanged (`history_card/2`).
- **The canary keeps ticking.** `deal canary run`'s scripted deal is an x-deal-v0 purchase, so a canary profile with no version setting is set to x-deal-v0 on its next run, which says so.
- **The deal skill's demo and the repository's check scripts** set x-deal-v0 until they move to x-deal-v1.

### Added: `deal list` shows x-deal-v1 deals (#626, B4a)

- **The plain line** keeps v0's columns: kind `deal`; the opened time is the offer's; the last record time is your deal book's last record's (the offer's before the book opens); the counterparty column shows the first 16 characters of the counterparty's key (v0's shows the payee fingerprint); the log id is empty until your book opens.
- **The state:**
  - before your book opens, it is the deal's stored state: `offered`, `offer_received`, `agreeing`, `declined` or `cancelled`;
  - once it opens, it is computed now, and never sealed: `settled` or `open`, as a report says, or `not_checkpointed` while your book has records after its last checkpoint;
  - any other failure is an error, not a state.
- **Notes under the table** (on stderr, so the tab-separated lines stay a table), and in `--json` as `settled_note` and `checkpoint_note`:
  - beside `settled`: "settled: computed from the agent's records; no act carries the person's own signature yet (agent-action-capsule#627)";
  - beside `not_checkpointed`: "the next cadence tick covers it (about every <interval>), then list again".
- **Listing writes nothing.**
- **`--json`** adds `version`, `side`, `role`, `counterparty_key`, `outcome_as_of` (when the state was computed), and the offer's item.
  - The amount is given only when the offer has exactly one payment row; otherwise it is null, with `payment_rows`.
  - The summary line says "counterparty: no name recorded (key only)".
  - An x-deal-v1 deal has no `say` line yet (evidencebook#732).
- **`--since` / `--until`** list an x-deal-v1 deal when any of its records falls in the range, with its state now, and say so in `x_deal_v1_note`. Such deals are not yet in the range's totals or its disclosures (evidencebook#732).

### Added: `capsulectl deal note` on an x-deal-v1 deal (#626, B1b part 1)

- An x-deal-v1 deal keeps a local list of its plain steps, in the form an x-deal-v0 deal folds, and `deal note` runs the same note code over it. It seals the x-deal-v1 journal record of the same body: `intent`, `message`, `claim`, `evidence`, `change` (as `detail_change`), `disclosure`, and the agent moves (`searched`, `options`, `recommended`, `chose`, `couldnt`, as `agent_move`). Each record carries `deal_id` where the profile puts it.
- The first intent note opens the deal. `--input` is the intent, and the new `--opening FILE` gives the deal's type, an optional channel, who and recourse; the terms are the offer's, and the party role is the deal's own side.
- A step whose journal append fails is withdrawn, so a retry records it once. A step left by a process that stopped between its local event and its journal record is finished by the next `deal` command that loads the deal.
- Note kinds with no x-deal-v1 form refuse and name the verb that records them, or the one that will: `act`, `payment_received` and `delivered` (`deal perform` and `deal settle`, not yet available), `dispute` (`deal dispute`), `ruling`, and `acceptance` and `platform_approval` (`deal sale ...`). `approval` is not available yet: an approval answers a check, and `deal check` serves x-deal-v1 deals in part 2.
- The plain values stay on this device: the local list never leaves the store, and a copy or share carries only the journal's records, with their commitments. The cross-deal counterparty memory reads x-deal-v1 deals' steps too.
- `deal check` and `deal note --kind approval` on an x-deal-v1 deal follow in part 2.
- Each record is checked as an x-deal-v0 step is before it is stored, including the profile's privacy and wording scans. A seller's opening gets the representation defaults a seller's `deal open` gets. A seller agent's claim carries its authority in its own journal body. An email whose sender check cannot reach DNS is refused, not held for the tick.

### Added: `deal offer decline` on an x-deal-v1 offer (#626, B3)

- `deal offer decline OFFER_DIGEST [--reason not_selected|other] [--out FILE]` declines an x-deal-v1 offer sent to your key. It seals a decline in your journal and writes the file to send back to the offer's sender, who reads the deal as declined on `deal receive`.
- **What it accepts:**
  - only an offer whose `to` is your key, since a decline is signed by the key the offer was sent to;
  - not your own offer (`deal offer cancel` withdraws that), not an open offer, and not one sent to another key;
  - once per offer, whatever the reason, and never once you agreed to it. An agreeing run that stopped before sealing anything does not count as agreeing.
- **An agreement and a decline exclude each other:** `deal agree` refuses an offer whose decline is under way, and a decline re-run never overtakes an agreement.
- A stopped decline finishes on its re-run.
- **A sale to several buyers on x-deal-v1:** each buyer sends the seller an offer, and the seller agrees to one and declines the others as `not_selected`.
- To decline a held evidence request, use `deal decline`.

### Added: `capsulectl deal perform` and `deal statement` on an x-deal-v1 deal (#626, B2 part 1)

- `deal perform --deal ID --check DIGEST --act ACT --observed HOW --out FILE` seals a perform record in your deal book and writes the file to send to `--out` (required, so the output can list the person's signatures), which the other side counts with `deal receive`.
  - The act rests on your passing commit check. It is sealed only when that check's verdict has its approval, no detail changed since, and the approval has authorized no other act (`xdealv1.CheckActs`, run on the finished record).
  - Your first act is refused until you hold the other side's stamp proof on its agreement (`deal receive` brings it); run `deal perform` again then. If the other side runs no stamping, the act is sealed and the output says no stamp proof came.
  - The output lists the record digests the person must sign with their own key (`person_signature: missing`). Until those signatures are held, no one should rely on this act. The person cannot add them yet: where they travel is still being defined (agent-action-capsule#627).
- Flags:
  - `--statement row=value[:remedy]` (repeatable) says how a row went.
  - `--mark NAME` names the offer's named record this act is.
  - `--remedy-row ROW` makes the act a remedy for that row.
- `deal statement --deal ID --statement row=value ...` seals a statement record. A statement can be sent at any time: it does not wait for the other side's stamp proof, and it needs no check.
- Run again with the same flags, either command rewrites the same file and seals nothing new. A statement that repeats an older one after a different one is sealed again, since a row reads the side's latest. An `--out` that already exists is refused for a new record.
- An act is recorded as a local step after it is sealed, so the next `deal check`'s spend so far and used approvals count it. A run that stops in between is finished by the next `deal check` or `deal perform`, once.
- `deal agree` now stamps on the offerer's side too, and its file carries the stamp proof. So a first act on either side is never left waiting on an offerer that has not acted (PROFILE §8).

### Added: `deal report --bundle` on an x-deal-v1 deal (#626, B4c)

- `deal report --deal ID --bundle FILE` writes your deal book's Evidence Bundle, for `capsulectl verify --bundle`. It covers every record of your book at the checkpoint the report rests on, with their payloads.
- The sealed report travels beside the book, in `x-deal-report/v0`, with `x-deal-v1` naming it and carrying the records its rows rely on, the other side's included.
- Every audience's bundle carries the same records. An adjudicator's lists no rows.
- The bundle is checked as a reader checks it before it is written.
- Making it writes nothing to your book: no disclosure record goes into an x-deal-v1 book.
- With `--share counterparty|adjudicator --to NAME`, the bundle is a disclose act, as for `--out`: checked to contain none of the plain values kept only on this device, then put on record (the records it carries and the digest of the exact file), then written.
- **What a shared bundle discloses:** every record of your deal book with its contents, which is what you signed, and the other side's records your report's rows rely on, which is what it sent you, up to the report's checkpoint. For a counterparty that is the deal's own exchange; an adjudicator sees all of it. Your own notes (intent, opening) are never in a book, so never in a bundle: none of your plain values (your words, your limits, the counterparty's details) is in any bundle, its payloads included.
- `--out` and `--bundle` are one or the other.
- `deal export` on an x-deal-v1 deal points at `--bundle`.
- capsulectl-deal now needs capsule-cli v0.1.0-rc47, whose `verify --bundle` finds an x-deal-v1 deal's last step through its disclosed header (capsule-cli#605).

### Added: shared copies of an x-deal-v1 report (#626, B4b part 2)

- `deal report --deal ID --share counterparty|adjudicator --to NAME --out FILE` writes a copy of the report for someone else.
- An adjudicator's copy:
  - is only for a deal whose agreement names an adjudicator;
  - names `--to` as its referee. `--to` is a label for who it is for; the copy is recorded for the adjudicator key the agreement names;
  - lists no rows, and carries no agent claims (an x-deal-v1 report has no claim fields).
- As for an x-deal-v0 copy, the copy is a disclose act:
  1. its exact bytes are checked to contain none of the plain values kept only on this device (your words, your limits, the counterparty's details);
  2. the bytes are written to a temporary file beside `--out`, whose folder must take a hard link;
  3. it is put on record: the audience, the recipient and the digest of the exact file;
  4. only then is it linked into place, which never writes over another file.
- A refused copy writes and records nothing, and so does an existing `--out`, a folder that cannot be written to, or one on a drive without hard links. If the final link or the folder's sync fails after the copy is put on record, the error says what is on record and what is on disk.

### Added: `capsulectl deal report` on an x-deal-v1 deal (#626, B4b part 1)

- `deal report --deal ID` on an x-deal-v1 deal prints your own copy of the deal's report: each row's status as computed now from what you hold, whether the deal is settled, and what it rests on (PROFILE §10). It counts the other side's records you have received; the other side may hold different records and see a different status. Printing seals and writes nothing.
- Beside the report, `settled_note` says that no act on the deal carries the person's own signature yet (the format is still being defined: agent-action-capsule#627), so "settled" is computed from the agent's records, which the person has not signed. evidencebook#729 replaces this with a check per act.
- `--out FILE` also seals the report, standalone and appended to no book, and writes it with its signature to that new file. The sealed report verifies against the deal's agreement, and it must be made with the profile you agreed with.
- A book with records after its last checkpoint has no report yet: the next cadence tick covers it (about every cadence interval, 5 minutes by default; `deal tick` runs one once it is due), then run `deal report` again. A deal with no agreement of yours recorded yet names `deal agree`.
- `--html`, `--email`, `--from-bundle`, `--directory` and `--include-agent-claims` do not apply.
- `deal close` and `deal terms-result` on an x-deal-v1 deal now add "See `deal report`".

### Changed: `capsulectl deal settle --sent` asks a payment's check again before money leaves (#718)

- Two pay checks that each passed before either payment left could, together, go over a limit only one of them fits.
- `deal settle --sent` on a payment (not a refund) now runs the check's own evaluation again before the money leaves.
- With a rules checker configured, the checker runs again too. Its history holds every payment counted from the moment it was sent, on this deal and every other x-deal-v1 deal. Without one, only the check's own evaluation is asked again, and that does not count earlier payments.
- The send is refused, and nothing is sealed, when:
  - your check now pauses or fails where it passed;
  - your rules now escalate or deny where they allowed;
  - a rule now fails that did not before.

  Run `deal check` again. When your rules checker cannot answer, nothing is sent until it answers. A check you approved while the rules could not be checked still sends while they cannot.
- A payment that failed does not count toward your limits. Its check stays used, so a retry needs a new `deal check`.

### Added: `capsulectl deal settle` on an x-deal-v1 deal (#626, B2 part 2)

- `deal settle` seals a payment leg in your deal book and writes the file to send to `--out`, which the other side counts with `deal receive`. The leg (payer or payee), its terms (the offer as sent) and its amount come from the deal and your check. The settlement member is checked with AAC's structure check before anything is sealed.
- A payment you make takes two calls.
  - `deal settle --sent --check DIGEST`, before you hand the payment to the rail. It checks, on the leg as it will be sealed, that your first act has the other side's stamp proof, that the check's approval holds, and the approved amount, so a payment they would refuse never leaves. It then counts the amount toward your limits and seals a "payment sent" evidence record in your journal.
  - `deal settle --check DIGEST --status settled|failed --out FILE`, when the rail answers. It seals the one payer leg, bound to what was sent: the amount, the refund target and the whole reference. On x402 and the other types whose reference is known only after paying, `--payment-ref` names the rail's own reference. A re-run with other inputs is refused.
  - While the rail still says pending, nothing more is sealed. While a payment is in flight, no record that changes the deal's details can be sealed, so the payment already sent stays authorized.
  - A failed payment counts no spend and uses up its check: to try again, run `deal check` for a new one. If the rail's answer is lost, look the payment up on the rail by the reference `--sent` printed, then run `--status`.
- References:
  - `--payment-ref-type` and `--payment-ref` name the payment's reference, with the type's qualifiers (`--payment-ref-network` for x402, and so on).
  - On `ln.payment_hash`, `bolt12.invoice_payment_hash`, `acp.order_id` and `ucp.order_id`, the payer leg's reference is the check's `payment_ref`.
  - On x402, the leg carries the transaction, and the link from it to the checked nonce is the payer's claim unless a verifier reads it on chain.
- What you received is a payee leg: `--received` and `--receive-fee` (0 when none was taken), `--status pending|settled|failed`, and `--supersedes` for a later observation.
- A refund (once a refund leg is in a book, the report no longer matches that row's payment legs against each other, the payment's included: a refund needs its own terms, which are not defined yet):
  - It is a leg with `--reverses` naming a leg of your own book: the seller's payer leg on its cancel check, and the buyer's refund received.
  - The buyer's refund received returns what was received once, when settled, and never more than the payment counted.
  - A failed payment cannot be refunded. A second refund of a leg is refused while another is in flight or done, and a failed refund leaves the leg free to refund again.
- The rules checker's history now counts x-deal-v1 acts: a payment from its send, and a perform. Each is read as the x-deal-v0-shaped record of its own deal.

### Added: `deal settlement` on an x-deal-v1 deal (#626, B4d)

- `deal settlement --deal ID` on an x-deal-v1 deal prints, for each row whose owed is an amount, in the offer's order:
  - `report_check`: the report row's verdict (`passed`, `failed`, `not_run`, `not_recomputed`);
  - `payment.check`: the payment check on the legs, as the library reads your records now (`pass` or `fail`, or `""` with a `reason` when the check ran without a verdict, such as legs not yet agreed or several payments; with `checked_over`, and, when present, `excluded` and the derived settlement);
  - or `not_run`, when the check did not run, with a plain `not_run_note`:
    - `no_offer_pair`: this store holds the offer but not its signed copy, so the payment check cannot run here; receive the offer file again (`deal receive`) to keep it;
    - `no_acceptor_key`: your agreement names no key for the other side (it keeps no signed records of its own), so the payment check cannot run on this deal;
    - `not_recomputed`: the row is not recomputed, as its verdict says.
- `rows_state` says whether the rows are listed. Before the deal starts, `rows_note` says so instead of an empty list.
- Nothing is computed in the plugin, and nothing is written.
- The output carries `settled_note`: no act carries the person's own signature yet.
- v0's `--leg`, `--object`, `--payer-key` and `--payee-key` are refused: a v1 deal reads the other side's legs from the records you have received.
- A deal whose book has not opened names `deal agree`.
- The checkpoint message of `deal report`, `deal list` and `deal settlement` now ends "then run the command again".

### Fixed: on x-deal-v1, an answer that was not applied authorizes nothing (#790)

- `deal perform` and `deal settle --sent` refuse a check whose answer `deal note --kind approval` marked not applied: a later check or a changed detail came first, or the check was already answered. The refusal says to check again and seal the user's answer to the new check. x-deal-v0 already refused this.

### Added: the x-deal-v1 entry check (#773)

- `xdealv1.EntryOf` reads one party's copy, the bundle `deal report --share ... --bundle` writes, and says whether that party entered a ruling. The copy must carry the whole deal book its report rests on, every record from the agreement to the checkpoint. The states:
  - `entered`: the party's statement naming the ruling is in its book under the copy's checkpoint, which is signed by the checkpoint key its agreement names, and a receipt from a witness the agreement names verifies over the cadence checkpoint that carries that book checkpoint;
  - `entered_unwitnessed`: the same, without such a receipt;
  - `not_entered`: by its signed time, the copy's checkpoint is after the ruling's witnessed stamp, and it holds no such statement;
  - `not_shown`: the copy's checkpoint is not after the ruling's witnessed stamp, or the ruling has no witnessed stamp.

  The ruling's time is the time a witness the agreement names gave the adjudicator's stamp that covers it, never the ruling's own `at`.

  None of these is a refusal. The proof is the party's own records; the copy, a disclose act, is how they reach the adjudicator.
- `evidencebook.VerifyWitnessReceipt` verifies a witness receipt for another party's cadence checkpoint, with the check `Cadence.Witnessed` applies to the holder's own.
- Rust and Python: #774, #775.

### Added: the record and report calls, `Book.RecordAct` and `Book.EvaluatePack` (#895)

- `Book.RecordAct` seals one act of the book owner's agent, in capsule-engine's action form (the JSON `capsule-engine check --action` reads), as an `agent_act` record, a producer claim (the owner's own word, which no one else has checked). The book refuses an `agent_act` record whose act the engine cannot read, whichever call appends it. An act the rules engine cannot read is refused and seals nothing.
- `Book.EvaluatePack` runs a capsule-engine rules pack (capsule-engine's `guard` engine; this package decides nothing itself) over those acts in log order, each against the earlier ones, and seals the outcome as an Evidence Result v0. Each claim is one rule on one act, tier `recomputed`, grade `self-attested`. A rule that does not apply has no claim and counts in `excluded_not_applicable`, and no rule is rolled up to one verdict. It returns the bundle rooted at the Result, with the contract report, which `report.Build` renders. Rows the engine reports that are no rule of the pack are left out. Running it again over the same acts finds the Result it sealed. Acts to which no rule of the pack applies are refused, since there is no claim to report. The bundle carries every act in the book in full (amount, currency, target, operator, developer), so share it, or the page built from it, only with someone who may see all of them.
- docs/GUIDE.md opens with "Start here: three calls", which points to record an act, get the report and open a deal (the `deal` package, #899).

### Fixed: a deal still reports after the other side forks at its first record (#878)

- **The bug.** If the other side signed two different agreements as the first record of its deal book and sent both, the holder counted nothing of that book. Its status and every report then failed with "an offerer writes its agreement after the acceptor's it holds". An honest party could not show its deal or answer an adjudicator.
- **The fix.** The holder now reads the branch its own book names as received (its records' `seen`), on either side of the deal. The deal both sides signed starts from it, and `rests_on.counterparty.fork_at` stays 1, so a page can say the other side's book forked at its first record. This matches the branch the adjudicator reads (`GatherCase`). A holder whose book names neither of two first records reads nothing of that book: the deal reads awaiting before agree_by and not started after, with the fork shown (Steven's ruling u217). `Start` in Go, Rust and Python reads an offerer that counts no acceptor agreement that way instead of refusing it.

### Added: `Cadence.HistoryCard2`, the shelf's `history_card/2` staple (#626)

- `Cadence.HistoryCard2(ctx, n, window)` builds the profile's `history_card/2` over the cadence log as a staple: the finished ticks at the sampled positions `P(n-1, window)` (Evidence Request -03, `history_card/2`), as of tick `n` (0 for the newest finished tick), each with the consistency proof from the one listed before it. It holds no record content and no deal counts, and `CheckStaple` accepts it for the shelf's signer. Only the sampled ticks are read, so it has no tick cap: the cadence's `history_card/1` staple is refused past `DefaultMaxAnswerItems` ticks (#354). Window 0 is `DefaultHistoryWindow`, and a window outside 1 to 64 is `ErrInvalid`. Offer staples carry `history_card/2` only. `Cadence.HistoryCard`, and `Book.Staple`'s `history_card/1` and `standard_history/1`, stay for callers that have not moved yet and are deprecated (removal: #692). Cards answered on a signed ask are unchanged.

### Fixed: an x-deal-v1 holder no longer holds a counterparty agreement of its own role (#884)

- `xdealv1.ReceiveProfile.Admit` holds a counterparty's deal book only when its agreement takes the other role than the holder's agreement, or, at an offerer holding only its offer, the role the offer did not take. Before, such an agreement was held and the History card read the deal as "stamped by both".

### Added: `report.View.Coverage` checks and states the range a report over time windows covers ([#689](https://github.com/action-state-group/evidencebook/issues/689))

- For a report over a holder's time windows (window books, #689), `View.Coverage{HolderCheckpointKey, Witnesses}` has `report.Build` check, from the bundle the page embeds and under the holder's checkpoint key the caller obtained, that the windows link, tile the range the report's window chain states, and each has its answer (`evidencebook.VerifyReportCoverage`). A range with any gap is refused, each gap named, and no page is rendered. A covered range is stated in a banner above the report, in the shell's own `aac-banner` classes, with what the check does not show: without witness receipts the windows' continuity is as the holder signed it (with a witness directory given, that every window's final checkpoint carried a receipt that verified), and no check shows that an answer holds every row of its window. `Page.Coverage` is what the check found.
- Like a wording layer, the banner lives in the page, not in the bundle, so a page built with one gets no permalink. Coverage without a holder key, on a diagnostic page, or on a report that cites no window chain, is refused.
- `report` now requires the evidencebook module at the commit that adds window books (#800).

### Changed: the coverage banner names each placed deal and states the report's limit (#797)

- On a report over a deal store's windows (#797), the banner `report.Build` writes for `View.Coverage` also says how many deals are placed in the windows and that each has its answer over its whole book, and lists each deal's line in a collapsible list after the report's limit: its answer covers its whole book, through the checkpoint it is anchored at, records after its window included. A placed deal with no answer, or one answered at a tree the window chain does not prove, is a gap, and no page is rendered. The banner ends with the report's limit (each answer proves its rows are in the holder's log; the rows' field values are the holder's word; no proof shows that no row was left out), in place of "no check shows that an answer holds every row of its window."
- `report` now requires the evidencebook module at the commit that adds journal windows and placed deals (#846).

### Changed: `report` requires the library at 9ae62286, which has the front door's calls (#895)

- `report/go.mod` requires the `evidencebook` library at 9ae62286 (was 3550482e), which carries `Book.RecordAct` and `Book.EvaluatePack` (#908). A new Example, `Example_frontDoor`, runs the three calls end to end: it records three acts, evaluates them under capsule-engine's Everyday Rules 0.3.8, renders the bundle with `Build` as the contract report (VALID, 21 claims), and seals and checks the page with `SealPage` and `CheckPage`.

### Added: `report.Page.IgnoredReceipts` lists the receipts no deal cites (#764)

- A `View.Receipts` entry whose exchange capsule no deal's claims cite is neither carried nor held (a node hands over every exchange it holds, and the report's window decides which became deals), and `Page.IgnoredReceipts` now lists its exchange capsule ids, in the order given, so a caller that meant to carry one can tell a mis-keyed id from a deal it handed nothing for. A capsule cited by the claims of two deals is still refused.

### Added: `report.Receipts.Gate`, the caller's own check of each carried receipt ([#735](https://github.com/action-state-group/evidencebook/issues/735))

- When set, `Receipts.Gate` runs on each carried receipt bundle after the page gate, and a non-nil error refuses the receipt, so no page is built. capsule-cli's page gate no longer reads deal sections (capsule-cli#609). The caller that defines the receipts' format checks them here, and `report` stays generic and links no format's code. capsulectl-book passes the deal plugin's `capsulectl deal verify --bundle`.

### Added: `report.Receipt.NotClosed` marks a deal that had not closed when the page was built ([#778](https://github.com/action-state-group/evidencebook/issues/778))

- A `Receipt` sets exactly one of `Bundle`, `Held` and the new `NotClosed`: the deal had not closed when the page was built (an invoiced exchange with no settlement or delivery recorded yet), so it has no receipt, and the page says so apart from a held receipt and from a deal with no entry (a page built wrong). `report.Build` hands the deal ids to capsule-viewer's `OfflineOptions.ReceiptsNotClosed` (capsule-viewer#358), so `report` requires capsule-viewer go v0.23.0. A `Receipt` with none or more than one of the three is refused.

### Added: `report.View.Receipts` carries deal receipts into a settlement report (#764)

- `report.Build` takes `View.Receipts`: the receipt module a page opens a deal's receipt with (`capsulectl.deal-view/v0`, its manifest pinning the script and stylesheets; the composed receipt module is refused until a composed bundle, rooted at the composer's composition record, can be bound to its deal) and the receipts the holder hands over, each either a receipt bundle or `Held`, the holder's share policy keeping it off the page ([capsule-viewer#228](https://github.com/action-state-group/capsule-viewer/issues/228)). A receipt names the exchange capsule it was fetched by (`Receipt.Exchange`); `Build` maps it to the one deal (the part of a claim id before `::`) whose claims cite that capsule as evidence. The report never evaluates the policy: the caller states it, and nothing is carried by default, so a `View` with no `Receipts` builds the same page as before. A receipt whose capsule no deal cites is ignored (a node hands over every exchange it holds; the report's window decides which became deals), and a deal no receipt names reads "Receipt not carried with this page".
- That a bundle is the receipt of the exchange capsule's deal is the caller's word: nothing in a plain receipt names the capsule a Result cites. [agent-action-capsule#674](https://github.com/action-state-group/agent-action-capsule/pull/674) and [capsule-viewer#353](https://github.com/action-state-group/capsule-viewer/issues/353) are to give the Result a digest to check it against.
- `Build` refuses, before rendering, an exchange capsule that the claims of two deals cite, two receipts for one deal, a bundle that does not pass the page gate (`cli.PageGate`, the verdict a deal page is written under) or that the module's manifest does not take (its `requires` bundle kind and extensions and `forbids` extensions and profiles), receipts on a Result with no deal, and receipts on a diagnostic page. `Page.Permalink` refuses a page with receipts: the permalink carries the bundle alone.
- `report/go.mod` requires capsule-viewer go v0.22.0, the first release with `OfflineOptions.Receipts` ([capsule-viewer#351](https://github.com/action-state-group/capsule-viewer/pull/351)), which refreshes the page runtime: the three committed report pages under `report/testdata/presentation` are rebuilt.

### Changed: a settlement report opens its receipts with capsule-viewer's receipt modules (#869)

- `report.Receipts.Module` is now `Modules`, the receipt modules the page carries. Each must be one of `report.ReceiptModuleIDs()`: `capsuleviewer.receipt.unilateral/v0` and `capsuleviewer.receipt.seller/v0`, from capsule-viewer's Go package `receipt` (capsule-viewer#383). Each may be given only once, and at least one must be given. A carried receipt must be one that at least one of them draws. `report.ReceiptModuleID` (`capsulectl.deal-view/v0`) is removed, so a settlement report no longer opens receipts with evidencebook's deal view. A composed (two-sided) receipt is still refused, as before. This is a breaking change for callers, which today means capsulectl-book, which moves in the next capsulectl release (#869).
- `report` requires capsule-viewer/go v0.26.0, whose page runtime opens a carried receipt with those modules. In a report, the owner's copy reads as a shared copy, with the verifier's top line. The committed presentation pages are rebuilt on it.
- A carried receipt is held to the verdict of the page that opens it. That is capsule-cli's page gate or, for a producer-selected receipt such as a Mesh node's, agent-action-capsule's page verdict (`bundle.VerifyBundleForPage`, `bundle.Verified`), which the page runtime applies. Before, a producer-selected receipt was refused because strict verify calls it INCOMPLETE. Any other unmet check still refuses a receipt.
- `capsulectl/go.mod` also moves to capsule-viewer go v0.26.0. capsulectl builds report pages with the report module's runtime, and its copies of those pages and their snapshots (`capsulectl/testdata/presentation/4-monthly-outcome` and `5-monthly-compliance`) follow the report module's pages. The only change is the viewer version the page names. capsulectl-book still opens receipts with the deal view until it moves to report v0.9.0.

- `Book.RequestSentWithCommitment` records a sent request as `RequestSent` does and also keeps a JSON object the requester held from the responder when it asked (such as the responder's advertised commitment) as the optional `commitment` member of the `evidence_request` statement (`RequestStatement.Commitment`). The book never reads or checks it. It is the requester's own copy, and the responder does not sign it in this record, so no card, count or check may read it as "the responder committed to X"; only a later step that verifies the object under the responder's key can give it that weight. A record without the member reads as before, and a reader that does not know the member ignores it. Rust has the same as `ask::request_sent_with_commitment` (Mesh-LLM/capsules-plugin#368, row B3).

### Fixed: `xdealv1.SharedOperator` labels a party's own key with no stamped time (#822)

- An adjudicator key equal to a party key is now labeled "not independent (shared operator)" whatever the stamped time, a zero one included. One key is one operator at every time, and only a binding's validity window needs the time. Before, a ruling with no witnessed stamp time went unlabeled even when its adjudicator was a party.

### Added: `xdealv1.SharedOperator` marks a ruling "not independent (shared operator)" from a controller binding ([#780](https://github.com/action-state-group/evidencebook/issues/780))

- `SharedOperator(ruling, agreement, stampedAt, bindings, revocations)` returns `NotIndependentSharedOperator` ("not independent (shared operator)") when a valid controller binding with claim `same_controller_asserted` joins the agreement's adjudicator key to the ruling's buyer or seller key, and "" otherwise. An adjudicator key equal to a party key gets the label with no binding. A binding is judged at the ruling's stamped time, the time of the witnessed checkpoint covering it, which the caller passes in (never the ruling's own `block.at`, which its author sets, so an adjudicator cannot move it out of the binding's window); a revocation after the stamp does not remove the label; a bad signature, an inconclusive result or a binding of another claim shows nothing. There is no positive state: a reader shows this text or nothing, never "independent". It adds no record member and holds no list of any operator's keys. Rust and Python do not have it yet ([#782](https://github.com/action-state-group/evidencebook/issues/782)); showing it on the card is a separate change.

### Fixed: a refused window policy leaves no shelf file (#827)

- `OpenSQLiteShelf` checks a window policy before it touches the disk, so a refused policy leaves no shelf or lock file behind (evidencebook#827).

### Added: a Terms Pack served by the digest of its definition, and the requester's report over the answer (#606)

- `DerivationDefinition` gains `Recipients` (one definition digest granted to several requesters, as a set with `Recipient`), nested `Fields` (a header member or a path into one, such as `statement.generated_at`, which reveals that member only; a path and a field it extends are `ErrInvalid` together) and `Proofs`. With `Proofs`, the answer carries the anchor's signed `cll-checkpoint` and one `cll-inclusion-proof` per record (and the consistency proof when the checkpoint extends an earlier one), and the requester's `RecordResponse` checks them instead of reporting `anchor_inclusion` NOT_CHECKED. Proofs count against the item limit (a holder refuses, for a limit, what the requester's cap would refuse), and a request pinning an exchange half is checked under the artifact's checkpoint. Without `Proofs` an answer is byte for byte what it was, and `testdata/derivation/vectors.json` is unchanged. The holder attests the field values; the proofs show the rows are records of its log at the anchor, never that no row was left out.
- `Book.DerivedInputs` reads a recorded, verified answer to a derivation named by the digest of its definition (it refuses a refusal, a failed answer and a registered token). `Book.ReportFromDerivation` seals the Evidence Result the caller's evaluator returns, after checking that it is a valid Evidence Result v0 whose every claim cites exactly the response record (the full gate, schema and cross-claim rules) and builds one Evidence Bundle with every payload and the `contract-report/v1` extension (`ExtensionContractReport`); `ReportOptions.Derivation` names the pack the evaluator runs and must be the one answered, an answer without proofs is refused unless `AllowUnproven` (then the view title begins `UnprovenInputsTitle`), and calls for one Result seal it once: the machine view and, with the report page, the human view. The Result is the requester's, signed by its own book; the module runs no pack.
- `Book.Doctor` lists the digests of the registered derivations it serves.
- Go only for now: Rust (#746, needed by the Mesh plugin) and Python (#747) have no twin. The interop profile defines no material for a record set (P-13); the proofs use the existing kinds until agent-action-capsule#668 lands.

### Added: a verdict's `received_history` on x-deal-v1 (#666)

- An x-deal-v1 `verdict` body may carry `received_history`: `history_sha256`, the SHA-256 of each History document the rules checker read about the counterparty, and `input_sha256`, the SHA-256 of the JCS form of the checker input's `received_history` member. It binds by digest, never by content (capsule-cli#327, design §5.4), and is read beside v0's closed `body_verdict`, like `nonce`. `xdealv1.VerdictBody.ReceivedHistory` (`ReceivedHistoryDigests`) holds it, and the schema requires it to sit beside `rules` a checker was started on. Its optional `alone_deny: true` marks a verdict that asks (`pause`) over rules that deny, because History alone never makes the verdict NO (capsule-cli#327 design §3, condition 1); the schema allows a pause over a deny only with it. Two positive fixtures outside Appendix A (`received-history/`) and eight negatives cover it. The member is a format door: a reader from an earlier release refuses a verdict that carries it, so readers need this release or later.

### Added: window books, a shelf split into one book per time window (#689)

- `ShelfConfig.Window` (`WindowPolicy`: day, ISO week, month, quarter or year, calendar periods in UTC) splits a shelf into one book per window. The window is fixed when the shelf's file is created: a reopen with another policy or none is refused, a file created without one never gains one, and a windowed file is shelf version 3, which older builds and the Rust and Python shelves refuse. A windowed shelf does not take the facts migration. `ReadShelfWindow` reads a file's policy without opening it.
- `Shelf.Current` returns the current window's book, the only one that takes records; `Shelf.Roll` closes ended windows and opens the next ones, empty windows included; `Shelf.WindowBook` and `Shelf.Windows` read them. A window book opens with a `window_open` record naming the previous window's final tree and ends with a `window_close` record and a final checkpoint; a record outside its window, or in a closed window, is refused (`ErrWindowClosed`), and a clock that steps back over a boundary is refused (`ErrClockBehind`). The disclosure policy in force is re-stated at the start of each window, and `Shelf.RegisterDerivation` and the unsigned-ask limit apply across windows.
- `Shelf.Respond` answers from the window book a request's pin names and records the answer in the current window, so an answer's proofs stay inside its window.
- `Shelf.WindowChain` exports a run of closed windows as a `window_chain/v1` document, and `VerifyWindowChain` checks it alone, naming each break (`chain breaks at <label>`, `window <label> not witnessed`). `Book.RecordWindowChain`, `Book.ReportFromWindows` and `VerifyReportCoverage` build and check a contract report over a range of windows, naming every window without its answer. Without witness receipts a chain shows continuity only as the holder signed it, so a holder could re-cut a window and re-sign those after it. Neither check shows that an answer holds every row of its window.

### Added: a window's grace, for a Close over an earlier window (#807)

- `WindowPolicy.Grace` (zero: none) is how long after a window ends its book still takes a close/v1 Close for a period inside it; the window closes, and its final checkpoint exists, when the grace ends. It is fixed when the store is created, must be shorter than the period's shortest length, and a store with a grace is shelf version 4, which builds without grace refuse. A store created before grace opens with none.
- When a window ends, the shelf takes its boundary checkpoint, which the next window's open record names. A window chain (version 2) carries each such window's boundary checkpoint, a consistency proof to its final checkpoint, and every record after the boundary; `VerifyWindowChain` names any late record other than a close/v1 for a period inside the window or an index root, and a record left out. `Shelf.WindowChain` over a window still in its grace returns `ErrInGrace`, naming when it closes. `WindowState` gains `InGrace` and `ClosesAt`. A chain does not show that a holder did not delay a window's boundary past its end, so records placed before a late boundary read as in the window.

### Added: a shelf whose windows are its journal, with each deal's book placed in a window (#797)

- `WindowPolicy.Journal` makes a shelf's window books its journal (`journal/<label>`), for a deal holder: the shelf still holds one book per deal, its node and case books, the cadence and book facts. It is fixed when the shelf is created, requires the base `journal`, no grace and no predecessor, and gives shelf file version 6, which earlier builds and the frozen Rust and Python shelves refuse (#844, #845). Such a shelf is created on book facts, so `Migrate` has nothing to do on it.
- `Shelf.Journal` returns the current window's journal book there. `Shelf.JournalView` and `ExistingJournalView` return a `JournalReader` over every journal window in order, with one seq space; `StatusInput.Journals` carries it to a status provider (`StatusInput.Journal` stays the one journal book of a shelf without windows), and `ExistingJournal` refuses such a shelf. Each journal window's fact is a journal fact, decided by the log's kind. The x-deal-v1 receive cache, status provider and per-deal report read the journal across windows.
- `Shelf.Place` places a deal's book in the window holding its agreement's time, with a `window_place/v1` record in that window's journal, and appends the book's first record in the same step, so no roll can leave the deal out of its window's close; a placed book that holds records keeps its window as the deal runs on. `Shelf.Placement` reads it and `Shelf.WindowEnded` says a window book has closed, for a cadence host's `Ended`. A journal window's close is `window_close/v2`, which lists each deal placed in it with the deal book's tree at the close. A chain of journal windows carries, per placed deal, its checkpoint at the close, its latest checkpoint and a consistency proof between them (`ChainWindow.Deals`); `VerifyWindowChain` reads closes of versions 1 and 2, checks each deal's proof and returns each window's `Placed` and `Proved` trees.
- A report over journal windows covers each placed deal too: `Shelf.Respond` answers a pin on a placed deal book's checkpoint from that book, `ReportFromWindows` takes one answer per placed deal, at the tree the chain proves extends its tree at the close (`WindowInputs.Deal` names it), and `VerifyReportCoverage` names a placed deal with no answer, or with an answer at any other tree, an early one or a fork. It returns each covered deal in `ReportCoverage.Deals`, with a line saying the answer covers the deal's whole book through the checkpoint it is anchored at, records after the window included, and the report's limit once in `ReportCoverage.Limit`.

### Added: a JSONL book continues in a windowed store (#808)

- `Book.CloseForWindows` closes a book into a windowed store: a `book_close` record naming the store, then a final checkpoint ending at it, after which the book refuses every append (`ErrBookClosed`). It returns the store's `WindowPolicy`, whose new `Predecessor` (the book's final tree) and `PredecessorPolicy` the shelf keeps (shelf version 5). The store starts at the first whole second after the close (`SuccessorSince`), so its first window is partial (`WindowState.Partial`, and `partial` on the chain's verified window).
- A chain of such a store is version 3. From the store's first window it carries the predecessor (`ChainPredecessor`, built with `ChainPredecessorFromBook`). `VerifyWindowChain` checks its close record, final checkpoint, key, receipts and tree, and names a missing or altered predecessor or a record after its close. A report over the conversion's period names the part before it as not covered.

### Added: `VerifyWitnessReceiptTime` (#819)

- `VerifyWitnessReceiptTime` is `VerifyWitnessReceipt` with the receipt's registration time (its `iat`), when the receipt verifies and carries one. A receipt with no time gives none. A reader uses it to take the time a witness registered another party's stamp, such as an adjudicator's stamp covering a ruling.

### Changed: x-deal-v1 states what agreeing to an adjudicator commits a party to (#898)

- **PROFILE.md.** An agreement that names an adjudicator agrees to answer its `deal_adjudicator_copy/1` request in full. A party that refuses or does not answer leaves the case to proceed without its answer: its book is read only from the copy the other side holds and any copy it opened the case with, and a ruling may go against it, but only on records it signed itself. This is a statement of the existing rule; no record, check or code changes.

- x-deal-v1 is the default deal record format and x-deal-v0 is frozen ([#626](https://github.com/action-state-group/evidencebook/issues/626)): the status lines of `dealprofile/x-deal-v0/PROFILE.md` and `dealprofile/x-deal-v1/PROFILE.md`, and the design note's §2.5, Q6 and Q7, say so. No record, check or vector changes; books sealed under x-deal-v0 stay readable and verifiable, unchanged.

### Added: `xdealv1.CheckAgreedAt`, agreed no later than witnessed (agent-action-capsule#696)

- A deal's `agreed_at` may be later than its first witnessed time, the record time of the holder's own agreement from a witness the agreement names, by at most `xdealv1.AgreedAtSkew` (300 seconds), as agent-action-capsule `spec/standard-history-v0.md` §11.2 "Agreed no later than witnessed" says. A later one returns `xdealv1.ErrAgreedAfterWitnessed`: the deal entry is not well formed. Vectors: `testdata/deal-v1/agreed-at/vectors.json`.

### Added: `xdealv1.ChangesDetails` (#626)

- `ChangesDetails(Record)` exports the test the authorization applies to each journal record between a check and its act. It matches a `detail_change`; a `message` with a counterparty; an `evidence` with a counterparty or `counterparty_facts`; an `intent` that starts a leg or picks the item; and an `agent_move` that is `chose`.
- A writer holding a payment in flight runs it on each journal record it would seal, and refuses one that matches until the payer leg is sealed, so the payment already sent stays authorized. `CheckAuthorizations` uses the same function.

### Added: `xdealv1.CheckActs`, which holds a settled amount to the checked one (#626)

- x-deal-v1 §8 keeps x-deal-v0's authorization rules 5 and 6 unchanged. Rule 5 requires an act's amount and currency to equal the checked ones where both carry them.
- `CheckAuthorizations` reads records, but a settle's leg travels in its Capsule's `settlement` member, which the record does not carry, so it could not compare them.
- `CheckActs(journal, []ActIn{{Record, Settlement}})` runs the same check and also refuses:
  - a leg whose asset code differs from the check's `currency`;
  - a leg whose settled amount differs from the check's `amount_minor`. A payer leg settles its `amount`, and a payee leg settles `received` plus `receive_fee`, and one that states no `receive_fee` is refused.
- Amounts compare as integers at the larger scale, with `amount_minor` at the currency's minor units. A checked amount in a currency with no known minor units is refused.
- `CheckAuthorizations` is unchanged and reads an act with no settlement member.

### Fixed: `xdealv1.CheckActs` holds a refund to its cancel check's `cancelled_amount_minor` (#719)

- A seller's outgoing cancel check records the amount as `cancelled_amount_minor` when it states no `amount_minor`, as x-deal-v0's builder does. A refund leg answering such a check is now held to that amount, by the same rule as any other checked amount: a payee leg settles `received` plus `receive_fee`, and must state the fee.
- `amount_minor`, when stated, is still the amount held to. Only a cancel check is read this way.
- With a `fee_minor` (other than 0) beside it, who bears the fee is not recorded, so the refund owed is not known. No amount is held, and the new `ActAuthorization.AmountNotHeld` says so, never as a match (evidencebook#743 tracks recording who bears the fee).
- An act that cites a check of the wrong kind is now told so before any amount is compared.
- PROFILE.md §8 says the same.
- `deal settle` no longer sends a new refund on a cancel check that states a cancellation fee. It used to build the full `cancelled_amount_minor`, which picked who bears the fee. It now refuses and says why (evidencebook#743). A refund already sent completes, or is written again, with the amount its send recorded, and a note says that amount is not checked.

### Fixed (breaking): x-deal-v1's payment check runs on the payment's own legs beside a refund (#722)

- **The bug.** `xdealv1.PaymentCheck` stopped with reason `refund_cites_offer` as soon as a refund or remedy leg (`reverses`, `remedy_for`) cited the offer's terms leg. A payer who paid too little could seal a 1-unit refund and turn the payment row from failed into not checked.
- **The fix.** The check now runs on the payment's own legs. Refund and remedy legs citing the offer's terms leg take no part, and those not superseded are listed in the new `PaymentResult.NotChecked`. A status row counts them in `check_not_checked`. This follows Steven's ruling on #722.
- A payment leg is now superseded only by a later payment leg answering the same terms leg that AAC's verifier reads beside the offer. Before, a refund, a leg of another terms leg, or a malformed leg carrying `supersedes` could take the payment leg out of the check, which read "not agreed" instead of failed. A refund is still superseded by any later leg of its side.
- **Breaking.** `ReasonRefundCitesOffer` is removed. The payment, status and report vectors are regenerated, with a new case `short-payment-with-refund-fails` and a new status case `payment-refund-not-checked`.

### Added: `xdealv1.StatusProvider.ReportSnapshot` (#626)

- `ReportSnapshot` returns a v1 report, the book pairs it rests on, and each payment row's payment check, all from one read of the shelf. A host that shows the checks beside the report never shows a check the report did not read.
- Rows whose owed is an amount but whose check was not run are listed with why: `not_recomputed`, `no_offer_pair` or `no_acceptor_key`.
- `ReportWithBook` is unchanged and now calls it.

## v0.26.0 (2026-11-21)

Every pull request merged since v0.25.0:

- [#623](https://github.com/action-state-group/evidencebook/pull/623): feat(goose): register the demo node's History card on a service the host names (#620)
- [#655](https://github.com/action-state-group/evidencebook/pull/655): test: parity table of exported Go APIs against Rust and Python, with a CI check
- [#658](https://github.com/action-state-group/evidencebook/pull/658): feat(capsulectl): book card register and card check for History card registration (#624)
- [#614](https://github.com/action-state-group/evidencebook/pull/614): feat(dealprofile): rule 12 counterparty copy withholds answers, tellings, share checks and the close (#613)

### Added: what a rule 12 counterparty copy withholds, as vectors (#613)

- `dealprofile`: PROFILE.md section 6 rule 12 "Copies" now lists every record a counterparty copy
  of a seller's deal withholds so that nothing in it says which of the seller's agent claims were
  authorized (evidencebook#613, following #607): besides the `claim_authority`, the represent
  checks, the evaluations and the unchecked outcomes, every answer to a check (`user_approval`,
  `card_answer`, whatever the answer), every share check (its `repeat_disclosures` and
  `recipient_told_before` count only tellings given with authority), every telling (a
  `disclosure`, or an `action-record/v0` with `disclosed`) and every `close` (its `unchecked_actions` counts the unchecked acts). The
  counterparty's acceptance and platform approval observations stay. The copy says how the deal
  ended from the outcome the close refs, or "not shared" when that outcome is an unchecked one. In a rule 12 counterparty copy of a v0 deal, the
  buyer's "under the seller's terms, pack X" line is no longer shown, since the evaluations that
  name the pack are withheld; the adjudicator copy keeps it. PROFILE.md states this as a known limit.
  New `CounterpartyWithheld` and `RequiredDisclosed` give, for a deal's records, the positions such
  a copy withholds under rule 12 and the ones it must disclose; `check_profile.py` computes the same
  and generates `x-deal-v0/fixtures/counterparty-copy-vectors.json` (twin deals that differ only
  in whether the ordered claim was authorized), which both are checked against. Known limit: an
  act the gate held that the agent did anyway is not in force, so the buyer's copy lacks the steps
  that rest on it (the offer, the buyer's acceptance, a commit) and its end outcome compares the
  delivery with the terms without that offer, as does a later check's `material_fields_changed`
  and a share check in a later deal with the same party (Steven accepted this limit); a buyer who
  knows those steps happened can tell they were not authorized, never why.

### Added: `capsulectl book card register` and `card check` (#624)

- `capsulectl book card register` registers a Standard History card on the transparency service a `--service` file names, signs the statement under the profile's key (which must be the holder key), and writes the statement with its Receipt for the host to keep beside the card; `card check` reports `card_registered` over those statement files under the keys the same service file states. Both call `RegisterCard` and `CheckCardRegistered` (evidencebook v0.25.0, agent-action-capsule `spec/standard-history-v0.md` §13). Registration is opt-in and has no default service: without `--service` nothing is posted. The client speaks one protocol, capsule-anchor's statement registration (`POST /transparency/register-statement`), so a service must answer it. After the post the service may have logged the statement even if the Receipt fails to check; the error then says so and no statement file is written. A pass shows that the card existed by a time and nothing else: `anchor_inclusion` stays `NOT_CHECKED`, nothing shows it is the holder's only card, and the log is append-only, so a registration cannot be withdrawn. `capsulectl/go.mod` requires evidencebook `v0.25.0` (was v0.23.0).

## v0.25.0 (2026-11-21)

Every pull request merged since v0.24.0:

- [#612](https://github.com/action-state-group/evidencebook/pull/612): fix(dealprofile): check_profile.py --regen keeps the hand-written negative fixtures
- [#617](https://github.com/action-state-group/evidencebook/pull/617): feat: register a Standard History card on a SCITT service and check card_registered (#616)

A minor version: the root package gains the opt-in registration of a Standard History card and the `card_registered` check. Nothing existing changes, and nothing registers unless the host configures a service.

### Added: registering a Standard History card (#616)

- Register a Standard History card on a SCITT transparency service, opt-in and with no default service ([#616](https://github.com/action-state-group/evidencebook/issues/616), agent-action-capsule `spec/standard-history-v0.md` §13): `BuildCardStatement` signs a card statement over the card's digest, `RegisterCard` has the host's `CardStatementRegistrar` post it to the service the host chose and attaches the Receipt only when it passes under the relying party's transparency policy (`AttachCardReceipt` checks it, at most 16 per statement), and `CheckCardRegistered` reports `card_registered`. Registration shows that the card existed by a time and nothing else: `anchor_inclusion` stays `NOT_CHECKED`, nothing shows the holder registered no other card, and the check is never a card property or a level. The library reaches no service itself, and the host keeps the statement beside the card.

## v0.24.0 (2026-11-21)

Every pull request merged since v0.23.0:

- [#608](https://github.com/action-state-group/evidencebook/pull/608): feat(dealprofile): seal the seller's verdict on an agent claim apart from the claim (#607)

A minor version: x-deal-v0 gains a record type, `claim_authority`, and a claim shape without a verdict. A checker older than this release refuses a book holding one, so a producer that seals it (evidencebook-deal#244) needs this version. Books and copies made before still verify unchanged.

### Added: the seller's verdict sealed apart from the claim (#607)

- `dealprofile`: a seller's verdict on its agent's claim moves off the claim into a new x-deal-v0
  record, `claim_authority` (`TypeClaimAuthority`, `ClaimAuthorityBody`), sealed right after it,
  so a counterparty copy can carry the claim's words without saying whether the claim was
  authorized (evidencebook#607). `CheckChain` holds section 6 rule 12 on
  the new record, compares the check's `valid_until` with the claim's time, accepts a deal that
  ends at a claim whose verdict is not sealed yet, keeps the claims sealed before (verdict on the
  claim) valid, and refuses such a claim once the deal holds a `claim_authority`. PROFILE.md rule
  12 says what each copy carries. A checker older than this refuses a book holding a
  `claim_authority`. Copies already shared still carry the verdicts they carried. New fixtures in
  `x-deal-v0/fixtures/positive-typed-seller-represent/` and `testdata/chain-vectors.json`.
  Known limit, accepted by Steven: a copy never carries the verdict, but a withheld record keeps
  its place in the log, so counting the withheld records before a claim can suggest, never prove,
  that the claim had no check.

## v0.23.0 (2026-11-21)

Every pull request merged since v0.22.0:

- [#601](https://github.com/action-state-group/evidencebook/pull/601): feat: follow Evidence Request -03 checkpoint_range (AAC go v0.24.0, evidence-request 0.8.0)

A minor version: the module requires agent-action-capsule go v0.24.0, which drops `Subject.Ranged`, so a consumer of this module moves its agent-action-capsule go pin to v0.24.0 with it, and a ranged checkpoints request is now `{"checkpoint_range": [a, b]}`: a requester still sending `{"checkpoints": [a, b]}` is refused `request_malformed`. capsulectl and report pin v0.20.1 and take this release in a follow-up.

### Changed: the ranged checkpoints request is `checkpoint_range` (#595)

- Requires agent-action-capsule go v0.24.0: the ranged checkpoints request is the `checkpoint_range` subject (`SubjectCheckpointRange`, wire `{"checkpoint_range": [a, b]}`) as Evidence Request -03 names it; `{"checkpoints": [a, b]}` is `request_malformed` and `checkpoints` is content-free again. The book answers and verifies `checkpoint_range` as it did the ranged form; the manifest's `subject_forms` stay the profile's six forms (a responder lists no token beyond its profiles', interop profile §11.4.2) and an answered statement still records the form as `checkpoints` (evidencebook#595). `Subject.Ranged` no longer exists upstream.

## v0.22.0 (2026-11-21)

Every pull request merged since v0.21.0:

- [#597](https://github.com/action-state-group/evidencebook/pull/597): feat(dealprofile): x-deal-v1 report relies on the payment legs, and the bundle extension carries them

A minor version: #597 adds `xdealv1` API and vectors; a payment row's `relied_on` now lists the settle legs, so a report differs from v0.21.0's for the same deal, and `BuildReport` now refuses a payment result that does not begin with the offer's digest or names a record the books given do not hold. A report sealed by v0.21.0 still verifies: `VerifyReport` does not recompute rows.

### Added: the payment legs in the x-deal-v1 report, and the `x-deal-v1` bundle extension (#597)

- `xdealv1`: a payment row's `relied_on` in the v1 report lists each settle leg `PaymentCheck`
  read, so a page shows the payment each leg records (evidencebook#588). `BuildReport` refuses a
  payment result that does not begin with the offer's digest or names a record the books do not
  hold. New report vectors in `testdata/deal-v1/report/`.
- `xdealv1`: the `x-deal-v1` bundle extension (`ReportBundle`, `ReportBook`): the sealed report's
  id, the holder's seq 1 agreement and the records the report's rows rely on, as book pairs.
  `StatusProvider.ReportWithBook` returns a report and its book from one snapshot, and
  `VerifyReportBundle` verifies a report with its extension. Vectors in
  `testdata/deal-v1/report-bundle/`.

## v0.21.0 (2026-11-21)

Every pull request merged since v0.20.1:

- [#590](https://github.com/action-state-group/evidencebook/pull/590): feat(dealprofile): x-deal-v1 admit rows take admission pack row ids
- [#593](https://github.com/action-state-group/evidencebook/pull/593): build(capsulectl,report): require capsule-cli v0.1.0-rc45

A minor version: #590 widens what the x-deal-v1 schema accepts and states the `admit` citation rule; no record valid before is refused.

### Added: x-deal-v1 `admit` takes admission pack row ids (#590)

- x-deal-v1 `admit` names admission pack rows as the pack names them (capsule-engine#303): `rows` takes `^[a-z][a-z0-9_-]*$`, the admission pack loader's grammar, so a baseline row such as `owner-signed` is valid; every record valid before stays valid. PROFILE.md states what `tier`, `rows`, `pack_digest` and `card_digest` hold, that a NO, which has no tier, is not sealed as an `admit`, and that an agreement cites a GO, or a LIMIT whose tier holds the deal: a host refuses to seal an agreement whose `admit` is ASK or NO (capsulectl-deal's check is evidencebook-deal#240). Vectors: `testdata/deal-v1/records/admit-rows.json`.

### Changed: capsulectl-book and the report builder build against capsule-cli v0.1.0-rc45 (#593)

- `capsulectl/go.mod` and `report/go.mod` require capsule-cli `v0.1.0-rc45` (the witness fix: `selectedWitness` scopes `PendingWitnessesFor` and delivers older rows first, capsule-cli#590). `capsulectl/go.mod` also requires evidencebook `v0.20.1` (was v0.14.2): agent-action-capsule go v0.23.0, which the bump brings, parses a ranged `checkpoints` subject (Evidence Request -03 as agent-action-capsule go v0.23.0 reads it), and v0.14.2 would have answered it with the whole chain; v0.20.1 answers the positions asked with a consistency proof to the anchor (`TestRespondAnswersARangedCheckpointsRequest`). The bump moves agent-action-capsule go to v0.23.0 and capsule-viewer go to v0.19.2, whose page runtime the committed monthly pages under `report/testdata/presentation` are rewritten with (`EVIDENCEBOOK_UPDATE_PRESENTATION_PAGES=1`: the runtime's version, commit and script hash only). A maintainer's `capsulectl/v0.1.0-rc45` tag on #593's merge commit pairs capsulectl-book with that capsulectl release.

## v0.20.1 (2026-11-21)

Every pull request merged since v0.20.0:

- [#591](https://github.com/action-state-group/evidencebook/pull/591): fix(goose): the mailbox receives a reply over 1 MiB, a poll reports a transport error, and ask prefers history_card/2 (#589)

### Fixed: the Goose reference's mailbox receives a large reply (#589)

- The Goose reference (`python/examples/goose`) reads a relay message up to the base64 of the largest reply a book accepts, where websockets' 1 MiB default closed the connection on a long history's `history_card/1` and the poll recorded an absence. A poll that could read no relay returns `transport_error` and records nothing, and `loop_ask_mailbox` asks `history_card/2` where the asked node's commitment offers it. No Go, Rust or wire change; details in `python/CHANGELOG.md`.

## v0.20.0 (2026-11-20)

Every pull request merged since v0.19.0:

- [#569](https://github.com/action-state-group/evidencebook/pull/569): feat(capsulectl): answer refusals uniformly, the library default (#473)
- [#578](https://github.com/action-state-group/evidencebook/pull/578): docs(design): an untimed deal is counted, local, unless its agreement names a witness (u608, #570, SH-12)
- [#582](https://github.com/action-state-group/evidencebook/pull/582): feat: RespondOptions.OnRefusal reports the decided reason locally; README names policy_declined (#580, #581)
- [#583](https://github.com/action-state-group/evidencebook/pull/583): feat(dealprofile): x-deal-v1 status value carries the first act (#575)
- [#584](https://github.com/action-state-group/evidencebook/pull/584): feat: CheckStaple reads a standard_history/3 staple, in Go, Rust and Python (#579)
- [#586](https://github.com/action-state-group/evidencebook/pull/586): feat(shelf): MCP and A2A entries for Shelf.Receive (#577)

**Consumers: regenerate pinned x-deal-v1 status values.** #583 adds `start.first_act` to every x-deal-v1 status value, so a status value's commitment changes. #583 is the breaking change that makes this a minor version (v0.20.0, not a patch): a consumer that pins or seals status values regenerates them before bumping its evidencebook pin.

**Consumers: refusal reasons.** Anyone parsing `capsulectl-book` refusal reasons now sees `policy_declined` where `not_authorized`, `no_such_subject` or `coverage_unsatisfiable` appeared (#569).

### Changed: capsulectl-book answers refusals uniformly (#473)

- `capsulectl-book` no longer sets `RefusalPolicySpecific`: it follows the library default (`uniform`, v0.14.0), so a refused request is answered `policy_declined` wherever `not_authorized`, `no_such_subject` or `coverage_unsatisfiable` would tell a requester whether the holder has a record for a key it does not hold. A holder's `holder-page --view asked` list shows the sealed code (`policy_declined`) as its reason.

### Added: the x-deal-v1 status value carries the first act (#575)

- `start.first_act` (`{"time": <status time>}` or `null`) is the holder's first `perform` or `settle` record for a deal whose counterparty runs no layer, so a reader of a status opening computes `Start` for such a deal from the fact. Go (`xdealv1.StatusStart.FirstAct`), Rust (`StatusStart::first_act`) and Python (`start["first_act"]`) give it byte for byte; the status vectors gain no-layer cases, and every status value now carries `"first_act": null` or the act, so a status value's commitment changes. A consumer that pins or seals status values (status vectors, status commitments) regenerates them before bumping its evidencebook pin.

### Added: the MCP and A2A entries of Shelf.Receive (#577)

- `Shelf.PushMCP` and `Shelf.PushA2A` (and `PushA2AExtension`) carry a record push and a stamp proof over MCP (the tool `record_push`) and A2A (one Agent Card extension, one `data` part) to `Shelf.Receive`, as `spec/bindings/record-push-v0.md` of agent-action-capsule specifies (evidencebook#577, #461 acceptance 4). Duplicates are acknowledged, gaps and forks answered, and a part's refusal is an answer; a malformed push is an invalid-params error and a receiver fault an internal error, never an answer. Vectors: `testdata/shelf/receive-carriage`.

### Added: a `standard_history/3` staple is read (#579)

- `DerivationStandardHistory3` names `standard_history/3` (SH §11), and `CheckStaple` accepts a staple of it over `full_history`, checking the card with the `standard_history/3` form (`standardhistory.CheckAs`). A book still builds no /3 card, since it holds no shelf facts: the host counts it from a shelf, and `Staple` refuses the token. `standard_history/1` and `/2` are unchanged.

### Added: `RespondOptions.OnRefusal`, the decided reason as a local diagnostic (#580)

- `RespondOptions.OnRefusal` is called once for each refusal `Respond` returns, with the request digest, the reason the gate decided and the reason the refusal carries. They differ only under the uniform refusal policy, which folds `not_authorized`, `no_such_subject` and `coverage_unsatisfiable` into `policy_declined`. It is for the holder's operator: never sealed, replied or served, and no byte of a reply, record or cut changes.

### Fixed: README named the wrong default refusal (#581)

- The README said an unauthenticated request for a protected subject gets `not_authorized`; under the default uniform policy it gets `policy_declined`.

## v0.19.0 (2026-11-19)

### Added: the x-deal-v1 sealed deal report (#461 step 5, PR 5)

- **`xdealv1.BuildReport(books, copy)`** computes one copy of the v1 report at `as_of` (design §9, PROFILE §10). The copy holds:
  - `start`, `settled`, `rows_state`;
  - each agreed row's rowstatus result, `check`, `late` statements and disputes, effective ruling and `relied_on` records;
  - `rests_on`.
- **`xdealv1.StatusProvider.Report(ctx, logID, copy)`** computes the same from a shelf, at the book's checkpoint. `xdealv1.ReportPayload` is its JCS payload, which a host seals with `dealprofile.ReportCapsule`.
- **The card members a page must not misread:**
  - `counterparty_kind` is null ("not stated").
  - `counterparty_proof` is `held`, `not_yet` or `no_layer` (u611).
  - `disputes` has one entry per dispute record.
  - `rulings_unnamed` is shown beside an `open` outcome.
  - `status_fact` says whether the book's last status fact is current.
- **The adjudicator's copy** withholds the rows, the counterparty's prefix, the witnesses, the disputes and the unnamed count.
- **Vectors:** `testdata/deal-v1/report/` and `testdata/deal-v1/settled/`.
- **A payment remedy is not counted yet,** because it needs its own terms leg. A ruled row whose ruling orders a payment reads remedy `not_counted`, never `due` or `not_honoured`, and the deal stays unsettled. `remedy_made` says whether a remedy for the row is held.
- **Snapshot rules:** a report refuses books holding a record timed after `as_of`. From a shelf, it rests on the book's latest checkpoint covering the whole book, and writes nothing.
- **`settled`:** an `agreed` row whose value is `not_met` counts toward it only with a remedy made (Steven's SH-16 ruling).
- **`Shelf.ExistingJournal(ctx)`** is the shelf's journal, or nil when the shelf has none. Unlike `Shelf.Journal`, it never creates one.

- **Added (`dealprofile/xdealv1`):** `VerifyReport` and `ReportPair`. A reader checks a sealed v1 deal report against its deal's first record (design note §9, "Verifying a report"): the report must be signed by the key that signed the agreement at seq 1 of the holder's deal book, of the deal and log the report rests on, and each row's `ruling.against` must be `buyer`, `seller` or `null` (evidencebook#461). The vectors are in `testdata/deal-v1/report-verify/`.

- `report.Build` (`capsulectl book report`) refuses a bundle carrying the `x-deal-v1` extension. An x-deal-v1 deal seals no Evidence Result, and its page reads the v1 deal report instead (#485).

### Changed: x-deal-v1 counts a deal whose counterparty agreed and then went silent (#511)

- **Start rule, design §5.2 step 7.** After `agree_by`, with both agreements joined, an offerer's agreement that has no record time and that the acceptor's book never names is now `started`, `local: true`, `by: null`, where it was `not_started` (Steven, u608: "count it"). The silent side's failure then shows in the status and on the card. A later witnessed time after `agree_by` still gives `not_started`, a naming gives `started` for good, and a timed late agreement that is not named stays `not_started`.
- **Go, Rust and Python** change together: `xdealv1.Start`, `deal::v1::start` and `evidencebook.deal.v1.start`, with the shared `testdata/deal-v1/start` and `report` vectors (a new `agree-then-silent` report case pins that its rows are listed).

- **Fixed (shelf):** `Shelf.CountedPrefix`, and the duplicate path of a receive, now read a sender's records and recorded forks in one read-only transaction (evidencebook#543). A receive that committed between the two reads could give a size and a fork position from different store states, and the x-deal-v1 receive gate reads that prefix.

- **Fixed (`dealprofile/x-deal-v0`):** the reference checker `check_profile.py` reads each schema pattern as Go's validator reads it, RE2 (evidencebook#548). A `$` no longer matches before a final newline, and `\s` takes only RE2's white space. On these inputs its verdicts now match Go's, the Python package's and the Rust crate's. A self-test holds it to the schema-pattern cases of `dealprofile/testdata/parity-vectors.json`.

- **Fixed (`dealprofile/xdealv1`):** `VerifyReport` refuses a report whose `rows`, `report` text, or a row's `relied_on` or late list is null, whose row `owed` is not an object, whose `rulings_unnamed` is negative, or whose `relied_on` record is `witnessed` without a `time` (or has a `time` and is not `witnessed`). Go's round trip had kept these, so a report with null `rows` passed although `BuildReport` never writes one (evidencebook#554).

- **Fixed (`dealprofile/xdealv1`):** `VerifyReport` refuses a report pair whose members are not exactly `record` and `agent_input`. Go's reader also took a member in another case, so a second `agent_input` spelled `Agent_Input` was read in place of the one beside it (evidencebook#554).

- **Fixed (`dealprofile/xdealv1`):** a pair and what it signs are read by their exact member names (evidencebook#559). Go's reader also took a member in another case, including U+017F for `s` and U+212A for `k`, with a later spelling replacing an earlier one. So a pair, its signed Capsule or its signed header could carry a second, unverified spelling that Go read in place of the one an exact or schema-checking reader takes.
  - `VerifySent`, `VerifyBook` and `VerifyReport` refuse a pair whose members are not exactly its own.
  - They read a Capsule's `action_id`, `action_type`, `references` and `agent_input_digest` by exact name.
  - `VerifyBook` refuses a header (or a link, correlation or reproduction in it) carrying a member its type does not have exactly, as `schemas/record-header.schema.json` does.
  - `PaymentCheck` reads a settlement member's `leg`, `sealer_role` and `terms_ref` by exact name.

  The vectors are in `testdata/deal-v1/pair-names/` and `testdata/deal-v1/payment/`.

- **Added (shelf):** `Shelf.StampTime` and `Shelf.StampTimeByKey` give the witnessed time of a stamp of the shelf's own, an x-deal-v1 status time `{"stamp": n}`: the earliest registration time, from the witnesses named, of the shelf checkpoint of tick `n` or a later one. The holder fills `xdealv1.ResolveRow`'s stamps with it for its own books' facts (evidencebook#572).

### Changed: an external-check finding's `limit` and `value` are money-cap amounts (#456)

- The x-deal-v0 external-check result schema and PROFILE now state that a checker MUST send a finding's `limit` and `value` only on a failing money cap (a per-action or rolling spend cap), both integers in minor units of the deal's currency with `value` greater than `limit`, and MUST NOT send them on a pass or on a count or floor rule. The deal card already read them this way; the schema's shape is unchanged, so no record changes schema validity, but a checker that sent them otherwise is now out of profile.

- A history_card/1 or unranged checkpoints answer whose stated `anchor.freshness` is not what the anchor checkpoint commits is now refused, as for the other answer forms, including a stapled history_card/1 (#521).

## v0.18.1 (2026-11-18)

### Fixed: `xdealv1.BookEntry` seals header `record_type` `deal_step`

- Since v0.18.0, `BookEntry` (and so `Receive`'s ruling naming) sealed a deal-book record's header with the payload's own x-deal-v1 type (`statement`, `settle` and so on). Design §3.1 and v0 say `deal_step`, with `epistemic_type` `producer_claim`; the payload's `record_type` says which x-deal-v1 type it is. x-deal-v1's checks, `VerifyBook`, the shelf and the status provider do not read the header's record type, so books already sealed stay readable. A host that queries a deal book by header record type (`Filter.RecordType`) finds `deal_step` from v0.18.1 and the payload's type on records v0.18.0 sealed; read the payload's `record_type` for the x-deal-v1 type.

## v0.18.0 (2026-11-18)

### Added: `BookStatusProvider`, handed the book and journal a stamp gives values for (#461 step 5, PR 4c-2b-1)

- A stamp holds the shelf's lock while it asks a status provider for a book's values. So a provider could not open the book or the journal it describes: `Shelf.Open` and `Shelf.Journal` take that lock.
- `BookStatusProvider.BookValues(ctx, StatusInput{N, BookID, LogID, Book, Journal})` hands over the stamp's tick and the handles the shelf already holds. `Journal` is nil before `Migrate`, and until the host first opens it with `Shelf.Journal`; a stamp never creates it. A provider reads through them (`Query`, `ResolvePayload`, `Sealed`) and through the shelf's lock-free readers, never another `Shelf` method.
- A shelf prefers it to `StampStatusProvider`, which it supersedes, and to `Values`.

### Added: x-deal-v1 status value and `StatusProvider` (#461 step 5, PR 4c-2b-2)

- `xdealv1.BuildStatus(StatusBooks)` builds a v1 deal book's status value (design §8.1). Its contents:
  - the deal's inputs, with no shared identifier;
  - each row's rowstatus input, carrying only the statements invariant A keeps;
  - times as a string, `null` or `{"stamp": n}` (`StatusTime`);
  - when the other side first named each statement;
  - the effective ruling's `against` and the first remedy made;
  - the payment check's result, reason and excluded count;
  - one entry per dispute record;
  - `counterparty_proof_held`.
- `xdealv1.StatusProvider` is a `BookStatusProvider` that gives the profile, link value and status value at each stamp:
  - it reads the counterparty's counted prefix and the adjudicator's case book lock-free (`HeldByDeal`, `CountedPrefix`);
  - it runs the payment check when the host gives the offer's sent pair (`OfferPair`).
- `xdealv1.ResolveRow` is a status row's input as a reader computes it once its `{"stamp": n}` times resolve, recomputing each dispute's `seen_in_time`. A ruling is timed by the earliest naming of its arrival in either book. PROFILE says a receiving library MUST name a counted ruling (#527).
- `Shelf.HeldByDeal(ctx, dealID)` lists the received records of a deal, lock-free.
- A status provider's error for one book no longer stops a stamp. That book keeps its last fact, the error is reported in `TickResult.Unstated`, and the other books' facts are written.
- `testdata/deal-v1/status/vectors.json` holds the cases `appendix-a`, `times-three-cases`, `unwitnessed-then-witnessed`, `carry-forward`, `seen-in-time-resolves` and `carry-forward-lower-bound`. Each case holds the books, the status value they build, and the row results a reader resolves from the fact; those results equal the full books' results.

### Added: x-deal-v1 `Receive` names a counted ruling's arrival; `AfterReceiveProfile`, `Book.AppendAt`, `BookEntry` (#527)

- **`Shelf.Receive` acts on what it took.** On every call, a duplicate included, it calls an `AfterReceiveProfile`'s `AfterReceive(ctx, result, at)` before it returns, once the parts committed (on a proof part's store error too, with the record part's result), and holds only the receive lock while it does. `RecordOutcome.DealID` is the deal the profile read the record as.
- **x-deal-v1 names rulings there.** Its `ReceiveProfile` runs `xdealv1.NameRulings` with `Changes.Grew` and the received record's deal, so a ruling sent again retries a naming that failed.
  - It seals, in the holder's deal book, a `statement` naming each counted ruling of the deal's adjudicator that the book does not name yet. PROFILE §5 now requires this, so a holder's own status reads a ruling whichever way it went and no host can skip it.
  - It builds each statement from a fresh read and appends it with `Book.AppendAt`.
  - It never writes to a book that is not wholly x-deal-v1 (a withheld payload, a lifecycle record); the status value counts that book's ruling unnamed.
  - It reports the record ids it sealed even on an error.
- **The next stamp retries a failed naming.** A `BeforeStampProvider`'s `BeforeStamp(ctx, at)` runs before each stamp, from `Stamp` and from a due `Tick`, outside the shelf's lock and holding the receive lock; its error is `TickResult.BeforeStamp` and the stamp is still taken. `xdealv1.StatusProvider` implements it with `xdealv1.NameAllRulings(ctx, shelf, at)`, `NameRulings` over every v1 deal book, so a ruling that was a deal's last record is named without the host.
- **`rulings_unnamed`** in the x-deal-v1 status value counts the counted rulings of the deal's adjudicator held and named by neither book (no ids; left out at zero), so an `open` outcome never hides one silently.
- **`Book.AppendAt(ctx, seq, entry)`** appends only as the book's next record, and gives `ErrBusy` otherwise. A profile whose record names its own place uses it to build that record from the tail it read. A stored record awaiting its substrate append takes its place first. Every x-deal-v1 deal-book write, a host's own included, uses it, rebuilt on `ErrBusy`.
- **`xdealv1.BookEntry(record, prevRecordID)`** is the entry that seals an x-deal-v1 deal-book record:
  - one payload, the record's JCS bytes, whose digest is the record digest;
  - its record type, with the deal id as the subject;
  - past seq 1, a cites link to the record before it.

## v0.17.0 (2026-11-18)

### Changed: x-deal-v1 `admit`, `check`, `verdict` and `approval` carry a nonce (u528)

- **The leak.** An agreement cites its `admit` by digest, and an act cites its `check`. Neither record held anything the counterparty could not guess, so it could test each possible admission outcome, or check, against the digest.
- **The fix.** `admit` and `check` now carry a required `nonce`: 256 random bits as 64 lowercase hex, fresh per record. So do the `verdict` and `approval` that an act's person's signature reaches. This follows Sherlock2's privacy rule: any journal record whose digest leaves the holder must carry its own nonce or a required salted member. A secret in a record it cites does not count. An `intent` already meets it with its required `sha256-jcs-nonce256` `verbatim_commitment`.
- **Where it is in the code.** The rule is in PROFILE §5 and the schema: any journal record whose digest leaves the holder carries its own secret, and records sent whole are exempt. `xdealv1.AdmitBody.Nonce`, `xdealv1.CheckBody.Nonce` and `check_profile.py` carry it. `xdealv1.NewNonce` draws a nonce from `crypto/rand`, and `CheckAuthorizations` refuses a journal in which two records share a nonce or a `verbatim_commitment`. `xdealv1.VerdictBody` and `xdealv1.ApprovalBody` are v0's bodies plus `nonce`.
- **Refs.** A deal-book record and an offer carry no block `refs`, so a record that leaves the holder cites only through its type's own members.
- **What changes for fixtures.** The Appendix A fixtures and their digests change, and ten negatives are added (nonce, verbatim_commitment, refs and payment_ref placement).
- **Not covered yet.** The Rust and Python x-deal-v1 ports (PR 6) will carry it too.

## v0.16.0 (2026-11-18)

### Added: x-deal-v1 payment check over AAC settlement legs, and `Entry.Members` (#461 step 5, PR 4a)

- `Entry.Members` adds top-level members to the Capsule the book seals for a record, such as a settlement leg. The header does not carry them, and a base-profile name or `evidence_record_header` is refused. `SealSent` puts them on the standalone sent Capsule only, never on the journal record's. `AppendCapsule` refuses them, since its caller seals the Capsule.
- `dealprofile.AssetScale` is the `assetScale` of a deal's `amount_minor` in a currency (it was the History card's internal rule).
- x-deal-v1: an offer whose terms have exactly one payment row carries the settlement terms leg on its sent Capsule (`xdealv1.TermsLeg`). `VerifySent` refuses such an offer without it, and any other `settlement` member. `xdealv1.PaymentCheck` runs a payment row's recomputed check with AAC's `settlement.Verify` over that terms leg and the two sides' deal-book legs. It passes on agreed, settled legs at the terms' amount, fails on a mismatch or on failed, reversed or another amount, and is otherwise not run. It is also not run when a refund or remedy leg cites the offer's terms leg or when the terms have more than one payment row. `RowBooks.Checks` carries a recomputed row's check to rowstatus.

### Added: a status provider is told its stamp, and a record's first covering stamp (#461 step 5, PR 4c-2a)

- `StampStatusProvider` is a `StatusProvider` that also has `StampValues(ctx, n, bookID, logID)`: n is the tick of the stamp it gives values for, the migration's stamp included. A shelf calls it in place of `Values` when its provider implements it, so a status value can name a record its own stamp is the first to cover (`{"stamp": n}`, design §8.1).
- `Shelf.RecordStamp(ctx, bookID, position)` and `Shelf.ReceivedRecordStamp(ctx, bookID, receivedLogID, position)` are the tick of the first fact that covers a record, or that first reaches a received one. They find that fact the same way `RecordTime` and `ReceivedRecordTime` do, and they take no lock, as those do.

### Added: x-deal-v1 first-act gate (Steven, u610 and u611)

- `xdealv1.ReceiveProfile.GateAct` refuses a party's first `perform` or payer `settle` of a deal until the counterparty's stamp proof covers the counterparty's agreement (`CanAct`). It applies on both sides, whether or not a witness is named. A payee leg, a statement and any later act are never gated. When the counterparty runs no layer, the first act goes ahead and is not refused. It is marked unguarded (`GateNoLayer`), and the receipt and the card say the other side sent no stamp proof (u611). PROFILE §8 and design §7 record the ruling, including that the offerer stamps its agreement at once when its first act waits on the acceptor's.

- **`history_card/2`** (Evidence Request -03 §8.1.2, evidencebook#507): the default first question for
  a node with many checkpoints. A card lists the first checkpoint, the checkpoints 1, 2, 4, 8 and so
  on positions back from the anchor and the latest `window` checkpoints in full (agent-action-capsule's `HistoryPositions`), each
  with its receipts and a consistency proof from the entry listed before it. Its size grows with
  the logarithm of the number of checkpoints, and `MaxAnswerItems` does not cap it; `history_card/1`
  lists every checkpoint and is refused past `MaxAnswerItems`. A book answers it to any requester over `checkpoints` or `full_history`, and
  `ResponderPolicy.HistoryWindow` (1 to 64, default 16) is the window it states. `CheckHistoryCard2`
  runs the spec's seven ordered checks, a requester verifies a /2 answer by them, and `Staple` and
  `CheckStaple` carry the card. A `checkpoints` subject with a range `[a, b]` (§8.1.3) is answered with
  the entries from a through b and verified by a requester. The manifest lists the derivation and
  draft -03. `history_card/1` is unchanged. Needs agent-action-capsule go v0.22.0.

### Changed: `report` and capsulectl-book render with capsule-viewer v0.19.1 (evidencebook-deal#220)

- The `report` and `capsulectl` modules require capsule-viewer `go/v0.19.1` (was `v0.18.2`), whose pages verify with agent-action-capsule 0.14.0 and cll 0.4.0. The report-build pages in `report/testdata/presentation/` (`4-monthly-outcome`, `4b-monthly-outcome-informational`, `5-monthly-compliance`) are regenerated with `capsulectl book report build`. Their new text is the viewer's own:
  - the totals note ("These totals count every rule in force…");
  - the month in the outcome report's title;
  - the "of 8" counts and the "Judged" chips;
  - "requirement" in place of "test" on the compliance page, and its numbered notes.
  - Each page still passes 10 of 10 checks.

## v0.15.0 (2026-11-17)

### Changed: checkpointed-local-log go/v0.4.0 (CLL 0.9.0, -02 checkpoint `sub`) (#452)

- The root, `capsulectl` and `report` modules require checkpointed-local-log `go/v0.4.0` (was `v0.3.0`). A checkpoint's CWT `sub` is now the bare log id, so every checkpoint pin changes: `testdata/derivation`, `testdata/standard-history-respond` and `testdata/shelf` are regenerated, and the third-party guide's pins, `sub` values and the invariance request's digest follow. `capsulectl`'s book witness store implements `PendingWitnessesFor`, which the CLL 0.9.0 witness-state contract adds.
- The responder interop program (`test/interop/responder`) writes each `log_keys` identity with the bare log id as `sub`, as the checkpoints it signs now carry (the Python profile verifier installs these as its trust policy).
- The History interop is pinned to agent-action-capsule `f143a18` (`vectors/personal-history-basic-v0` regenerated for the bare checkpoint `sub`, 23 cases): `test/interop/personal-history/main.go` and `testdata/vectors.json` are that commit's `producer/main.go` and `vectors.json`, unchanged (`producer.diff` is removed), and the CI jobs and §6 of `docs/THIRD-PARTY-VERIFIER.md` read that commit. The shelf vectors added on main since are regenerated on checkpointed-local-log `go/v0.4.0`.

- `rowstatus` (new package): `Compute` gives the status of one term row of a deal as of a time, from the two sides' statements and the dates: not reached, not due, no statement, waiting, uncontested, agreed so far, agreed, in disagreement, in dispute or ruled (with the remedy's state), and whether the result is local because a record it rests on has no time. The in-time rule, the single concession, late disputes, withdrawals, conflicting final rulings and dates that hang on a named record are as evidencebook#461 acceptance 6 states. `testdata/row-status/vectors.json` holds the shared vectors. Additive; nothing else reads it yet.

- The entry documents of a shelf log of book facts (#461, `docs/design/461-shelf-facts.md`): a book fact and a journal fact with their salted link and status commitments and openings (`FactOpening`, `Verify`), the stamp entry, the format-switch marker, `EntryID`, and `OpeningsFor`, which says which opening each audience may have. `testdata/shelf/facts/vectors.json` holds the shared bytes. Additive: no shelf writes them yet.

- The whole-shelf handover and its replay (#461, `docs/design/461-shelf-facts.md` §2.7). `Cadence.Handover` hands a reader the holder chooses every entry of the shelf log up to a stamp's checkpoint, the entry documents after the format switch with each book fact's status opening, the book checkpoints the facts carry, the consistency proofs between a book's facts, and the witness receipt the holder holds. `VerifyHandover` replays it under the holder's key and the reader's witness directory: it recomputes the anchor's root from the entries and fails when an entry is held back, a document is left out or changed, a status opening does not open its commitment, a book checkpoint is not the holder's, or a book's facts break their chain (a replaced book). It returns each book's latest fact and status opening, where coverage starts, whether a witness receipt verifies (a shelf with no witness is recomputed and unwitnessed), and the books whose genesis fact is not tied to the old witnessed tree; ties are not handed over yet, so every book is reported untied. Link and journal openings are never handed over.

- The shelf log of book facts (#461, `docs/design/461-shelf-facts.md` §2.4, §2.5 and §4). `Cadence.Migrate` switches a shelf for good. It first closes the file to older builds by moving it to shelf file version 2, then writes `<path>.v1.bak`, a version 1 copy an older build still opens. It then appends the format-switch marker, a genesis fact for each log, and a stamp entry. It can be resumed at every step. After the switch, `Cadence.Stamp`, and the heartbeat `Tick`, append a fact only for a log whose checkpoint grew or whose committed values changed (the `CadenceConfig.Status` provider gives the values), then a stamp entry, so every stamp gets a new checkpoint. A stamp is a cadence tick whose leaves carry its batch. Stamps are therefore recovered, witnessed and used as `standard_history/1` and `/2` anchors as ticks are. `Shelf.Openings` hands out a fact's openings by audience. This build opens shelf file versions 1 and 2; an older build refuses version 2.

- The shelf's journal (#461, `docs/design/461-shelf-facts.md` §2.2 and §3). `Shelf.Journal` opens the journal, a book of kind `journal`, one per shelf, on first use, on a shelf of book facts. A stamp writes it a journal fact when it grew: `size`, `checkpoint_sha256` and `prev_fact_size` are zero in the clear and their true values are inside its status commitment, whose opening goes only to the owner and an auditor. The journal is not one of `Shelf.Books`, has no extent on a `standard_history/1` or `/2` card, and a handover carries none of its openings. `Shelf.Open` and `Shelf.HostLog` refuse the journal's log id, `journal`. A file whose `shelf_logs` table predates the kind gets the table rebuilt, with the same rows, when the journal is first opened.

- The shelf's store of what the other side pushes (evidencebook#461 §3): `Shelf.Receive` takes a record, a
  `StampProof`, or both, each part on its own, and returns the deal ids with records that entered or left their
  sender's counted prefix, those with new fork or checkpoint evidence, and those whose records a held proof
  newly covers (once the records reach its size and recompute its root). A `ReceiveProfile` (in
  `ShelfConfig.Receive`) reads each record and holds the push rule, so the library knows no record format. The
  store acknowledges a duplicate, stores a gap without counting it until it is filled, keeps both records of a
  fork as evidence and ends the counted prefix before it for good (a deletion never undoes it; evidence is kept
  once per position), and bounds record size, records per book and unanswered unsolicited offers (dropping the
  oldest first). Before an offer is dropped the profile's `Answered` is asked, outside the write transaction,
  under a mark of the holder's own books; a mark that moved before the transaction plans the drop again
  (`ErrBusy` after three tries). What unsolicited offers leave behind (the positions of dropped offers, and
  conflicts with their evidence in logs of offers alone) is bounded per sender and in total
  (`ReceiveBounds.MaxOfferEvidencePerSender`, `MaxOfferEvidence`); an offer belongs to no deal, so past the
  bound no row is kept and such a position may later be forgotten. What `Forget` leaves, and conflicts in logs
  holding other records, are kept for good. A record arriving where a deleted record was (`Forget`, or a dropped
  offer) is a fork there, and a deletion returns the proofs reaching it to unchecked. A proof is refused, and
  its evidence kept, when it contradicts a held proof at the same size or the records held; every accepted proof
  is kept. `Cadence.StampProof` builds a proof for an audience and `VerifyStampProof` checks one. `Shelf.Forget`
  deletes a settled deal's records under the holder's `Retention` (by default the life of the shelf, so never),
  leaving tombstones that read as `ErrDeleted`. `CountedPrefix`, `ReceivedRecordOf` and `Evidence` read the
  store without the shelf's lock, so a `StatusProvider` may call them during a stamp; the shelf's handle now
  keeps four connections. Works on version 1 and 2 shelves. `testdata/shelf/receive` holds the store's vectors:
  a test profile, a script of steps with each step's expected outcome, and the store's dump after it.

- Record time (evidencebook#461 §2.6): `Shelf.RecordTime(bookID, position, witnesses)` returns the earliest
  registration time, among the receipts held from the named witnesses (their receipt URLs), of a shelf
  checkpoint covering the first fact of the book whose size covers the position, or no time. `Witnessed` keeps
  every verified receipt in the new `shelf_receipts` table, one per witness with its registration time, the
  earliest kept; a witnessed tick missing there is filled in at the cadence's next open. `Handover` carries every
  receipt held for its anchor, and `VerifyHandover` reports `Witnessed` only for a receipt that gives a
  registration time.

- **`Book.Sealed(ctx, recordID)`** returns a record as it was sealed: its canonical header, its
  Capsule (without signature) and its Producer Envelope, as the store keeps them, in the caller's own
  copies. They are what another party verifies the record by on its own. The x-deal-v1 profile sends
  a deal-book record to the counterparty in this form (evidencebook#461 step 5, design note §8.3).

- **The library calls the x-deal-v1 profile needs** (evidencebook#461, `docs/design/461-deal-profile.md`
  §11.1, PR 1). Each is additive and profile-neutral:
  - `rowstatus.ComputeLate` lists, by index into the input, the statements and disputes the rule
    sets aside as late as of T, beside `Compute`'s result, which is unchanged
    (`testdata/row-status/late.json`, hand-written from the rule).
  - `Shelf.SealSent` appends a sent journal record and returns its standalone Capsule, signed by the
    journal's key, so a record that leaves the shelf whole never shows its place in the journal.
  - `Shelf.HeldFirst` returns every received record at position 1 of a log, across senders and both
    records of a fork, and `Shelf.HeldProofs` every stamp proof held from a sender, re-verified, with
    the records it covers against the counted prefix now (none after a fork at the genesis). Both
    take no lock.
  - `Shelf.ReceivedRecordTime` is a received record's time from the holder's own stamps: the first
    fact whose link value reaches the record's position, and the earliest named receipt covering it.

- A whole-shelf handover now carries the receipt history a reader needs for record times
  (evidencebook#461, `docs/design/461-deal-profile.md` §8.1, invariant C). `Handover.Witnessed` lists every
  shelf checkpoint before the anchor that the holder holds receipts for, with those receipts.
  `VerifyHandover` checks that each is the holder's checkpoint of the shelf log, within the anchor, and
  recomputed by the handed-over entries. The new `VerifyHandoverTimes` gives a book position the record time
  the holder's `RecordTime` gives, even when the earliest receipt is not the anchor's
  (`testdata/shelf/handover/earliest-receipt-not-anchor.json`).

- **A witness's receipts are kept per key** (evidencebook#492). `shelf_receipts` is keyed by
  checkpoint, witness URL and directory key id, so a witness whose directory row lists two keys keeps
  a receipt under each for the same checkpoint, and `RecordTimeByKey` reads each key's own time.
  Before, one receipt per URL was kept and the other key's was lost. Opening a shelf whose
  table has the old key rebuilds it in place and keeps every row; the shelf version is unchanged.
  The handover carries every receipt held, ordered by URL and then key.

- **Record times by witness key id** (evidencebook#461; Steven, #482 Q1, u604: an x-deal-v1 agreement names each
  witness by its verified key id, with its URL only a hint). `Shelf.RecordTimeByKey`,
  `Shelf.ReceivedRecordTimeByKey` and `HandoverTimes.RecordTimeByKey` give the times `RecordTime`,
  `ReceivedRecordTime` and `HandoverTimes.RecordTime` give, with witnesses matched on the key each receipt verified
  under rather than its URL, so a witness that moved stays the same witness. The URL readers are unchanged.

- The whole-shelf handover ties each genesis fact to the old witnessed tree when the holder can
  (evidencebook#461 §2.7): `Handover.Ties` carries, per book, the old witnessed tick's leaf and path, its
  root's position in the shelf log, the cadence checkpoint and its receipts, the old book checkpoint and a
  consistency proof to the genesis checkpoint. `VerifyHandover` checks each tie and fails on one that does not
  verify; a genesis fact without one is still reported in `Untied`.

- **x-deal-v1 acts are authorized as v0's were** (evidencebook#461, `docs/design/461-deal-profile.md` §3.2,
  invariant D; §11.1 PR 2c). `xdealv1.CheckAuthorizations(journal, acts)` holds a deal-book perform or settle to
  v0's rules 5 and 6, through the check it cites:
  - the check has exactly one verdict, the verdict's first approval proceeds, and a deny authorizes nothing;
  - a standing intent answers only a passing verdict, for an action in the allowed in force;
  - one approval authorizes at most one act;
  - a change of identifiers between the check and the act needs a new check.

  Each act's `ApprovalDigest` is defined and binds the check and the card that was answered, and
  `NeedsPersonSignature` marks a person's own approval, which the agent's record key never stands for.
  Vectors: `testdata/deal-v1/authorization/approval-migration.json`.

- **x-deal-v1's Python reference checker** (evidencebook#461 step 5, PR 2e). `dealprofile/x-deal-v1/check_profile.py`
  checks every x-deal-v1 fixture on its own, independent of the Go package: the canonicalization, schema
  and record stages, the agreements against the offer, the dispute and rulings against the agreement, each
  book's links, and that every negative is refused at its stage. CI's `deal-profile` job requires "ALL OK".
- x-deal-v1's schema admits a row's `recomputed` only as `true`: Go already refused `false`, which it
  would not encode back, so every implementation now refuses it at the schema stage (PROFILE.md §1).
  Two negatives pin it and a pattern's `$` as the end of the text (`neg-recomputed-false`,
  `neg-deal-id-trailing-newline`).

- **x-deal-v1 agreements carry the recourse clause** (evidencebook#461; #404). An offer and its agreement name
  `forums`, the forums both sides accept for a dispute. The list is empty exactly when there is no adjudicator,
  which allows no dispute, and the agreement's forums must equal the offer's. `xdealv1.CheckDispute` and
  `CheckRuling` refuse a dispute or ruling outside them, so no forum is picked at dispute time.

- **A standing-intent act rests on a signed first intent** (evidencebook#500, Steven's option b).
  `xdealv1.CheckAuthorizations` returns, for an act a `standing_intent` approval authorizes, the record
  digest of the first intent it rests on (`ActAuthorization.Intent`). The caller refuses the act until the
  person's signature over that digest is held, as for the approval and the `confirm_limits` approvals.
  A `standing_intent` approval with no intent before it in the journal is refused. The approval-migration
  vector gains `intent_signature` per authorized act, and a refused case.

- **x-deal-v1's normative text and fixtures** (evidencebook#461 step 5, PR 2d). `dealprofile/x-deal-v1/PROFILE.md`
  states the record rules the `xdealv1` checks apply: the block, where `deal_id` is present, the record
  types and where each is sealed, the bodies, `seen`, records sent whole, the authorization of acts, and
  the stages a refusal names. `dealprofile/x-deal-v1/fixtures/` holds the integrated design's Appendix A
  deal record by record (both journals, both deal books, the adjudicator's case book, the path settled
  without a ruling, and a declined and a cancelled offer), each with its record digest, and 45 negatives,
  each one positive record with one change and the check and stage that refuse it. `TestFixtures` builds
  and checks them (`EB_WRITE_DEAL_V1_FIXTURES=1` writes them). The Python reference checker for v1 follows.

- **x-deal-v1's receive profile and the coverage gate** (evidencebook#461 step 5, PR 4b).
  - `xdealv1.BookPair` and `VerifyBook`: a deal-book or case-book record travels as its Capsule with its
    signature and key, the header the Capsule commits, and the record the header commits. The Capsule
    comes from `Book.Sealed`.
  - `xdealv1.ReceiveProfile` implements `evidencebook.ReceiveProfile`:
    - `Read` places a book record at its book log and seq, and a sent record in its own one-record log;
    - `Admit` applies EBL's current push rule (design note §8.3), which is not ruled yet (Forms SF-6);
    - `Answered` says whether our agreement, decline or counter-offer cites a held offer;
    - `ProofSender` relates a stamp proof to the counterparty whose agreement opens the log, refusing a
      fork at position 1 and anything but exactly one candidate.
  - `ReceiveProfile.CanAct` computes the gate before a first act of a deal. Whether a first act must wait for
    it is not ruled yet, so the caller decides whether to apply it. The counterparty's agreement must be
    of that deal, signed by the key it expects and citing its offer, and counted now. A held stamp proof
    bound to it must cover it: its book id, its checkpoint key, and its shelf log, which is also the
    log the counterparty's evidence commitment names when one is given.
  - A v1 deal book needs no authenticated indexes, so its log holds the deal's records only.

- **x-deal-v1 records, Go first** (evidencebook#461, `docs/design/461-deal-profile.md` §2.3, §3; §11.1 PR 2a).
  New deals stay x-deal-v0 by default; x-deal-v0 is unchanged and stays readable.
  - **The package:** `dealprofile/xdealv1` defines the ten record types (propose, agree for an agreement,
    decline or cancellation, perform, settle, statement, dispute, ruling, ask, answer, admit), the journal
    overlay (intent, check with `payment_ref`, verdict, approval), `rules_verdict` and the journey overlay.
    The overlays reuse v0's bodies.
  - **Reading a record:** strict `Decode`, and `Encode`, `Check` and `Digest`.
  - **The record checks:** where each record is sealed (deal book, case book or journal), `deal_id` presence,
    `seen` on agreements, and the rows. `CheckAgreement` reads an agreement against its offer.
  - **The schema:** `dealprofile/x-deal-v1/x-deal-v1.schema.json`.
  - **Telling the versions apart:** `dealprofile.Version` says whether a record is x-deal-v0 or x-deal-v1, so a
    reader dispatches on it.
  - **Vector:** `testdata/deal-v1/records/deal-id-presence.json`.

- **x-deal-v1 row inputs and the statements a fact carries** (evidencebook#461 step 5, PR 4c-1).
  - `xdealv1.RowInputs` projects the holder's deal book, the counterparty's counted prefix and the
    adjudicator's case book onto one `rowstatus.Input` per agreed row: the row, the named records by
    `marks`, each side's statements, disputes with `seen_in_time`, withdrawals, rulings and non-payment
    remedies. The payment check and payment remedies follow with `PaymentCheck`.
  - The statements a status fact carries are those `rowstatus` can still read at or after the fact.
    A randomized differential test against `rowstatus.Compute` holds that.
  - Design note §8.1 defines the status value's times: a known time, `null`, or `{"stamp": n}`.

- **x-deal-v1 offers, declines and cancellations travel whole** (evidencebook#461,
  `docs/design/461-deal-profile.md` §3.1, §3.5; §11.1 PR 2b).
  - `xdealv1.Pair` builds the pair a sent record travels as from its standalone Capsule, envelope and
    record: `{"record": <the Capsule with its signature and key_id>, "agent_input": <the record>}`.
  - `xdealv1.VerifySent` checks such a pair on its own:
    - the Capsule and its signature, and that it is a standalone `"<deal_id>/sent"` fyi Capsule;
    - that `agent_input` is the record it commits;
    - that the record is a valid offer, decline or cancellation.
  - Given the offer, a decline must answer it and be signed by the key the offer was sent to, and a
    cancellation by the offer's signer.
  - Vectors: `testdata/deal-v1/decline/vectors.json`.

- **`xdealv1.StartFromBooks`** (evidencebook#461 step 5, PR 3b) derives `Start`'s input from the two deal
  books and computes it. It reads the holder's own book, the counted prefix of the counterparty's book,
  and the offer, each record with its record id and the times witnesses gave it. The offerer's agreement
  has a time only from a witness the agreements name. A record of the acceptor's book names the offerer's
  agreement when its `seen` is that agreement or a later record of the offerer's book; a cancellation
  named before that means not started. With no layer, the holder's first act starts the deal.
  `testdata/deal-v1/start/books.json` holds 23 book-level cases, including a cancellation the acceptor named with none of the offerer's book held, a time from an unnamed
  witness and an empty witness list. Reading the books from a shelf comes with the ReceiveProfile (PR 4).

- **When an x-deal-v1 deal has started** (evidencebook#461 step 5, PR 3a). `xdealv1.Start(StartInput)`
  computes `started`, `awaiting_agreement` or `not_started`, whether the result is local, and what
  started it (`offerer_in_time`, `acceptor_named`, `first_act`, or null while nothing has yet), from the values of design note §5.2: the
  two agreements' match, the offerer's agreement naming the acceptor's, its record time against
  `agree_by`, the acceptor's book naming it or a cancellation first, and, with a counterparty that
  runs no layer, the holder's first act. It refuses inconsistent input, such as a held counterpart
  agreement with an unknown match. `testdata/deal-v1/start/vectors.json` holds 21 cases.
  `StartFromBooks`, which reads these values from the two books, follows.

## v0.14.2 (2026-11-16)

Go module only. Pull request since v0.14.1: #464 (the temporary `outcome_note` record; entry cites its issue #462). Cut for the 2026-11-19 Goose ARD demo.

- **TEMPORARY** x-deal-v0 `outcome_note` record (evidencebook#462; evidencebook#461's computed row status supersedes it): the book owner's own note, sealed after the final close, that the deal did not hold. The History deal entry's `outcome` is `did_not_hold` for a deal whose log holds one (`dealprofile.StandardHistoryDeal`).

## v0.14.1 (2026-11-16)

Go module only. Pull request since v0.14.0: #454 (the `sale_unpost` record; entry cites its issue #451).

- `dealprofile`: a sale's own log takes a `sale_unpost` record, a listing taken down (PROFILE.md section 6, rule 15). Its body is `platform` only, it needs no check, evaluation or approval, and its one `unposts` ref names the live post act of that platform (the latest post on it, not already taken down); a later post makes the platform live again, and a taken-down listing no longer counts in `listing_classes`. It is a record type of its own, not an `unpost` action beside `post`, because an action record rests on a check and an approval and a takedown needs neither; it is refused after `sale_close`. The record type is new, so a verifier that predates it refuses a sale that holds one; a sale without one verifies as before (no `rule_inputs_version` change). New fixtures: `positive-sale-unpost/` and the `neg-sale-unpost-*` negatives (#451).

## v0.14.0 (2026-11-13)

Go module only. Pull requests since v0.13.9: #433 (`uniform` is the default refusal policy, breaking), #432 and #434 (diagnostic page and `report build --diagnostic`), #435 and #439 (pin agent-action-capsule go v0.21.0) and #445 (the `rules_verdict` record).

### Changed: pin agent-action-capsule go v0.21.0; evidence-interop/2 is still refused (#436)

- The module pins `agent-action-capsule/go` v0.21.0, whose `evidencerequest.Profiles` also names `evidence-interop/2`. The book does not implement `/2`, so a request naming it reads as an unknown profile everywhere the book reads a request (answering, `RequestSent`, `CheckStaple`), and the signed refusal's detail still reads `profiles supported: evidence-interop/0, evidence-interop/1`; it is never answered as `/0`.

- `capsulectl book report build`: `--diagnostic` writes a marked diagnostic page for a bundle that fails verification or is incomplete, and the command still exits with the bundle's own failure; the result gains `status_line`, `records`, `records_verified`, `claims_not_evaluable`, `diagnostic` and `diagnostic_reasons` (counts with denominators, no percentage). `--diagnostic` cannot be combined with `--seal` or `--permalink` (evidencebook#111).

- `report`: `View.Diagnostic` writes a marked diagnostic page for a bundle that does not verify (title, banner and card block say so; the viewer draws its failed-bundle callout), with no permalink and no seal; `Page.Failure` returns the error the bundle earns. `Page` carries `Records`, `RecordsVerified` and `StatusLine`, counts with denominators and no percentage. `Build` runs pre-publish page checks (no foreign script, pinned inline scripts, a chart text alternative, the core runtime, language, viewport, title and print stylesheet) and writes nothing when one fails; a chart view without a `description` is now refused (evidencebook#111).

### Changed (breaking): `uniform` is the default refusal policy (#225, #206 row 8)

- An empty `ResponderPolicy.RefusalPolicy` now means `RefusalPolicyUniform`: a responder answers `policy_declined` wherever a more specific token (`not_authorized`, `no_such_subject`, `coverage_unsatisfiable`) would tell a counterparty whether the holder has a record for a key it does not hold. The manifest declares `refusal_policy: "uniform"` for the zero value. `RefusalPolicySpecific` is an explicit operator opt-in. To keep the old answers, set `ResponderPolicy{RefusalPolicy: RefusalPolicySpecific}`. Rust and Python change the same way (see their changelogs).

### Added: x-deal-v0 `rules_verdict` record, the party's own overall rules word (#444)

- x-deal-v0: a deal-level `rules_verdict` record carries one word (`met`, `not_met`, `unknown` or `out_of_scope`), the pack's `pack_digest` and, only for a published pack, its `pack_id`. At most one per deal, sealed after the final `close` and confirming it (a late record like evidence). It carries Steven's ruling for evidencebook-deal#191 (Buzz 8b6aaecf) and adds no meaning beyond it: the word is never derived here, re-run, counted or combined. Go (`dealprofile.RulesVerdictBody`), Rust (`RulesVerdictBody`), Python names, the schema, `check_profile.py`, fixtures and vectors carry it.

## capsulectl/v0.1.0-rc39 (2026-11-06)

capsulectl-book only; no library tag.

### Changed: capsulectl-book `v0.1.0-rc39`

- `capsulectl/go.mod` and `report/go.mod` require capsule-cli `v0.1.0-rc39` (the version this release pairs with). No change to the plugin since `capsulectl/v0.1.0-rc38`: it exists so the plugin pairs with the new capsulectl release, whose install check refuses a plugin built for another one.

## capsulectl/v0.1.0-rc38 (2026-11-06)

capsulectl-book only; no library tag.

### Changed: capsulectl-book `v0.1.0-rc38`

- `capsulectl/go.mod` and `report/go.mod` require capsule-cli `v0.1.0-rc38` (the version this release pairs with). It also carries what merged since `capsulectl/v0.1.0-rc37`: #415, #418 and #420 (the conduct refusal) and #403 (`book bundle-diff`), below.

### Added: `capsulectl book bundle-diff` renders one history at two disclosure levels (#403)

- `capsulectl book bundle-diff --higher A.json --lower B.json [--checkpoint-key HEX]` reads two held bundles of the same log and lists the records and payloads the lower one drops (`DiffBundles`). It reads files only: no profile, nothing is sealed.
- It refuses a bundle that does not verify, bundles of different logs or signed under different checkpoint keys, a `--checkpoint-key` that did not sign them, and a `--lower` that discloses something `--higher` does not (it is then not a lower level).
- Only records and payloads are compared: an interval-coverage leaf whose header a bundle does not carry is not, and the verb does not detect a fork.

### Changed: `capsulectl-book` requires `evidencebook` v0.13.9 (#415, #418, #420)

- The conduct refusal now covers every record type, not only a Result: `bundle`, `disclose`, `permalink` and a Close's `--bundle-out` refuse an observation or an evidence_request that cites a conduct pack. `book respond` hands the pack list to the book, so a record that cites one is refused as an unknown id is.

## v0.13.9 (2026-11-06)

Go module only. Pull requests since v0.13.8: #427 (`external-check-input/v0` carries `received_cards`; `external-check-result/v0` may carry an unsealed `notice`) and #428 (how the deal check treats them). The `capsulectl-book` entries pending in `changelog.d/` (#425, #424) and the Rust entry (#426) belong to those packages' own releases.

### Added: `external-check-input/v0` carries `received_cards`; `external-check-result/v0` may carry an unsealed `notice` (evidencebook-deal#166)

- `dealprofile/x-deal-v0/external-check-input-v0.schema.json` allows an optional top-level `received_cards`, a non-empty array of the cards the host received about the deal's counterparty, each with its JSON values, numbers and key order as received, given only to the profile's own rules checker. `external-check-result-v0.schema.json` allows an optional `notice` (`{line}`), the checker's one-line reading of those cards for the person.
- Neither is sealed or shared. Nothing derived from the cards may reach any other member of the result (the sealed verdict, findings, tier, grade, ruleset id and definition digest), and an unreadable card is never a refusal. A host sends no `received_cards` until its checker accepts the member (the input schema is closed), and a checker prints `notice` only when it was given cards, so a deal check and a checker that do not use them behave as before.

### Changed: the deal check seals the run given no cards; `received_cards` go to a second run read for `notice` alone (evidencebook-deal#166)

- `dealprofile/x-deal-v0/PROFILE.md` and `external-check-input-v0.schema.json` state how the deal check treats `received_cards`: the sealed result comes from a run of the checker given no cards, and when that run was evaluated, a second run given them is read only for its `notice`, so nothing a card could change reaches a sealed result. No schema member changes.

## v0.13.8 (2026-11-06)

Go module only. Pull requests since v0.13.7: #415 (capsulectl refuses a conduct Result in a bundle), #419 (`DiffBundles`), #420 (the conduct refusal for every record type, `Book.Respond`, and the workspace default).

### Added: `DiffBundles` lists what a lower disclosure level drops (#403)

- `DiffBundles(higher, lower)` verifies two bundles of one history and lists the records whose header the higher discloses and the lower does not, the payload digests the lower leaves out, and anything the lower discloses that the higher does not (`Extra`, so a bundle that is not a lower level of the other is not mistaken for one). A level is a scoped bundle; the difference is read from what the two bundles carry, not from a policy. Interval-coverage leaves whose header the bundle does not carry are not compared, and only records and payloads are compared, not a bundle's other extensions. A bundle that does not verify, whose anchor is not authenticated, or that comes from a different log than the other is `ErrInvalid`.

### Added: `capsulectl-book` refuses a conduct pack's records in a bundle (#414, #418)

- `bundle`, `disclose`, `permalink` and a Close's `--bundle-out` hand the book the private conduct pack ids, so a bundle that would carry a record citing one is refused with the book's `ErrInvalid`, and no bundle is written. The ids come from `packs/*/pack-source.yaml` in the current directory with `scope.kind: conduct` (read as `pack_id`, version cut at `@`), and from the pack ids in `<profile database>/book/conduct-packs` (one per line; blank lines and `#` comments skipped); a pack source that cannot be parsed, a conduct source without a `pack_id`, or an unreadable list also refuses. Run from a directory with no `packs/`, the verb prints a warning on stderr that no workspace conduct packs were read (the list file still applies). For a Close, the Close is already sealed when its bundle is refused, and a conduct record just outside the window (an edge record) is left out of the bundle instead. There is no owner-copy path: the CLI bundles only for others. The kind is local policy; no record, Result or compiled output changes.
- Until `capsulectl` requires an `evidencebook` release with the library change of #418, the book itself still checks only `evidence_result` records.

### Changed: the conduct refusal covers every record type (#418)

- `Book.Bundle` and `Book.StandardHistoryBundle` now refuse, for anyone but the owner, any selected record whose `subject_ref` names a listed conduct pack, whatever its `record_type` (an `observation` or an `evidence_request`, not only an `evidence_result`), and any record whose statement holds a `contract_ref` member, at any depth, that names one. This replaces the `evidence_result`-only check of #412. `ErrInvalid` names the record type and pack. The pack ids are still the ones the caller lists in `ConductPacks`. No record, Result or compiled-output member changes.
- `RespondOptions.ConductPacks` is the same list for `Book.Respond`: a record that cites a listed pack, whatever its type and whoever sealed it, is treated like the holder's other private records (`neverServed`). No subject form or derivation serves it, and a request that names it gets the refusal an unknown id gets, where a conduct Result was served digest-only before. An exchange named by its id, and a lapsed retention commitment on it, are refused the same way. A correlation holding one serves the rest. `A2AOptions` and `MCPOptions` take the same `ConductPacks`. A pack id holding `@` or empty is refused with `ErrInvalid`, as for a bundle. Built-in derivations read only servable records, so they leave it out too. The kind is local policy: no record changes.

## v0.13.7 (2026-11-05)

Go module only.

### Added: carry witness receipts in a bundle (#397)

- `BundleRequest.Witnesses` and `BundleRequest.WitnessDirectory`: the witness receipts (`checkpoint.witnesses` entries: `ts_url`, `entry_hash`, `receipt_b64`, `leaf_index`, `tree_size`) for the anchor pinned by `At` (required with them), and the directory they are checked under. `Book.Bundle` checks each receipt against the anchor's signed checkpoint before building and refuses (`ErrInvalid`, nothing written to the book) a receipt that does not verify, one with a member besides the five or non-canonical base64, any receipt without `At` or a directory, and more than `MaxReplyReceipts`; it then carries them in `checkpoint.witnesses`. With this a bundle can carry the receipt a reader needs before it counts an unavailable-window statement as registered, with no edit of the bundle JSON; a reader still checks the receipt under its own witness directory.

### Added: a bundle for anyone but the owner refuses a Result that cites a listed conduct pack (#412)

- `BundleRequest.ConductPacks` lists the pack ids the book's owner holds as conduct packs (`scope.kind: conduct`, private to the owner). `Book.Bundle` refuses, with `ErrInvalid` naming the Result and pack, a bundle whose selected set (root, closure or `Include`) holds an `evidence_result` that cites one, in its `subject_ref` or any claim's `contract_ref`, and so does a Result sealed with its header inside its capsule anywhere in the carried interval. `StandardHistoryBundleRequest.ConductPacks` applies the same refusal to a handover. `BundleRequest.OwnerCopy` marks a bundle that stays with the owner, which may carry it. Only `evidence_result` records are checked, and only for pack ids the caller lists. The kind is local policy: no record, Result or compiled-output member changes. The `capsulectl` wiring (a local list of private pack ids) follows once `capsulectl` pins a release that has this field.

### Added: reconcile failed exchanges by outcome (#205)

- `Half.Failed` marks the record of an exchange that did not complete. `ReconcileHalves` pairs a failed half with its counterpart on the exchange id as before, then decides the pair by outcome: two failed halves are `MATCHED`, a failed half against a served one is `CONFLICTING`, and the content check runs only on two served halves. A served pair is classified as before, except that served halves sharing an exchange id now pair by request digest first, whatever the input order (evidencebook#205). `ReconcileInput.OwnFailed` and `PeerFailed` name the records `Book.Reconcile` treats as failed, since a header carries no outcome. Pairing prefers a half of the same outcome, so a failed attempt and a served retry of one exchange id never swap partners.

### Added: hand a standard_history card's material, and recompute its counts (#211)

- `Book.StandardHistoryBundle` (and `Cadence.StandardHistoryBundles`, one per committed log): the `evidence-bundle/v2` of a book's records from seq 1 to a checkpoint it issued, as `spec/standard-history-v0.md` §6 "Handing the material" (SH-3) carries it, with no new member. `StandardHistoryBundleRequest.Disclose` is the host's share gate: a record it refuses still travels as a capsule with its header and payloads withheld, so coverage can be shown without the record; a record whose caller sealed its header inside the capsule cannot be refused (`ErrInvalid`).
- `CheckStandardHistoryMaterial`: reports the card's six properties and recomputes the counts over the card's own window from the handed bundles. `counts_recompute` is `PASS` or `FAIL` only when coverage is shown for the anchor's log and for every other log the anchor commits (interval coverage and per-record membership established from seq 1 to the committed extent, anchored at the committed checkpoint), the anchor's time is the checkpoint's own, no record's header is withheld, and the host's `DealMapping` read every member it needs. A partial handover, a withheld member (`ErrMaterialWithheld`) or an anchor that commits its logs through a salted or sparse tree (SH-10, which a cadence tick's anchor does) keeps it `NOT_CHECKED`, never `FAIL`.

### Added: seal and read unavailable-window statements (#223)

- `Book.SealUnavailableWindow` seals the book's signed statement that it was unavailable from `start` to `end`, for a `reason_class` of `flood`, `maintenance`, `outage` or `other_declared` (record type `unavailable_window`; signature: Ed25519 over the JCS of `end`, `reason_class` and `start`, the profile of agent-action-capsule's `unavailable-window.json`). It is registered once a checkpoint covering it carries a receipt from a transparency service the reader accepts as independent; the host obtains and attaches that receipt (this package has no input to attach one to a bundle yet).
- `VerifiedBundle.UnavailableWindowStatements` reads the statements out of a bundle `VerifyBundleAnswer` accepted, with each receipt's attested time under the reader's witness directory; a receipt made under the responder's own key is never independent. `Book.ShowAbsence` shows a recorded absence beside them through agent-action-capsule's `evidencerequest.DisplayAbsence`: "no answer; holder states unavailability" with the class, the stated period and "stated afterwards" when registered after the waiting window closed, never a refusal and never proof of an attack. The absence record and any retention commitment are unchanged. `UnavailableWindowText` is the wording a person is shown.
- The vendored request vectors gain `unavailable-window.json`; all 16 display cases run against a requester's book.

## capsulectl/v0.1.0-rc37 (2026-11-05)

capsulectl-book only; no library tag.

### Changed: capsulectl-book `v0.1.0-rc37`

- `capsulectl/go.mod` and `report/go.mod` require capsule-cli `v0.1.0-rc37` (the version this release pairs with). It also carries what merged since `capsulectl/v0.1.0-rc36`: #407, below.

### Changed: capsulectl-book vendors the History card module from capsule-viewer `v0.18.3`

- `capsulectl/internal/book/assets/standard-history` is now the module of capsule-viewer `go/v0.18.3` (it was `go/v0.18.2`): the PRIVATE notice is a rounded, bordered box like the card's other boxes, and "Which checks ran" has a gap under the "How this page was checked" line (capsule-viewer#298, PR #299). Layout only; no wording or contract change.

## capsulectl/v0.1.0-rc36 (2026-11-05)

capsulectl-book only; no library tag.

### Changed: capsulectl-book `v0.1.0-rc36`

- `capsulectl/go.mod` and `report/go.mod` require capsule-cli `v0.1.0-rc36` (the version this release pairs with; capsule-cli#547, #549, #552). capsulectl-book's code is unchanged, and it keeps vendoring the History card module of capsule-viewer `go/v0.18.2` (`capsulectl/go.mod` still requires capsule-viewer `v0.18.2`).

## v0.13.6 (2026-11-04)

Go module only.

### Added: the verdict's `rules` body records the pinned pack's kind (#394)

- The verdict record's `rules` body gains an optional `kind` (an open string, schema `x-deal-v0`): the kind of the pinned pack (`deal`, `conduct`, or a later one) as the profile's pin stated it when the check ran. A reader shares a pack with a counterparty only for the exact kind `deal`; a record without `kind` names none. Go `VerdictBodyRules.Kind`, the Rust `VerdictBodyRules.kind` and the vendored Python and Rust schemas follow (evidencebook-deal#146).


## capsulectl/v0.1.0-rc35 (2026-11-04)

capsulectl-book only; no library tag.

### Changed: capsulectl-book vendors the History card module from capsule-viewer `v0.18.2`

- `capsulectl/internal/book/assets/standard-history` is now the module of capsule-viewer `go/v0.18.2` (it was `go/v0.10.0`): the "Everything on the card" fold-out has the same gap under the card as the other sections (capsule-viewer#283), checkpoint times show to the minute (capsule-viewer#281), and the other card changes since 0.10.0. `capsulectl/go.mod` and `report/go.mod` require capsule-viewer `v0.18.2`, and the committed monthly pages under `report/testdata/presentation` are rewritten with its runtime (`CAPSULECTL_UPDATE_PRESENTATION_PAGES=1`; bundles unchanged). This ships with the next capsulectl release.

### Changed: capsulectl-book `v0.1.0-rc35`

- `capsulectl/go.mod` and `report/go.mod` require capsule-cli `v0.1.0-rc35` (the version this release pairs with; a rules-checker pin may state its pack's kind, capsule-cli#545). capsulectl-book's code is unchanged besides the History card module and the pins above.

## v0.13.5 (2026-11-04)

Go module only.

### Docs: for currency BTC, `amount_minor` counts millisatoshi (#385)

- x-deal-v0 (PROFILE.md sections 3 and 11 and the `currency` descriptions in the schemas, Go, Python and Rust copies): for currency `BTC`, `amount_minor` counts millisatoshi (10^-11 BTC), shown as "N msat". `dealprofile.MinorUnits("BTC")` stays unpriced. Description-only; validation is unchanged.

### Fix: `StandardHistoryDeal` prices a BTC deal in msat (#388)

- A deal whose check states currency `BTC` is no longer refused as unpriced: its entry is `{value: amount_minor, assetCode: "BTC", assetScale: 11}` (10^-11 BTC = msat; the settlement-records draft's own example). The scale comes from an explicit table (`historyAssetScales`), not from `MinorUnits`, which still has no ISO 4217 minor units for BTC. The Python and Rust copies match. The card keeps BTC as its own currency row and never sums it with fiat. Other codes with no ISO 4217 minor units stay refused as unpriced.

## capsulectl/v0.1.0-rc34 (2026-11-04)

capsulectl-book only; no library tag.

### Changed: capsulectl-book `v0.1.0-rc34`

- `capsulectl/go.mod` and `report/go.mod` require capsule-cli `v0.1.0-rc34` (the version this release pairs with; a mistyped top-level command now names itself, capsule-cli#543). capsulectl-book's code is unchanged; the release ships with capsulectl-deal `v0.1.0-rc34`, which prices a Lightning (BTC, msat) deal on the standard-history card (evidencebook#388, library `v0.13.5`).

## capsulectl/v0.1.0-rc33 (2026-11-03)

capsulectl-book only; no library tag.

### Changed: capsulectl-book `v0.1.0-rc33`, on capsule-viewer `v0.18.1`

- `capsulectl/go.mod` and `report/go.mod` require capsule-cli `v0.1.0-rc33` (the version this release pairs with) and capsule-viewer `v0.18.1`. The committed monthly pages under `report/testdata/presentation` are rewritten with the v0.18.1 runtime through `book report build` (`CAPSULECTL_UPDATE_PRESENTATION_PAGES=1`; bundles unchanged), so evidencebook-deal's copy of these pages matches; the release ships with capsulectl-deal `v0.1.0-rc33`.

## v0.13.4 (2026-11-02)

Go module only.

### Added: the disclosure policy, `policy-change/v1` and `Doctor` settings rows (#156)

- The disclosure policy (#156): `DisclosurePolicy` is the `policy` member of loop's `disclosure-policy/0` file, shipped as "Action State default v1" for a person (Muse) or a node (Mesh), with the settings window, standard history, staple, more-than-standard, receipt, unsigned-ask rate limit, under-contract scope and asked-of-you log retention, and the never-list as fixed text. `Book.AdoptPolicy` and `Book.ApplyPolicyChange` seal it: a `policy-change/v1` block (base policy digest, round, changed fields, `then[]`) becomes one `disclosure_policy` record per field, a change composed against another round is refused `ErrStaleRound`, and one that names a never-list item is refused `ErrNeverList`. A policy record is never served, by any subject form or derivation. A book that holds a policy applies its standard-history, receipt and unsigned-ask switches in `Respond` whatever `SharePolicy` the caller passes; asks of askers that do not sign share the weekly limit, except the public checkpoints. `DisclosurePolicy.Rows` is the settings page, and `Doctor` reports the same rows (`setting_*`) for a book that holds a policy, answers under it, and takes `DoctorOptions.As` (`stranger` or `signed`). `SharePolicy.StandardHistory` switches `standard_history/1` off (`derivation_unsupported`). The probe's standard-card line now states the card's window instead of the deal total.

### Changed: capsulectl-book `book result build` enforces a calibration claim's evidence rule (#376)

- A `calibration` claim must cite exactly one distinct `calibration-summary/v1` record and otherwise only `human-rating/v1` records (evidence-result-v0 section 4.1), read from each cited record's `record_version`; a claim that cites no summary, two, or a record of another kind is refused. A cited record with no disclosed body is checked for presence only, and the one summary must be disclosed. `testdata/result` vendors the three negatives that need the cited records (`neg-calibration-without-summary`, `-two-summaries`, `-cites-other-record`, aac 09c8b866), and `TestResultVectors` runs them through the walk.

### Changed: capsulectl-book `v0.1.0-rc32`, and the monthly fixtures cite their weeks' calibration records (#168)

- `capsulectl/go.mod` and `report/go.mod` require capsule-cli `v0.1.0-rc32`, whose `evidence-result-v0` schema has the `calibration` claim type (agent-action-capsule#552); `capsulectl/go.mod` requires `report` at the main commit 976f311 and so capsule-viewer `v0.12.0`, which the committed pages are written with (the capsulectl workflow had not run since the pages were rewritten for it, #357, so `TestReportBuildGoldens` was failing on main); `report/go.mod` also requires evidencebook `v0.13.3`, so `report.Build` verifies the book-form Result root `book result build` seals. `capsulectl/internal/book/testdata/result` vendors the schema-visible calibration vectors from that commit (`pos-example-org-calibration-result`, `neg-calibration-with-close-body`, `neg-out-of-scope-on-calibration-claim`); the three negatives that need the cited records are vendored with the book's check of a calibration claim's evidence (#376).
- `report/testdata/presentation/{4-monthly-outcome,5-monthly-compliance}` are regenerated through `capsulectl book result build` and `disclose` (a Result root of the book form, 36 and 19 claims): each Result carries a `calibration` claim per week with a blind sample, citing that week's `calibration-summary/v1` and the `human-rating/v1` records of reports its requirement claims cite, and the bundle discloses them. One week has a rating that differs from the judge and one a shortfall (drawn > rated); the last week has no sample and no claim. Pages and snapshots are rewritten.

### Fixed: a disagreeing reconcile Result counts as undecided (#212)

- x-deal-v0's `standard_history/1` deal entry (PROFILE.md section 11) counts a deal whose reconcile Result has a `not_met` claim as `undecided`, not `did_not_hold`: halves that disagree do not show which side failed (evidencebook#212). Go, Python and Rust change together.

### Docs: admit a Mesh node on path 5 (capsule-engine#273)

- docs/GUIDE.md path 5 gains "A Mesh node": `Example (Staple)` now reads the Mesh node's checked
  `inference-service-history/0` answer with capsule-engine's `admission.InferenceServiceHistoryCounts` and evaluates
  `admission-node-baseline@1` (`testdata/admission-node-baseline/`) for a GO and a LIMIT, showing per-property rows and
  no score (capsule-engine#273).

### Docs: Path 5, admit a peer (#189)

- docs/GUIDE.md gains Path 5, admit a peer: verify the staple, evaluate the admission pack with capsule-engine's
  `admission.Evaluate` (go/v0.3.0) and seal the decision's block with `Book.Append`. `Example (AdmitAPeer)` runs
  `admission-baseline@1`'s fixtures (`testdata/admission-baseline/`, listed in VECTORS.md) for a GO, an ASK with its
  recorded request and a LIMIT after a refusal. The root module requires capsule-engine/go v0.3.0, for that Example
  only (evidencebook#189).

### Changed: capsule-viewer `go/v0.13.0`, and the monthly pages are regenerated with it (evidencebook-skills#73)

- `capsulectl/go.mod` and `report/go.mod` require capsule-viewer `go/v0.13.0` (capsule-viewer#238), whose release notes (https://github.com/action-state-group/capsule-viewer/releases/tag/go%2Fv0.13.0) list all the changes the pages pick up; the reason for this bump is #238, which no longer counts a calibration claim in the roll-up header. `report/testdata/presentation/{4-monthly-outcome,4b-monthly-outcome-informational,5-monthly-compliance}/page.html` are rewritten with it; the Results and snapshots are unchanged.

## v0.13.3 (2026-11-02)

Go module only.

### Docs: the Mesh node's History in the guide (#322)

- Guide path 4 derives and checks the Mesh node's `inference-service-history/0` History in a CI-run Rust example (`rust/interop/tests/mesh_history.rs`) with agent-action-capsule's `inference_history`, under the definition a Mesh node serves. The Go Example still stops at the node's `history_card/1`: this module pins agent-action-capsule go v0.19.0, before the Go reader (v0.20.0); checking the History there is #347. No change to the Go module's API.

### Changed: Book derives record order from its log (#219)

- `Book` no longer keeps a separate list of record ids beside its log of records; the log is the one source of record order. No behavior change.

### Changed: `Cadence.HistoryCard` is bounded like a Book's card (#354)

- `Cadence.HistoryCard` returns the new `ErrOverLimit` when the card would list more than `DefaultMaxAnswerItems` (10,000) ticks, about 35 days at the default 5-minute interval, the cap a Book's responder applies (a Book's own card over it is a refusal, `Book.Staple` reports `ErrInvalid`). Before, the card grew without bound at about 288 entries a day. A longer-lived cadence staples its `standard_history/1` card (`Cadence.StandardHistoryAnchor`), which counts and does not list ticks. The card's form and `CheckStaple` are unchanged.

### Changed: capsulectl-book `v0.1.0-rc31` pairs with capsulectl `v0.1.0-rc31`

- `capsulectl/go.mod` and `report/go.mod` require capsule-cli `v0.1.0-rc31`; capsulectl-book
  `v0.1.0-rc31` pairs with capsulectl `v0.1.0-rc31` and carries `book report check` without the signing
  secret (#324, #349) and `SOURCE_DATE_EPOCH` for scratch books (#330).

### Fixed: capsulectl-book `book report check` no longer needs the profile's signing secret (#324)

- `book report check` no longer reads the profile's signing or checkpoint signing key, so a profile with no key file, a
  missing one or a key no `trusted_keys` holds answers `match` or `mismatch`.

### Added: `report.View.Charts` fills and passes a pack's chart views to the viewer (#319)

- `report.Build` takes the view entries of a pack's `chart-views/v1` block (calendar, timeline, co-failure) in
  `View.Charts`, adds to every chart in them the `range` filter on the sealed window and the `usermeta.capsule`
  statement (population, window, runs, values, a not-run day as `not_run`) computed from the Result's dataset, checks
  each with capsule-viewer's `chartspec.Check`, and hands them to the viewer as its `charts` setting. A Result with no
  disclosed dataset, a plan without exactly one window, or a chart the filler cannot state refuses the build. A page
  built with charts gets no permalink.
- Raised the `report` module's capsule-viewer pin to go/v0.12.0, whose Go emitter accepts view entries (capsule-viewer#156), so a Go-built page draws the views and rewrote the committed presentation pages for its
  page runtime.

### Changed: the #206 answer doc cites the plugin's no-COMPLETE vectors

- Row 7 is answered: Mesh-LLM/capsules-plugin#248 adds one vector per answer kind showing no producer-selected answer reads COMPLETE (Mesh-LLM/capsules-plugin#233).

### Changed: rule_inputs_version accepts 4 (#363)

- The x-deal-v0 schemas accept `rule_inputs_version` 3 or 4 (`enum [3, 4]`, was `const 3`); 4 is the rule inputs version in which a rule's `channel` comes from the latest counterparty message only. A positive fixture (`positive-rule-inputs-4`) seals a pay check under 4 (#363).

### Changed: the shelf design note records its outcome

- `docs/design/254-shelf.md` is marked accepted and gains §11, which lists the pull request that landed each work item and what is still open (#254).

### Fixed: `CheckStaple` verifies that a `history_card/1`'s checkpoints form one chain (#344)

- `CheckStaple` now refuses a `history_card/1` staple whose checkpoints are not one chain: each checkpoint signed under
  the first's key, one log, each entry's signed `prev_size` and `prev_root` the one before's, and each consistency proof
  reaching the next commitment. Before, a card of unrelated checkpoints with the right form passed. The shared staple
  vectors pin two refusals (a skipped checkpoint, a consistency proof that does not verify).

### Added: `Example (Staple)` checks the Mesh node's History with the Go reader (#347)

- The Example derives the Mesh node's `inference-service-history/0` over its stream for the week closing 2026-11-02, embeds the stream's `history_card/1`, and checks it with agent-action-capsule's `inferencehistory.Evaluate` (go v0.20.1, which reads real COSE checkpoint cards). The module now requires agent-action-capsule go v0.20.1. This supersedes the note under #322 above that the Go Example still stops at the node's `history_card/1`.
- Changed by that bump: `Book.Respond` now refuses a CBOR request that is not in core deterministic encoding (`request_malformed`, digested over the octets as received), per agent-action-capsule's profile §5.2 (go v0.20.0). Before, it answered one.

## v0.13.2 (2026-11-02)

Go module only.

### Added: `Cadence.HistoryCard` builds the profile's `history_card/1` (#341)

- `Cadence.HistoryCard(ctx, n)` returns the profile's `history_card/1` over the cadence log as a `Staple`: the cadence
  checkpoint of every finished tick through tick `n` (the newest finished tick when `n` is 0), each with its consistency
  proof from the one before, as of tick `n`'s checkpoint and signed by the shelf's `Signer`. `CheckStaple` accepts it for
  `KeyID` of that signer. A host that holds a shelf and no per-profile book (capsulectl-deal) staples this card.

### Added: `Book.StapleForDeal` and `Book.StandardCardDeals` (which deals got the standard card) (#334)

- `Book.StapleForDeal(ctx, derivation, deal)` is `Staple` for a staple attached to one deal. A
  `standard_history/1` staple's `evidence_request_answered` record then carries the host's opaque
  deal id in one optional, holder-local member (`deal`; these records are never served).
  `history_card/1` seals no record, so it takes no deal. `Staple` is unchanged and names no deal.
- `Book.StandardCardDeals(ctx)` lists the deals those records name, in order, and seals nothing.
  `history_card/1` is stapled to every deal by default, so a deal not listed had that card only.
  This is the per-deal level the holder's own History page shows (D0 or D0 + D1). Python and Rust
  do not staple `standard_history/1` yet (#328), so they have nothing to change.

## v0.13.1 (2026-11-02)

Go module only. The entry below was merged (#330) after v0.13.0 was tagged, so this is the first v-tag that carries it.

### Added: capsulectl-book reproduces a scratch book from `SOURCE_DATE_EPOCH` (#232)

- With `SOURCE_DATE_EPOCH` (seconds since the Unix epoch) set, every book verb (`publish`,
  `book result build`, `book close`, `cll checkpoint create`, ...) stamps that one time on each
  record, Result and checkpoint, and the records carry the header's `reproduction` marker, so two
  runs of one judge over one manifest give byte-identical bundles. Unset, nothing changes. A
  reproduced book cannot be extended without the variable, a live book refuses it, a time earlier
  than the book's last record is refused, and an unreadable value is an input error, never the
  wall clock. `backfill run` and `book migrate` stamp the wall clock and refuse to run with it set.
  A marked book is read like any other while the wall clock is past its time. Mind build environments that set the variable
  globally: every book written there is a reproduction. Countersigning a marked bundle is
  capsule-cli's to refuse (action-state-group/capsule-cli#525). Design:
  `docs/design/232-reproduced-scratch-book.md`.

## v0.13.0 (2026-11-01)

### Added: a guide path for History cards that staple (#266)

- `docs/GUIDE.md` path 4 and `Example_staple`: a Goose agent's shelf (a book per deal, a node book,
  the cadence) and a Mesh node's one stream each answer `history_card/1`; the agent's
  `standard_history/1` card is derived as of a tick and checked; a relying party composes the two
  parties' halves of the deal in `composed/v1`, joined with `pre_agreed_identifier` on the deal id,
  and joins the Mesh node to its operator's own agent only through a `same_controller_asserted`
  controller binding. The path states what is not defined yet: a History answer as a `composed/v1`
  member (#254 D4) and a library for `inference-service-history/0` (#322).
- agent-action-capsule's Go module moves to `go/v0.19.0`, which verifies `controller_binding` joins.

### Added: `external-check-input/v0` carries the acting role's rules pin (#230)

- `dealprofile/x-deal-v0/external-check-input-v0.schema.json` allows an optional top-level
  `rules_pin`, `{pack, definition_digests}` (either may be absent, never both): the pin the
  check runs under for `party_role` when the deal profile pins rules per role
  (`rules_checker.roles`). Absent when the profile pins no roles, so every input valid before
  stays valid. PROFILE.md ("The external rules checker") documents it.

### Added: `Book.Staple` and `CheckStaple`, a History card stapled to the holder's own message (#275)

- `Book.Staple` builds a staple of `history_card/1` or `standard_history/1`: the holder's own
  evidence request, pinned to its newest checkpoint and carrying a fresh nonce, with the signed
  envelope its book answers it with. No other derivation is stapled, so a staple never carries more
  than the standard card. Stapling `standard_history/1` seals an `evidence_request_answered`
  record, so the holder's book records which card it attached. The holder's own staple is not
  counted by the responder's limiter. Whether to staple is the host's setting; the book has no
  default.
- `CheckStaple` checks that a staple is presented by its holder and stays inside that cap: the
  envelope verifies under the holder's key and answers that very request, the derivation and
  subject are one of the two pairs, the anchor and profile are the requested ones, and a
  `standard_history/1` card is of the standard card's form, and a `history_card/1` card has the
  card's members and nothing else and ends at its anchor. It returns the anchor, nonce and issue time for the reader's replay
  check against its own `min_freshness`.

### Changed: capsulectl-book `v0.1.0-rc30` pairs with capsulectl `v0.1.0-rc30`

- `capsulectl/go.mod` requires capsule-cli `v0.1.0-rc30` (the capsule-viewer
  `go/v0.10.0` pin) and `report/go.mod` requires the same; capsulectl-book
  `v0.1.0-rc30` pairs with capsulectl `v0.1.0-rc30`.

### Added: `capsulectl book report build --contract` and `--contract-digest` (#258)

- `--contract FILE` pins a compiled Evidence Contract v0 to `--contract-digest` (the lowercase hex
  SHA-256 of its bytes), as `--wording` and `--wording-digest` do for a wording pack: both or neither,
  and not with `--permalink`. The page is the SDK's page for `View.Contract`; the JSON result names
  `contract_ref` and `contract_sha256`, and a refusal names `--contract`. capsulectl requires
  `report` `v0.4.0`, the release that carries `View.Contract`.

### Added: the Goose reference receives a referee's Result over the Nostr mailbox (step 09, #310)

- `python/examples/goose`: `loop_dispute_ask` publishes a request for the Result to the referee's
  mailbox key on the relays the deal terms name (`referee.mailbox_key`, `relays`,
  `private_relays: true`, which the node requires because the events are not encrypted, and `answers_within`), with a fresh carriage key and a deadline of `answers_within`; the poll
  interval may not exceed `answers_within`. `loop_dispute_poll` polls the relays for a reply that
  passes the reply checks of `nostr-mailbox-v0` §4.2 (the binding's event kinds and checks carrying a Result, not an evidence envelope, so not a conforming use of it), verifies the Result with capsule-engine's
  `verify_referee_result` after binding each party's terms to its signed half, and seals it as
  `goose_dispute_received`. The record cites both judged halves, which the book holds as
  `goose_dispute_half` records, and carries the SHA-256 of the Result's bytes: both parties' books
  hold the same `result_digest`. A Result that fails any check is refused and sealed as
  `goose_dispute_refused` with the refusal's code; no reply by the deadline is sealed once as
  `goose_dispute_absent`, and a reply that arrives later is still taken. The transport is
  `nostr_mailbox.py` (BIP-340, NIP-01, a poll per call); the example needs `websockets`.

### Added: a receipt's referee is one both sides chose (x-deal-v0, #277)

- x-deal-v0 section 1: an adjudicator's sealed report names its referee in `referee`, NFC and
  trimmed, and only that copy names one. Section 10, "A referee both sides chose": a referee is
  both sides' choice only when both parties' adjudicator's copies of one deal name it byte for
  byte; the adjudicator's check still runs otherwise and says which case it is (each side's
  name, or a copy naming none).
- `dealprofile.ReportPayload` takes `Report.Referee`: required on an adjudicator's report and
  refused on the others, so a producer (the capsulectl deal plugin) must now name the referee
  `--to` names. `NormalizeReferee`, `ReportReferee` and `SharedReferee` (with `RefereeError`, its
  reasons and, when the names differ, both names) read it; `testdata/parity-vectors.json` gains the referee cases Python passes.

### Added: `dealprofile.CheckChain`, the x-deal-v0 chain stage (§6) (#308)

- `dealprofile.CheckChain` reads a deal's records in `seq` order against PROFILE.md §6
  (sequence, hash chain, references, approvals, limits, close, reversals, offers and
  acceptance, a seller's representations, and a sale's own log), as the reference checker's
  `check_chain` does: the same refusals, in the same order, with the same text, at
  `StageChain`. A verifier built on Go can run the stage instead of leaving it unchecked.
  `testdata/chain-vectors.json`, written by `check_profile.py --chain-vectors`, pins the
  reference checker's verdict over every prefix of the positive chains, each negative
  fixture and single-member mutations of the positive chains; CI regenerates it and
  fails on a difference, and the Rust crate is tested against the same file.

### Added: `Doctor` with `Probe` renders the standard card a signed requester is served (#276)

- A book that serves `standard_history/1` (`Config.StandardHistory`) gets a
  `probe_standard_history` finding: the card as of the latest checkpoint, in the
  new optional `DoctorFinding.Card`, byte for byte what `Respond` answers any
  signed requester (the host policy's window, three months by default). It is
  built by the serve path's own decision and builder, in-process, so doctor
  still seals nothing and cuts no checkpoint; with no checkpoint the finding is
  `NOT_CHECKED`. A book that does not serve the card reports no such finding.
  capsulectl-book sets no `StandardHistorySource`, so it shows it only once a
  host serves the card.

### Changed: capsulectl-book and `report` build against capsule-viewer 0.10.0

- `capsulectl/go.mod` and `report/go.mod` require capsule-viewer `v0.10.0` (was `v0.9.0`;
  the receipt's `paymentPreimageCheck` and `readerCadenceWitness` runtime, capsule-viewer#215).
  The `report` fixtures' pages (`4-monthly-outcome`, `5-monthly-compliance`) are rewritten with
  the new page runtime. The vendored History card module
  (`capsulectl/internal/book/assets/standard-history`) is byte-identical in `go/v0.10.0` and
  `go/v0.9.0` (same bundle sha256); only its `SOURCE.txt` names the new release.

### Fixed: x-deal-v0 folds both cases of a Cherokee name to the capitals (#283)

- `dealprofile`: `cases.Fold` swaps Cherokee case, so `Ꭰ` and `ꭰ` had different
  fingerprints and the personal-data scan missed a local value in the other case.
  The fold now follows CaseFolding.txt (small forms to capitals), as the Rust
  crate and Python already do or now do. **The fingerprint of a name written in
  Cherokee changes**; a deal already sealed with one no longer matches.
  PROFILE.md section 4 states the rule; `testdata/parity-vectors.json` pins it.

### Changed: capsulectl-book and `report` build against capsule-cli v0.1.0-rc29 and capsule-viewer 0.9.0

- `capsulectl/go.mod` requires capsule-cli `v0.1.0-rc29`, evidencebook `v0.12.1`
  (was `v0.12.0`; it carries the x-deal-v0 Lightning preimage, #260) and
  capsule-viewer `v0.9.0` (calibration per clause, the History card's witness line and the
  red mark on a withheld claim: capsule-viewer#205, #206, #207); `report/go.mod`
  requires capsule-cli `v0.1.0-rc29` and capsule-viewer `v0.9.0`. The `report` fixtures'
  pages (`4-monthly-outcome`, `5-monthly-compliance`) are rewritten with the
  viewer's page runtime, and the vendored History card module
  (`capsulectl/internal/book/assets/standard-history`) is the one in capsule-viewer's
  `go/v0.9.0` release (was `go/v0.6.0`). capsulectl-book `v0.1.0-rc29` pairs with
  capsulectl `v0.1.0-rc29`.

### Added: `report` takes the compiled Evidence Contract, pinned to its digest (#204)

- `report.View.Contract` is a compiled Evidence Contract v0 (for example
  evidencebook-skills `contracts/<id>/<version>.json`) with the lowercase hex
  SHA-256 of its bytes, checked as `View.Wording` is. `Build` refuses a
  contract the Result does not cite, one that lacks a requirement the Result's
  claims cite, and a `tier` other than `must_have` or `informational`.
- `Page.ContractRef`, `Page.ContractSHA256` and `Page.Tiers` (each cited
  requirement's tier by `requirement_ref`; `""` when the contract names none)
  record what the page read. The contract reaches the page as the viewer's
  contract setting (capsule-viewer#201), from which the contract-report
  module reads each row's tier and which records the contract's digest. With
  no contract every tier is undecided, never must-have. A page built with a
  contract gets no permalink.

### Changed: capsulectl-book and `report` build against capsule-cli v0.1.0-rc28 and capsule-viewer 0.8.0

- `capsulectl/go.mod` requires capsule-cli `v0.1.0-rc28`, evidencebook `v0.12.0`
  (was the pseudo-version at the commit that adds `RequestSent`) and
  capsule-viewer `v0.8.0` (capsule-viewer#196, #197, #200, #201) and
  checkpointed-local-log `v0.3.0` (was `v0.1.0`), and `report/go.mod` requires
  capsule-cli `v0.1.0-rc28`, capsule-viewer `v0.8.0` and agent-action-capsule
  `v0.18.0`. The `report` fixtures' pages (`4-monthly-outcome`,
  `5-monthly-compliance`) are rewritten with the viewer's page runtime, shell
  styles and the CSP hashes that come with them. capsulectl-book `v0.1.0-rc28` pairs
  with capsulectl `v0.1.0-rc28`, and carries `book request --sent` (#253).

### Added: the `reproduction` header member and `Config.Reproduction` (#232)

- A reproduced scratch book takes the caller's time (`Config.Now`) on every record and
  checkpoint and marks each record's digested header `reproduction: {"clock": "caller"}`, so
  two runs over the same inputs give byte-identical records and a reader can tell the time is
  not a commit time. A book keeps one clock kind for its whole history: writing the other kind
  into it is `ErrInvalid`, a checkpoint included (the Rust and Python mirrors read and write the marker but do not
  guard against mixing). The published `record-header.schema.json` accepts the member. Absent by default. Design: `docs/design/232-reproduced-scratch-book.md`.

## v0.12.2 (2026-11-01)

Go module only. The `report` and `capsulectl` entries above ship with their own tags.

### Fixed: a wrongly typed `amount_minor` is unpriced in `dealprofile.StandardHistoryDeal`

- A check whose `amount_minor` is a string, a boolean or an array read as an amount
  of 0 (`json.Unmarshal` into a `*int64` allocates the pointer before it fails). It now
  refuses the deal as `ErrUnpriced`, as the Python reader does; the parity vectors pin it (#282).

### Added: `counterparty_class` on a pay check, read by the History card (#273)

- x-deal-v0: a `check` record whose `body.action` is `pay` may carry a
  top-level `counterparty_class` (`person_agent`, `business_agent` or
  `shop_preauth`, SH §7). The schema (Go, Rust, `check_profile.py`) allows it
  on that record only and refuses another value or another record; three
  negative fixtures. `dealprofile.Record` gains `CounterpartyClass`.
  `StandardHistoryDeal` fills the deal entry's `counterparty_class` from it, so a
  `shop_preauth` deal is no longer counted. A deal whose records carry it on
  two records, on a record that is no pay check, or with another value, still
  lacks it. Records without the member are unchanged.

### Fixed: shelf transactions take the write lock when they begin

- Every transaction on a shelf's own handle begins `IMMEDIATE`. A
  transaction that read and then wrote (storing a record or a checkpoint, a
  tick's witness, an import) began deferred, so under WAL another
  connection's commit between its read and its first write, such as another
  book's append to its CLL log, failed the write at once with
  `SQLITE_BUSY_SNAPSHOT` ("database is locked (517)") or `SQLITE_BUSY`
  ("database is locked (5)"), neither of which the busy timeout covers. A book checkpointing while another book appends
  could fail that way. It now waits for the lock under the busy timeout. No
  stored byte changes.

### Added: parity vectors for the Python x-deal-v0 profile (#264)

- `dealprofile/testdata/parity-vectors.json`, written by `TestParityVectors`
  (`EB_WRITE_DEAL_PARITY_VECTORS=1`): Go's answers for identifier normalization,
  the record-level stages, the `standard_history/1` deal entry and the cards derived
  from it, for inputs the fixtures do not reach. PROFILE.md §4 names the reference
  behavior of "lowercase", "casefold" and "IDNA".

### Added: the shelf contract's Go additions and the shelf vectors (#261)

- `Shelf.HostLog(ctx, logID, inCadence)` opens a host log: a log on the shelf
  whose records the host seals in its own format. `HostLog.Write` runs the
  host's write under the shelf's lock on the log's CLL backend and the
  shelf's artifact store (`HostLogStores`). A host log in the cadence is
  ticked like a book; one outside it is never counted or checkpointed by the
  cadence. `LegacyLog` is now an alias of `HostLog`, and `Legacy` and
  `LegacyLog.Append` behave as before.
- The shelf's file gains `shelf_meta` (version 1, `ShelfVersion`) and
  `shelf_logs` (each log's kind, `book` or `host`). A file with no
  `shelf_meta` row, which includes every shelf v0.12.0 and earlier wrote, gets
  version 1, and a file without `shelf_logs` gets it filled from
  `book_records` and `cadence_logs`. A version this build does not know is
  refused with `ErrUnsupported`. `Open` refuses a host log's id, and
  `Legacy`, `HostLog` and `ImportLegacy` refuse a book's id, across reopens.
- `ShelfConfig.HeldLock`: a host that already holds the store's writer lock
  passes it, and the shelf takes no lock of its own. Such a shelf opens on
  any platform.
- `ShelfConfig.Random` is the shelf's one random source, for every salt,
  tree position, filler nonce and seed, jitter and padding nonce the cadence
  draws. Nil is the system CSPRNG. Each draw is now defined on the bytes it
  reads (a tick's jitter is a uniform draw of 8-byte big-endian values, with
  rejection), so another implementation fed the same stream draws the same
  values.
- `testdata/shelf/` holds the shelf vectors written by this build:
  `tree.json` (cadence leaves, fillers, derived fillers, empty roots, trees
  and paths, padding record ids and random draws), and `script.json` with
  `after-write.json` and `after-continue.json` (a scripted shelf's tables,
  CLL entries, `Records`, `StandardHistoryAnchor` and `Extents` of each tick,
  and the cadence chain of every checkpoint a tick carries).
  `EB_WRITE_SHELF_VECTORS=1` regenerates them. `testdata/cadence` moved to
  `testdata/shelf/cadence` unchanged.
- `test/interop/shelf` is Go's side of the shelf storage interlock: it
  writes, continues and dumps the scripted shelf, and holds or tries the
  shelf's lock. CI's `shelf-interlock` job runs it.

### Changed: checkpointed-local-log go/v0.3.0

- The module requires checkpointed-local-log `go/v0.3.0` (was `v0.1.0`). Its
  receipt verifier applies the receipt profile shared with scitt-cose and
  agent-action-capsule, so a receipt that carries several valid inclusion
  proofs now counts as witnessed. The `asker-limits` witness vector's case
  with one proof listed twice becomes `malformed-second-proof`, a valid
  proof followed by a malformed one, which every checkpointed-local-log
  release refuses.

## v0.12.1 (2026-10-31)

Go module only. `report` and `capsulectl` changes under Unreleased ship with their own tags.

### Added: x-deal-v0 evidence may carry a Lightning payment's preimage

- x-deal-v0: evidence of a payment the user received may carry `preimage` (64 lowercase hex, only with `received_to`), a Lightning payment's proof of settlement; its SHA-256 is the step's `ln.payment_hash` (Steven, Buzz 79b46dfb item 5: seal the preimage, not the BOLT11). `dealprofile.PreimageMatches` and `fixtures/preimage-vectors.json` give the check; the Rust types carry the member (devcon-workshop#12).

## v0.12.0 (2026-10-31)

### Added: the book answers `evidence-interop/1` requests (evidencebook#248)

- `Respond`, and so the stream, HTTP, MCP and A2A carriages, answers a request
  naming `evidence-interop/1` with the same artifact as under `/0`, in an
  envelope naming `/1`. A request naming `/0` or no profile is answered as
  before. An unknown profile is refused `request_malformed` with the detail
  `profiles supported: evidence-interop/0, evidence-interop/1`.
- The manifest lists both profiles, and `draft-mih-agent-evidence-request-01`
  and `-02` beside `-00`: the book accepts all three request maps.
- A `/1` request refused for a limit of the book's own, its size, the rate
  limiter or the answer item cap, is refused `capacity_exceeded`, under either
  refusal policy (ER -02 §11). A `/0` request keeps `policy_declined`. The
  disclosure scan stays `policy_declined` under both. The book defines no
  requester authentication, so it never sends `request_unauthenticated`.

### Added: `capsulectl book request --request FILE --sent` records a request another tool sent (evidencebook#245)

- With `--sent`, `book request` records the file's request as another tool
  already sent it, such as the capsules plugin's sealed request, through
  `Book.RequestSent`. Every member is kept, including ones `--request` does
  not define (`audience`, `requester_id`, `requester_auth`), and no nonce or
  request profile is added. The file is recorded member for member in JCS. The answer then verifies with `book request --for
  --response`. Without `--sent`, `--request` still refuses a member it does not
  define, and `--sent` on a remote profile is refused. capsulectl requires
  evidencebook at the commit that adds `RequestSent`, and
  agent-action-capsule/go v0.18.0.

### Added: `Book.RequestSent` records a request another tool sent, so its replies verify (evidencebook#245)

- `Book.RequestSent` records a request that another tool composed and sent,
  exactly as given: every member is kept, including those the request draft
  does not define (the capsules plugin's `audience`, `requester_id` and
  `requester_auth`), and no nonce or profile is added. The octets must be a
  well-formed request in JCS, so the recorded digest is the one the responder
  signed its reply over. `RecordResponse` and `RecordAbsence` work on it as on
  a request `Book.Request` made. A nonce the book already used is refused.
  Its record marks the request `composed_elsewhere`, so the book is not read
  as vouching for members it keeps but never checks, such as
  `requester_auth`.
- A requester's book records `evidence-interop/1` requests and verifies the
  replies to them: a refusal's reason conforms under the profile the request
  asked for, so the two reasons ER -02 adds conform only under `/1`.
- A record-set artifact's record matches its digest when its body is the
  capsule preimage, as a book serves it, or the capsule as the capsules
  plugin's ledger holds it, whose `capsule_id` is that digest. The live-16
  replies to the plugin's requests (`testdata/live16-ask/`) verify as
  artifacts.

### Changed: agent-action-capsule Go v0.18.0

- The module requires `agent-action-capsule/go` v0.18.0, which reads
  `evidence-interop/1` and nests JSON at most 128 levels deep everywhere. A
  bundle extension or derivation definition deeper than that is refused; the
  derivation vectors' `too-deep` definition is now 129 levels.

## v0.11.0 (2026-10-31)

### Added: the History card over a deal profile's shelf (evidencebook#237, evidencebook-deal#85)

- `Cadence.StandardHistoryAnchor` is a cadence tick as the anchor of a
  profile's `standard_history/1` card: its `as_of` (the tick's cadence
  checkpoint, its time, and the cadence log's first checkpoint time) and, per
  log, how many records the tick commits (the largest checkpoint of the log
  any tick up to it carried).
- `DeriveStandardHistory` derives the card and checks it against its own
  inputs, as `Book.Respond` does for a book.
- `dealprofile.StandardHistoryDeal` fills a deal's SH §5.2 entry from its
  sealed x-deal-v0 records and Results (x-deal-v0 PROFILE.md §11). A deal
  whose agreeing step rests on a check with no amount is `ErrUnpriced`, and
  the card is refused. `dealprofile.MinorUnits` is ISO 4217's minor units.

### Changed: capsulectl-book and `report` build against capsule-viewer 0.7.0

- `capsulectl/go.mod` and `report/go.mod` require capsule-viewer `v0.7.0` (the
  frames merged since 0.6.0: capsule-viewer#186, #187, #188, #189, #190, #192,
  #193). The `report` fixtures' pages (`4-monthly-outcome`,
  `5-monthly-compliance`) are rewritten with the viewer's page runtime.

### Added: `capsulectl book holder-page` draws the holder's own pages from Steven's 9b canvas (evidencebook#156, #158, #239)

- `book holder-page --view share|asked|prompt --out PAGE.html` writes frame 2
  (What I share), frame 3 (They want more) or frame 9 (Settings to prompt) as one
  offline page, with the canvas's wording as an embedded `aac.wording-pack/v0`
  pack (`--wording` with `--wording-digest` replaces it; a pack missing an entry
  the pages draw is refused). The page is in the second person; `--shared`
  writes frame 2 in the third person. Frames 3 and 9 are the holder's alone and
  refuse `--shared`.
- A row the book has no producer for reads "Not available yet" and shows no
  value, so no default is drawn as if it were in force and the canvas's example
  values are never data. The rows with a producer: the public checkpoint answer
  and "Who asked about you" (the book's sealed `evidence_request_answered`
  records, which name no requester). The receipt row stays "Not available yet":
  `record_at_completion` is declared and reported by `doctor`, but this book's
  responder never reads it. The gaps are in #239.

### Changed: capsulectl-book builds against capsule-cli v0.1.0-rc26 and capsule-viewer 0.6.0

- `capsulectl/go.mod` and `report/go.mod` require capsule-cli `v0.1.0-rc26` and
  capsule-viewer `v0.6.0`, so `capsulectl/v0.1.0-rc26` pairs with that capsulectl commit. It
  carries the History card module as a plugin presentation (evidencebook#235,
  #236), the holder pages (#240), and the report pages' new outcome headline
  and tile wording from the viewer's page runtime.

### Changed: capsulectl-book builds against capsule-cli v0.1.0-rc25 and `report` v0.3.1

- `capsulectl/go.mod` requires capsule-cli `v0.1.0-rc25`, capsule-viewer `v0.5.0`
  and `report` `v0.3.1`, so `capsulectl/v0.1.0-rc25` pairs with that capsulectl
  release. Its report pages carry the bundle in a JSON data element
  (capsule-viewer#109) and `book report check` reads it from there (#164). The
  tests find the bundle in that element, and the network test looks for what a
  page or the runtime loads instead of the text `https://`.

### Added: capsulectl-book offers the History card as a plugin presentation (evidencebook#235, capsule-viewer#171)

- The handshake's `presentations` carries `capsuleviewer.standard-history/v0`, capsule-viewer's page for a `standard_history/1` card (`requires` `x-standard-history/v0`), so `capsulectl presentation list|build` resolves a standard-history bundle to it, the way capsulectl-deal's deal view reaches pages. The entry is capsule-viewer's own `presentation.json` from its release bundle, copied unchanged with the four files it names (`capsulectl/internal/book/assets/standard-history/`, `SOURCE.txt` names the release). The release ships the files as `standard-history-VERSION.tar.gz`, to unpack beside the launcher (`standard-history-module/`); `scripts/check-standard-history-archive.sh` checks it against the handshake in CI and in the release. Without the files, the core refuses the module and every command is unaffected.

### Changed: the report module reads a page's bundle from its JSON element (evidencebook#164, capsule-viewer#109)

- `report` requires capsule-cli `v0.1.0-rc25` and capsule-viewer `v0.5.0`.
  `InspectPage`, `CheckPage` and `SealPage` read the bundle from the page's
  `<script type="application/json">` element (capsule-viewer 0.4.0 and
  later), so a `"__proto__"` member stays data. A page sealed before that,
  which carries the bundle after `window.__BUNDLE__ = `, is still read and
  its seal still checks. The committed `4-monthly-outcome` and
  `5-monthly-compliance` pages are rebuilt on the new runtime, and the
  network test looks for what a page loads (a `src`, `href`, `url()` or
  `@import` naming a URL) instead of the text `https://`, which the vendored
  curves' comments now contain.

### Added: `ReconcileAsked` names the exchanges a provider left out before sealing (evidencebook#206 row 9)

- `ReconcileAsked` checks what a provider answered about a pair against the
  requester's own sealed halves of that pair, one ask per half by the
  exchange's two digests, and names every half the provider confirmed it
  holds no record of: a verified answer without it, or a signed
  `no_such_subject`, each only at an anchor the caller states covers the
  exchange (`Covers`). Any other verified refusal is REFUSED, reported on its
  own and never omitted. An absence, a failed answer, an older anchor, or a
  half with no digests is UNRESOLVED. Halves match
  records as a maximum one-to-one matching, so input order never changes how
  many are omitted. The verdict is `OMISSIONS_FOUND`, `NO_OMISSION_FOUND` or
  `UNRESOLVED`, never complete, and the provider's `seq`/`prev_seq` run over
  the matched records is reported beside it. The caller verifies the answers;
  the reconcile does not. Vectors: `testdata/reconcile-asked-v0`.

### Changed: `capsulectl book request --response` prints the answer's witness standing (evidencebook#209)

- The result now carries `witness`, as the response record holds it (#216):
  `witnessed` only when a receipt the reply carries (`witness_receipts`),
  from a witness in `--witness-directory`, covers the answer's anchor
  checkpoint; `unwitnessed` for a chain that verifies only against its
  holder's own signatures. A refusal or a failed answer prints none. Before,
  the command printed only `outcome` and `reason`, so an unwitnessed artifact
  read the same as a witnessed one.
- A book now accepts `--witness-directory` with `--response`; it refused the
  flag before, when a book's answer could carry no receipts. A directory that
  cannot be read refuses the command and records nothing. A carried receipt
  that does not verify counts for nothing; it never fails the answer, since
  nothing signs the carrier. The flag's help on a book says so.

### Fixed: answering an evidence request no longer holds the book's lock (evidencebook#208)

- `Book.Respond` answers from a snapshot of the log taken under the book's
  lock and builds the answer without it. It takes the lock again only to
  issue an anchor an authenticated requester's freshness floor needs and to
  seal the `evidence_request_answered` record. A flood of requests no longer
  stalls `Append`, `AppendCapsule`, `Checkpoint` or any other write: before,
  each seal waited for the answers queued ahead of it. What an answer may
  contain is unchanged; it is as of the snapshot it was built from.
  `TestRespondFloodDoesNotStallSealing` is the load test: while every answer
  costs 1 s, every seal lands in order and 95% of seals take at most 500 ms,
  waiting for the seals queued ahead of them (one per answer in flight),
  never for an answer; under a flood of strangers, who never seal, no seal
  takes over 2 s. Holding the lock, seals took seconds.
- Concurrent answers that each need an anchor for the same freshness floor
  issue one between them.
- A host's `Store` (`GetRecord`, `Checkpoints`), `Signer` and
  `StandardHistorySource.Inputs` are now called by `Respond` without the
  book's lock, concurrently with the book's writes and with other answers.
  The stores and signer this module provides already allow that.
  `Config.Now` and `RequestLimiter` are still called under the lock.
  `Release` waits for the answers already in progress.

### Added: `standard_history/1` for signed requesters (evidencebook#157)

- `Config.StandardHistory` (`StandardHistorySource`) has `Book.Respond`
  answer `full_history` under `standard_history/1`
  (agent-action-capsule `spec/standard-history-v0.md`) to any authenticated
  requester, whatever the share policy's tier: a card of counts, the same
  bytes for every requester, with no verification material. A stranger still
  gets only `checkpoints` and `history_card/1`. The host supplies its holder
  kind, and a function returning the policy's digest and window and the deal
  entries (SH §5.2) it reads from the book's servable records under the
  anchor; the book derives the card with agent-action-capsule's
  `go/standardhistory` and checks it against those inputs before answering.
  Without the source the token is `derivation_unsupported`. The manifest
  lists `standard_history/1` when it is served.
- `Book.Doctor`'s identified view says an authenticated requester gets
  `standard_history/1` whatever the tier when it is served, and names
  `correlation` beside `record` and `exchange` (evidencebook#114).
- Requires agent-action-capsule `go` v0.16.0 (was v0.11.0) for
  `go/standardhistory`.

### Added: the requester bounds a hostile answer, and records whether a witness covers its anchor (evidencebook#209)

- **Stated limits, checked before the work they bound.** These are new exported constants:
  - `MaxReplyBytes` is 8 MiB, the most a record's originals may hold.
  - `MaxReplyDepth` is 32 levels of JSON nesting.
  - `MaxReplyItems` is 10,000 verification entries, and 10,000 checkpoints, history
    entries or records in an artifact.
  - `MaxReplyReceipts` is 64 carried witness receipts.
- **What exceeding a limit does depends on what it falls on.**
  - `Book.RecordResponse` refuses a reply over a limit on its unauthenticated octets (size,
    nesting, the receipt carrier) with an `ErrInvalid` naming the limit. It records nothing,
    so the request stays open for the genuine answer.
  - A responder-signed envelope over a count or artifact-depth limit is recorded as
    `artifact_verification_failed`, with `failure` naming the limit (`limit: history: 10001
    over 10000`). This happens after the envelope signature and the artifact's digest are
    checked, so the limit falls only on signed content, and before any checkpoint signature
    or proof is checked.
  - `VerifyBundle` refuses a bundle over the byte, depth, record or receipt limit before the
    bundle verifier runs.
- **Witness standing.** `ResponseStatement` gains `witness`, which is `witnessed` or
  `unwitnessed` for an artifact that verified, and absent otherwise.
  - It reads `witnessed` only when the material carried the anchor's signed checkpoint (a
    `record`, `checkpoints` or `history_card/1` answer) and a receipt for that anchor, from a
    witness in `ResponderKeys.Witnesses`, verifies over it.
  - Receipts are read from the reply's unsigned top-level `witness_receipts` member
    (`WitnessReceiptsMember`), the carrier capsules-plugin sends. Nothing signs it, so a
    receipt that is malformed, forged, for another checkpoint or from an unlisted witness
    counts for nothing and never fails the answer.
  - A chain that verifies only against its holder's own signatures, which could be a private
    fork, reads `unwitnessed`. Answers with no anchor checkpoint in hand, such as
    `correlation`, always do.
  - `witness` is the requester's reading under its own directory when it recorded the
    answer. The carried receipts stay in the response record's original (the reply octets),
    so anyone holding it can check them again under their own directory.
  - `VerifyBundleAnswer` sets `VerifiedBundle.Witnessed` and adds the finding `unwitnessed`
    when a bundle carries no receipt. A bundle still fails on a carried receipt that does not
    verify, as before: a bundle is checked on its own, with no open request whose outcome a
    forged receipt could foreclose.
  - This repository's responders carry no receipts yet (#214), so answers between two
    evidencebook books read `unwitnessed`.
- The vectors in `testdata/asker-limits/` (`limits.json`, `witness.json`) are read by the Go
  and Python tests alike.

### Changed: a correlation subject is answered under the per-record rule (evidencebook#114)

- `Book.Respond` answers an underived (or shared-derivation) `correlation`
  subject under the rule of `record` and `exchange`, as Steven's amendment to
  the #114 ruling asks: under `counterparties`, the default, only to a
  counterparty every servable record held under the key names; under `peers` to any
  authenticated peer; never to a stranger. A correlation key matches exactly,
  never by prefix. Before, every underived correlation was refused
  `not_authorized`. `range` and `full_history` stay refused unless a
  recipient-specific derivation grants them.

### Changed (breaking): capsulectl-book seals and reads `close/v1` Closes, and takes `--period month` (#125, #122)

- `book close` seals the `close/v1` body (below): the period, and the counts
  by record type and head of the window the Close binds. Under `--since-last`
  that window can start before `period.start`, and the counts and head say so;
  the period stays the calendar period, which repeats and peer matching key on.
- It seals tallies only when the `--peer` bundle discloses the counterparty's
  Close for this period, which it then cites (`peer_close` in the output), and
  it reconciles against the peer positions that Close binds. Only Closes the
  bundle verified under its authenticated checkpoint count; one another
  disclosed Close supersedes is dropped; two left is refused. A head the
  bundle does not disclose, or counts the head cannot hold, is refused.
- What users lose: a Close sealed without a citable peer Close has no tallies,
  so `book result build` refuses a reconcile claim citing it (a no-peer Close
  used to back GAP from its INSUFFICIENT counts). Of two books closing one
  period, the first to close has no peer Close to cite and its Close states
  counts only; a later `close --peer` returns that Close. Superseding it once
  the peer's Close is citable is #201.
- Output: `close` prints the sealed body as `close`. `reconciliation` is the
  reconciliation a fresh Close ran; a repeat prints the one an older Close
  sealed, and none for a `close/v1` Close.
- Closes sealed before this change keep working for `close`: a repeat finds
  them by their profile string, and `--since-last` starts after their
  `to_seq`. But `result build` now refuses a close or reconcile claim citing
  one, since Result v0 §4.1 makes a claim's `close_ref` cite a `close/v1`
  Close, and `close` returns a period's legacy Close as a repeat rather than
  superseding it: a period closed before this change cannot back a Result
  claim until #201 lets `close` supersede its Close.
- `--period month` closes or reconciles a UTC calendar month (`month:YYYY-MM`,
  `[start, end)`); without `--date` it takes the last month that has ended.

### Changed (breaking): a Close seals the `close/v1` body (#125)

- `Book.Close` seals agent-action-capsule's `close/v1` body
  (`schemas/judge/close-v1.json` at `201f4df`, vendored as
  `schemas/vendor/close-v1.json`) with `record_version: "close/v1"`, and the
  record's `epistemic_type` is `producer_claim` (it was `derived_metric`). The
  body states `record_id`, `closed_at` (the header's `committed_at`), the
  caller's `period`, `counts_by_kind` and `head`. Only when
  `CloseInput.PeerClose` names the counterparty's Close for the period does it
  add `reconcile` (the six tallies, `status` `UNILATERAL`) and one `cites` link
  to that Close in `x-evidence-links`; otherwise the Close states counts only.
- `record_id` is a locator, `<book_id>#<seq>` (the record capsule's
  `action_id`). The record's identity is still its capsule id: every link
  target, `head.digest` and the `x-evidence-links` target is a record id
  (that digest), never the locator.
- `head` and `counts_by_kind` describe the own window the Close binds
  (`ReconcileInput.FromSeq`/`ToSeq`, an open end resolved to the log), not
  the log's tip: `head` is the record at the window's last position (for an
  empty window, the record just before it) and `counts_by_kind` counts every
  record in it. A window a caller starts before
  `period.start` is counted as given. `CloseStatement.Window` recovers the
  window and refuses counts that do not fit the head.
- `CloseInput` takes `Period` and `PeerClose`, and loses `Profile` and
  `Reason`. `CloseStatement` is now the `close/v1` body; the body Closes
  carried before (`counterparty`, `profile`, `reconciliation`, `reason`) is
  `LegacyCloseStatement`. `ReadClose` reads either by `record_version`; a
  `close/v1` body must validate against the vendored schema and agree with
  its header (`record_id`, `closed_at`, `producer_claim`). Closes sealed before this
  change still verify and read as legacy; they carry the whole
  reconciliation (pairs and windows) and the profile string, which a
  `close/v1` body has no member for.
- `Book.Close` commits a stored record still awaiting its append before it
  takes its window, and refuses a window past the log or an empty book.
- The root package now imports `github.com/santhosh-tekuri/jsonschema/v6` at
  run time (it was a test-only dependency of the root package), for
  `ReadClose`'s schema check; modules that import `evidencebook` build it.

## v0.10.0 (2026-10-30)

### Changed: the third-party verifier guide's two-node exchange is one whose records name both halves (#41)

- `docs/THIRD-PARTY-VERIFIER.md` §7.4 now takes the exchange from evidence-network/loop
  `5905bf0` (step 05 of 2026-10-30, a lab build with capsules-plugin#138), in place of
  the 2026-10-29 one at `cfeaf09`. Each holder's record carries
  `received_half.own_half_capsule_id`, so the two records name each other's halves; §7.4
  adds that row and the join through it, and its other values are the new exchange's.
  `test/cleanroom/run.sh` checks the new files, values and tie. §7.1 to §7.3 are unchanged.

### Changed: capsulectl-book builds against capsule-cli v0.1.0-rc24 and `report` v0.3.0

- `capsulectl/go.mod` requires capsule-cli `v0.1.0-rc24` and `report` `v0.3.0`,
  release tags in place of pseudo-versions, so `capsulectl/v0.1.0-rc24` pairs with
  that capsulectl release. It carries `book report build --wording` (#155, #159)
  and `book result build --plan` (#107).

### Changed: the install lines name v0.9.0

- The install lines in the README and the guide name v0.9.0.

### Added: `dealprofile` record types (#93)

- `Record{Block, Body}` is a step record, with one body type per record type
  behind the closed `Body` interface, and the typed action records and
  contract documents (PROFILE.md §9) have a type each. They follow the
  schemas member for member and carry no rule: `Encode` takes a `Record` as
  it takes the record's text and checks it the same way. `Decode` reads a
  record into them, refusing text they would not encode back to (such as a
  member name in another case, which `encoding/json` reads). Every record in
  the profile's positive fixtures and the baseline vectors, and a hand-built
  sample of each type they lack, decodes into them and encodes to the same
  bytes.

### Added: package `dealprofile`, the x-deal-v0 deal record profile (#93)

- The x-deal-v0 profile moves here from the deal plugin (evidencebook-deal),
  with no rule change: `dealprofile/x-deal-v0/` holds PROFILE.md, its schemas,
  fixtures and reference checker (`check_profile.py`, now run in CI with
  `jsonschema` pinned), and `sentence-marks.txt`. PROFILE.md changes only where
  it names its home.
- Package `dealprofile` gives the profile's names; `DealKey`, `ProfileKey`,
  `Normalize`, `Fingerprint` and `CommitText`; `Encode` (a record's payload and
  digest, once it passes `Check`), `Check` (the canonicalization,
  personal_data, wording and schema stages, refusing with a `*StageError`) and
  `CheckDigest`; `ReportPayload` and `ReportCapsule` for a sealed deal_report;
  and `StepCapsule`, a step's Capsule members and book header fields, each
  naming the producer's own developer. It seals nothing and imports neither
  the root package nor any CLI.
- `dealprofile/testdata/baseline-vectors.json`, frozen from the deal plugin's
  builders before the move (evidencebook-deal#65), is reproduced byte for
  byte: every step's payload, digest, Capsule and header fields, and every
  report's payload and Capsule. The chain stage (§6) stays the reference
  checker's: CI runs `check_profile.py` over every chain the package builds
  from those vectors.
- The module now requires `golang.org/x/net` v0.59.0 (identifier
  normalization), which raises `golang.org/x/text` to v0.42.0,
  `golang.org/x/crypto` to v0.57.0 and `golang.org/x/sys` to v0.48.0, and
  `github.com/stretchr/testify` v1.12.1 for the package's tests (with
  `go.yaml.in/yaml/v3`).

## v0.9.0 (2026-10-30)

### Changed: `report` requires the published capsule-cli v0.1.0-rc24 (evidencebook#144)

- `report/go.mod` requires capsule-cli `v0.1.0-rc24`, a release tag, in place of
  a pseudo-version, so `report/v0.3.0` can be tagged. The one pseudo-version left
  is agent-action-capsule's `go` module, which capsule-cli rc24 itself requires
  (no tag carries that commit yet). No code changes.

### Added: the third-party verifier guide checks one exchange between two live nodes (#41)

- `docs/THIRD-PARTY-VERIFIER.md` §7.4 adds both halves of one exchange
  between two lab nodes, as each side holds the other's (evidence-network/loop
  `cfeaf09`, step 05 of 2026-10-29): the files and their digests, both node
  keys, the texts (adding checkpointed-local-log's JSON checkpoint record), the
  result specified per side, the client-nonce join, and what it does not show.
  §7 now pins that loop commit throughout; the §7.1 to §7.3 files are
  unchanged at it. `test/cleanroom/run.sh` fetches the new files and checks
  that the values §7.4 states are theirs, each in its own side's column.

### Added: the third-party verifier guide checks a live node's bundle, and runs in a clean room (#41)

- `docs/THIRD-PARTY-VERIFIER.md` §7 adds a bundle that a node on the evidence
  network's lab sealed and published (evidence-network/loop `b02f8cf`), with
  the keys to pin, the texts to read and the result specified for each of the
  bundle draft's checks. The guide's §8 runs all its commands in a container that holds only
  Debian, `git`, `curl` and Go (`test/cleanroom/Dockerfile`), with an optional
  `VERIFIER` the tester supplies.
- `test/cleanroom/run.sh` runs every command of the guide and checks every
  value it states; CI runs it on every change. It found that §6.2's first `cmp`
  no longer held once `test/interop/personal-history` gained the originals
  store and `-correlated`: the guide now compares against `producer.diff`, as
  CI already did.
- A clean-room rehearsal of §7 (a verifier written from the public texts
  only) found that the node's checkpoint is in checkpointed-local-log -01 form;
  the guide now names -01, adds the disclosure-envelope draft to the inputs,
  and states the four points the texts leave to the verifier
  (agent-action-capsule#460).

### Added: `book result build --plan` binds a Result to the dataset plan it ran (#107)

- `capsulectl book result build --plan PLAN.json` runs an agent-action-capsule
  Plan IR v0 dataset plan (§5.1) over the profile's book with aac's
  `go/evidenceplan` gate. It sets the Result's `plan_ref` and `dataset_ref`
  (Result v0 §7.3) to the digests of the plan and of the dataset it selected,
  and refuses stated ones that differ. It refuses a plan over another book, one
  that reads past the book's last record, and one the gate refuses. The plan
  and the dataset are sealed as `evidence_plan` and `evidence_dataset` records,
  which the Result cites; the dataset cites the plan and names its rows'
  records by seq and header digest. The same plan reproduces both digests
  however the book grows. A Result that states either member without `--plan` is refused.
  A different plan or dataset is a different Result. `book report build --seal`
  reports `page_seal.revision`, so a presentation-only rebuild over the same
  Result shows the next revision. `capsulectl` builds
  against agent-action-capsule go `8c673d1` and capsule-cli `1a6d212b`, whose
  Result gate admits the two members (capsule-cli#490).

### Added: `report.Seal.Revision` numbers the pages sealed over one Result (#107)

- `report.SealPage` returns `Revision` (`revision` in JSON): the seal's place,
  from 1, among the book's page seals with the same Result root, in log order.
  A page that changes only its presentation embeds the same Result, so sealing
  it bumps the revision and the Result stays as it was. A repeat of a page
  already sealed keeps its revision. No record changes: the revision is read
  from the page seals the book already holds. `report` builds against
  capsule-cli `1a6d212b` (capsule-cli#490), whose Result gate admits
  `plan_ref` and `dataset_ref`, so `report.Build` accepts a page over a
  plan-driven Result. Needs a new `report/v*` tag; none is cut here.

### Changed: `report` takes its page runtime and block names from capsule-viewer/go (evidencebook#152)

- `report` imports `pageruntime.IIFE` and the `presentation.CardExtension` and
  `presentation.PresentationExtension` block names from `capsule-viewer/go`
  v0.3.0, not from capsule-cli, which removed them (capsule-cli#486). Report
  pages now embed the viewer's own runtime, so every page changes bytes; the
  build-provenance values are the viewer's: commit `3fe9ad7b0737` and viewer
  version `0.3.0` (verifier `0.8.0` unchanged). `report` and `capsulectl` build
  against capsule-cli `93dff5d`. Needs a new `report/v*` tag; none is cut here.

### Added: `report build --wording` (evidencebook#155)

- `capsulectl book report build --wording FILE --wording-digest HEX` lays an
  `aac.wording-pack/v0` wording pack over the viewer's own words in the written
  page, as the SDK's `report.View.Wording` does. The digest is the lowercase
  hex SHA-256 of the file's bytes and is required with the pack. The build is
  refused, and no page is written, when the digest does not match, the pack is
  not a valid `aac.wording-pack/v0`, or either flag comes without the other.
  A worded page gets no permalink, so `--permalink` with `--wording` is
  refused. `capsulectl/go.mod` now requires `report` v0.2.0.

### Changed: `capsulectl book result open` validates against the Result v0 schema (#106)

- `result open` checked only that `aggregate.coverage` existed and called Result v0
  a draft. The Result v0 schema is no longer DRAFT (agent-action-capsule
  `spec/evidence-result-v0.md`), so `result open` now validates
  with the same schema gate as `result build` and `report build`. It is stricter: a
  document it accepted before may now be refused, with every schema violation
  reported. Its note says the schema is no longer DRAFT and names what the spec still
  marks PROPOSED: the §4.1 `type: close` claim, §7.1 `coverage_report` and §7.2
  `coverage_declaration`.

### Changed: builds against the capsule-cli whose core names no company plugin (capsule-cli#428)

- The `report` and `capsulectl` modules build against capsule-cli `8a856b75` and capsule-viewer `389c1bac`
  (pseudo-versions, unreleased). A book profile now names its owner: a jsonl
  profile with a `log_id` needs `log: book` (`capsulectl profile create ... --log
  book`, or `capsulectl profile update --profile NAME --log book` for an older
  profile). The core refuses a profile that names none and prints the fix
  (`capsulectl profile update --profile NAME --log <cll or the plugin that keeps its
  log>`; for a book the value is `book`), and `capsulectl doctor` lists every
  profile with no declared log owner. The core's transitional aliases are gone and
  the old commands no longer exist: `capsulectl close`, `reconcile`, `request` and
  `respond` are `capsulectl book close|reconcile|request|respond`, and `capsulectl
  store migrate` is `capsulectl book migrate`. The scripted demo creates
  its profiles with `--log book`. The two `report` page goldens are re-emitted with
  the newer page runtime (its banner check no longer discards a render when the page
  grows a scrollbar, capsule-viewer#113).

### Changed: capsulectl-book names no host-package book export (capsule-cli#428)

- `capsulectl-book` registers its log backend by its own name
  (`cli.RegisterLogBackend`), checks for its own profiles with the log owner
  (a jsonl profile whose `log:` is `book`) and keeps its own `book` name and
  alias-removal constants, so the neutral capsule-cli core can drop every
  export named for the book. It builds against capsule-cli `0e9eef2b` and
  agent-action-capsule `1d1752bf` (both unreleased); a book profile's behavior is unchanged. The two `report` page
  goldens are rewritten because that capsule-cli vendors a newer viewer
  runtime, and the `report` module's own pins (capsule-cli, capsule-viewer
  v0.2.0, agent-action-capsule) move with them so both modules build the same
  pages.

## v0.8.1 (2026-10-27)

### Added: the SDK guide (evidencebook#129)

- `docs/GUIDE.md` walks the SDK as four paths (join the loop, seal a Result,
  build a Report, serve a History), each with a runnable Go `Example` that
  reads a corpus vector from `testdata/`: `Example_joinTheLoop`,
  `ExampleBook_SealResult`, `ExampleBook_Respond`, and the `report` module's
  examples. Terms Packs and evaluation are not in this module; the guide
  links capsule-engine and evidencebook-skills for them.
- `VECTORS.md` lists this repository's vector sets with their owning spec
  and pinned commit.
- The install lines in the README and the guide name v0.8.1.

### Added: a wording pack and the contract report in `report.View` (evidencebook#147)

- `report.View` takes `Wording`, an `aac.wording-pack/v0` pack with its
  SHA-256 (`report.Wording`), the wording layer over the viewer's own base, and
  `ContractReport`, which records the empty `contract-report/v1` block so the
  viewer draws the contract report. `Build` refuses a pack that does not
  check, or a different `contract-report/v1` block already in the bundle, as
  `ErrRefused`. A page with a wording layer gets no permalink, which would
  carry the bundle without it. With neither, pages are byte-identical to before; the page
  now goes through capsule-viewer's `BuildOfflineHTML`, whose default
  settings write the same page.

### Added: package `report`, the report builder in the SDK (evidencebook#132)

- Released separately: the module as `report/v0.1.0`, and the
  capsulectl-book change in the last item with the next `capsulectl/v*`.
  Neither is part of this module's v0.8.1.
- `github.com/action-state-group/evidencebook/report`, a new nested module, renders a verified
  Evidence Bundle rooted at a sealed Evidence Result v0 into one offline HTML
  page: `Build(bundle, View{Card, Presentation, Draft})` verifies the bundle,
  gates the root (exactly one Result), checks the Result, and renders only
  when `verify --bundle` calls the bundle VALID; otherwise it returns what it
  found with the page gate's refusal. `FormPayload`/`FormBook` and
  `VerificationPass`/`VerificationClosureWithheld` name `Page.Form` and
  `Page.Verification`. `Page.Permalink` encodes the same
  embedded bundle. Refusals wrap `report.ErrRefused`.
- `SealPage` seals a page's bytes in a book as one `report_page_seal` record,
  stating only what the page itself carries, and makes sure a checkpoint
  covers it; `CheckPage` compares a page file with those seals (`match`,
  `mismatch`, `no sealed record`, `not covered by a checkpoint`) and judges
  the embedded evidence apart, which `InspectPage` does with no book.
- This is the code behind `capsulectl book report build` and `report check`
  (#108), moved from `capsulectl/internal/book`; runnable examples render the
  presentation fixtures, now in `report/testdata/presentation`.
- `report` is its own nested module (released as `report/v<version>`), so
  the root module's requirements and module graph are unchanged. The report
  module requires `evidencebook` v0.8.0, `agent-action-capsule/go` v0.13.1,
  `capsule-cli` v0.1.0-rc23 (a prerelease) and a `capsule-viewer/go`
  pseudo-version, for the Result and page gates and the pinned viewer
  runtime; an importer of `report` gets these in its module graph, with
  capsule-cli's own requirements (cobra, viper, the MySQL driver) as indirect
  ones.
- `capsulectl book report build` and `report check` now run on the `report`
  module (`capsulectl/go.mod` requires `report` v0.1.0), and the plugin's own
  copy is gone. Pages, output and exit codes are unchanged.

## v0.8.0 (2026-10-27)

### Added: `report build --seal` and `report check` (evidencebook#108)

- `capsulectl book report build --seal --profile NAME` seals the written
  page's SHA-256 in the book as one `report_page_seal` record and cuts a
  checkpoint over it. The record's statement holds the page digest, the
  digest of the Evidence Bundle the page embeds and the Result root, and
  nothing else; its `subject_ref` is the bundle digest. A repeat of the same
  page finds its record; `--dry-run` seals nothing.
- `capsulectl book report check --profile NAME --page FILE` compares a page
  file with the book's seals for the bundle it embeds and reports `match`,
  `mismatch`, `no sealed record`, or `not covered by a checkpoint`, with the
  embedded evidence's `verify --bundle` verdict in `verification`, apart from
  the page answer. Exit codes: the evidence's, when it is not VALID (1 INVALID,
  3 INCOMPLETE); else 0 match, 5 mismatch, 3 no seal or none covered yet; 2 for
  a file that embeds no Evidence Bundle (not a report page).

### Added: `capsulectl book doctor [--probe]` (evidencebook#130)

- `capsulectl book doctor` prints the book's `Book.Doctor` findings as
  `{"findings": [...]}`, a list and never a score, and seals nothing. It reads
  the share policy from `--policy` (as `book respond --policy` does; default
  the documented defaults), controller binding records from
  `--controller-binding` (repeatable), and the witness deliveries in
  `book/witness.json`.
- `--probe` asks the book's own responder, the path `book respond` answers
  with no `--requester`, for `checkpoints` and then `history_card/1` as a
  stranger, and verifies both under the profile's own signing and checkpoint
  keys. A profile records no responder address, so the probe goes through
  that path in-process. The book grows by no record and no checkpoint.
- The plugin's metadata declares `doctor`, which capsulectl's own `doctor`
  lists (capsule-cli#459).
- `capsulectl/go.mod` requires the library at `f8de416` (#133). With it,
  `book respond` answers under #120's subject-scoped authorization: the
  plugin's tests now ask for `checkpoints` where they asked for
  `full_history`, which is refused to everyone, and expect `not_authorized`
  for a record subject asked by a requester no record names.

### Added: `Book.Doctor`, a node's check of itself (evidencebook#130)

- `Book.Doctor` returns a list of findings, never a score: what is
  self-attested, whether the latest checkpoint is witnessed, the
  `NOT_CHECKED` properties of received answers, what this book never
  answers, whether a controller binding names the signing key (which binding,
  by digest; its signatures are `NOT_CHECKED`, so the key is never shown
  bound), whether sharing is on, and what a stranger and an identified
  requester would receive under the policy. Whether the policy digest is in a
  commitment is `NOT_CHECKED`: the book publishes no commitment document.
- With `DoctorOptions.Probe`, it asks the node's own responder for
  `checkpoints` and then `history_card/1` as a stranger, verifies both under
  the advertised keys, and reports a mismatch when an answer is refused, does
  not verify, or is not anchored at the latest checkpoint; with no checkpoint
  to verify against, a refusal is `NOT_CHECKED`. Both subjects are public
  under every policy, so a match shows what a stranger is served, not what an
  identified requester is. The identified view is computed from the policy,
  not asked: `Respond` seals a record for every authenticated requester.
  Doctor seals nothing and issues no checkpoint.

### Fixed

- `bookdemo aac-verify` decodes the bundle with `json.Number` as `verify
  --bundle` does, so a bundle holding an `own_capsule` record no longer fails
  graph closure on integer `seq` and `v`; `report build` omits `unmet_claims`
  when the verdict is `VALID` instead of printing the fallback failure
  message (evidencebook#113).

### Changed (breaking): responding is subject-scoped, counterparties by default (evidencebook#114, #120)

- `DefaultSharePolicy` defaults to `history_segments` for counterparties, and
  `prospective` is read as counterparties. A record or exchange subject is
  answered under counterparties only to a counterparty that every record it
  names names, decided before resolution. `range`, `correlation` and
  `full_history` are refused under every tier unless a recipient-specific
  derivation grants them to its recipient (Steven's ruling on #114).

### Breaking: the cadence verifies witness receipts itself (evidencebook#115)

- `Cadence.Witnessed` verifies the receipt against the tick's signed cadence
  checkpoint with AAC's witness verifier (`go/witness.VerifyReceipts`), under
  the new `CadenceConfig.Witnesses` directory, instead of trusting the host's
  check. It now returns a `WitnessCheck`: `VERIFIED` witnesses the tick as
  before; `NOT_CHECKED` (the witness has no directory row or usable key, or
  no directory is configured) changes nothing and is not an error; `INVALID`
  changes nothing and is an `ErrInvalid` naming the reason. Each state
  carries AAC's finding and reason, except a receipt missing a member, which
  is `INVALID` before any check.
- `ImportLegacy` verifies the receipt of each witnessed pre-shelf tick it
  stores or promotes the same way and refuses an import whose receipt is not
  `VERIFIED`, naming the tick. A tick already held witnessed is not checked
  again, so a re-run after a key rotation still imports new ticks.
- Hosts migrate by passing their witness directory
  (`witness.ParseDirectory`) in `CadenceConfig.Witnesses` and reading the
  returned state. Ticks witnessed before this release stay witnessed: they
  were recorded on the host's check.
- The SCITT receipt substrate stub's receipts read `NOT_CHECKED`: they
  register a record identity, which AAC's verifier has no binding for. They
  are still labelled `scitt-receipt-unverified` and never count as witnessed.

### Added: the capsulectl-book agent skill

- `capsulectl/skills/capsulectl-book` is the agent skill for the book verbs
  `request`, `respond`, `close`, `reconcile`, `result build` and `report build`
  (`capsulectl book VERB`), moved out of capsule-cli's `capsulectl` skill
  (capsule-cli#326, capsule-cli#447). It has a `SKILL.md`, an `AGENTS.md`, a
  reference and a scripted demo; CI checks that its verbs are the plugin's and
  runs the demo against `capsulectl` plus a `capsulectl-book` built here.

### Changed: capsulectl-book builds against capsulectl v0.1.0-rc23

- `capsulectl/go.mod` requires capsule-cli at the published release
  `v0.1.0-rc23` instead of the commit it was imported from (e0511c4), so the
  first capsulectl-book release is `capsulectl/v0.1.0-rc23`
  (`capsulectl/RELEASING.md`).

### Added: capsulectl-book, as the nested module capsulectl/

- Released separately, as `capsulectl/v*`: not part of this module's v0.8.0.
  The `capsulectl` plugin over evidence books moved here from capsule-cli
  (capsule-cli#326), imported at capsule-cli commit e0511c4 (capsule-cli#446), as the
  nested Go module `github.com/action-state-group/evidencebook/capsulectl`
  with its own `capsulectl/v*` releases (`capsulectl/RELEASING.md`), each
  taking the version of the capsulectl release it pairs with. It
  requires this library at a version, so it adds nothing to the library's
  own module graph.

### Added: sealing an Evidence Result into a book

- `Book.SealResult` seals a checked Evidence Result v0 as one
  `RecordTypeEvidenceResult` (`evidence_result`) record about its contract,
  reusing a record an earlier run sealed, and cuts a checkpoint when none
  covers it yet. `Book.CheckpointCovers` reports whether the latest
  checkpoint covers a sequence number. Both were capsule-cli's, shared by
  `capsulectl book result build` and the deal plugin's sealed terms Result;
  they move here so the two plugins, which leave capsule-cli for separate
  repositories, keep one sealing path (capsule-cli#326).
  `RecordTypeEvidenceResult` names the record type they seal, which the
  caller's `add` writes (`Book.Append` or `AppendCapsule`). Go only: they compose existing book operations
  and add no wire format.
- `SealResult` is not atomic: reuse, add and the checkpoint run one after
  another, so callers that may seal the same Result concurrently must
  serialize. A failure after the record is committed (an index or automatic
  checkpoint failure) returns the record with the error, so its id is never
  lost.

### Changed (breaking): a deal reconcile over a period with no half is refused

- `RecomputeDealReconcile` returns `ErrInvalid`, and no Result, when the
  period holds no half of either book. Before, it sealed both reconcile
  claims with an empty `evidence`, tallies of zero and verdict `not_met`.
  Evidence Result v0 requires every claim to cite at least one record
  (agent-action-capsule#353).

### Changed (breaking): a derivation cannot be registered again with other declarations

- `Book.RegisterDerivation` no longer replaces a registration whose
  definition document has the same identifier (#86). Registering it again
  with the same recipient and the same sets of forms and fields returns the
  same identifier and changes nothing; with a different recipient, forms or
  fields it is `ErrInvalid`, and the first registration stands. Before, `{}`
  registered for alice and then for bob answered bob, not alice.
- `testdata/derivation/vectors.json` gains `reregistered`, which both
  implementations check.

## v0.7.0 (2026-10-20)

Everything since v0.6.0, with two breaking changes. A record that carries a
correlation identifier is answered under it (#81). A CLL book's `Book.Bundle`
builds through agent-action-capsule's producer, so its bundles carry each
record's Producer Envelope and read VALID under strict verification; a SCITT
receipt book keeps the book's own assembly, so its records carry no Producer
Envelope (#89). JSON with trailing data is now refused: `ErrCorrupt` for
stored capsules and headers, `ErrInvalid` for bundles, bundle extensions and
reconcile halves (#89). The module now requires agent-action-capsule Go
v0.11.0.

### Changed (breaking): a correlation subject reads the header's correlation identifier

For capsule-cli#264, following Steven's direction of 2026-10-19 (PT); the
spec reading is agent-action-capsule#328 O2, still open (#81).

- `Book.Respond` answers a `correlation` subject with the records whose
  header carries that identifier as `correlation.exchange_id`. A record with
  no correlation identifier is still held under its `subject_ref`, so a book
  whose records carry none answers exactly as before.
- **Behaviour change:** a record that carries `correlation.exchange_id` is
  no longer answered under its `subject_ref`. For example, a reconcile half
  filed under `subject_ref` `order-17` with `exchange_id` `ex-9` is now
  answered for `ex-9`, not for `order-17`.
- A record whose correlation carries only a request digest or a twin bracket
  is still held under its `subject_ref`. The two key spaces answer the same
  requested identifier.
- The `subject_ref` authenticated index (`NewSubjectIndex`) still proves the
  records under one `subject_ref`. Its documentation now says that does not
  establish a correlation answer's completeness, and a correlation answer
  carries no verification material, underived or derived.

### Changed (breaking): `Book.Bundle` builds through agent-action-capsule's producer

capsule-emit#338's step for this repository; design note on #84. A CLL
book's bundle is now built by agent-action-capsule's
`bundle.BuildEvidenceBundle` (Go v0.11.0, the new minimum), over an
unexported proof source on the book's own log. The book keeps the disclosure
act: its closure picks the range start and the disclosed records, and it
still builds the overlay and `evidencebook/payloads` and commits the
disclosure record. A SCITT receipt book keeps the book's own assembly.

- **Producer signatures.** Every carried record, ride-alongs included, carries
  its Producer Envelope as `signature` and `key_id`, so a book bundle reads
  VALID under strict verification instead of INCOMPLETE. The recipient sees
  the key of each record's signer, every signer key the book holds included
  (ruled on #84 §4).
- **Closure.** `records_mode` and `missing` are the verifier's: the capsule's
  chain parent and `references[]`. `missing` is in first-seen order. A
  citation below the range start is `ErrCorrupt`.
- **`BundleRequest.ProducerKey`** declares the producer key
  (`producer-key/v1`). The extension itself is now refused in `Extensions`, as
  is `composed/v1`.
- **Refused as `ErrInvalid` before the build:** a `Suppress` name other than
  `agent_input` or `agent_output` (the list is now stated sorted and without
  repeats, in the bundle and the disclosure record alike), a negative
  `ClosureDepth`, a `ProducerKey` that is not 32 bytes, and an extension that
  is not JSON, has no canonical form or nests too deeply for the bundle.
- **`payloads=all`** is refused when a disclosed capsule commits an eligible,
  unsuppressed member the bundle does not reveal (an output with no original,
  or one declared never retained); the error says to use `selected` or to
  suppress that member.
- The `checkpoint` member also states `key_id`, `timestamp`, `prev_size` and
  `prev_root` from the signed statement.

### Fixed: JSON with trailing data is refused

JSON the book reads with trailing data after the value, such as a stray `]`
or `}`, is now refused: as `ErrInvalid` for a bundle in `VerifyBundle`,
reconcile halves and bundle extensions, and as `ErrCorrupt` for stored
capsules and headers. Before, it was read as the leading value.

## v0.6.0 (2026-10-19)

Everything since v0.5.0: the APIs capsule-cli#242 needs to move deals onto
shelf books (#73, #74) and the Deal Reconcile Contract's recompute (#79),
which completes evidencebook#36. No exported identifier is removed or
changed; this release only adds API and fixes the witnessed index.

### Added: the Deal Reconcile Contract's recompute (#79)

evidencebook#36's last acceptance item, for capsule-cli#242 (note §2.10).

- `RecomputeDealReconcile(DealReconcileInput)` recomputes a reconcile
  under agent-action-capsule's Deal Reconcile Contract, `ec:deal-reconcile@1`
  (`DealReconcileContractRef`). It reads each half through its book's
  `HalfMapping` (JSON Pointers onto the six abstract members) and opens
  `field_openings` by half digest. It returns the opening statuses, each
  side's set status, the per-deal states, the tallies, sufficiency and
  verdict per requirement, the optional deal state, and the Evidence Result
  with one `type: reconcile` claim per requirement, which cites no Close.
- Each pairing is decided by `ReconcileHalvesWith` on the two halves
  projected onto the requirement's one field, so a failed opening fails only
  that requirement. The contract's rule 1 (more than one half on a side),
  rule 3's precedence (a missing algorithm, commitment or opening before a
  failed opening; an unsupported algorithm or malformed commitment counts as
  missing) and the set status of a coverage declaration are applied around
  that call.
- A half that is malformed by the contract (an absent or ill-formed
  `deal_id`, `book_id` or `recorded_at`, or a `book_id` that is not its book)
  is an `ErrInvalid` error, never a claim.
- The contract's 14 conformance vectors are vendored from
  agent-action-capsule `2cf9037` with their upstream SHA256SUMS, and every
  expected member is checked.

### Added: import a pre-shelf cadence; list a shelf's records (#73)

evidencebook#36 §2 and §4, for capsule-cli#242's migration.

- `Cadence.ImportLegacy(ctx, LegacyCadence{Logs, Ticks})` carries a cadence a
  pre-shelf writer kept (capsule-cli's `deal_cadence` rows, on the same
  cadence log) into the shelf's, in one transaction: every legacy log counts
  into H, each tick not already held is stored and numbered after the
  cadence's last, witnessed ticks are indexed, and the live rule is applied
  to every registered log. It is re-runnable: a held tick keeps its number
  and is promoted to witnessed when it now carries a receipt. A tick that
  would rewrite a held one, or is older than the newest held, is refused.
- `Shelf.Records(ctx, bookIDs...)` lists the committed records of a shelf's
  books, with each record's retention state and a count of pending tails,
  without opening a book.
- `CadenceConfig.WitnessIDs` queues each cadence checkpoint for those
  witnesses in the checkpoint's own commit; `CadenceConfig.NoJitter` makes
  every tick due exactly `Interval` after the last; `Cadence.Last` returns
  the newest tick.

### Fixed: an unchanged checkpoint is indexed to its newest witnessed tick (#74)

- The witnessed index now keeps the newest witnessed tick that carried a
  log checkpoint, whatever order receipts arrive in, for `Witnessed` and
  `ImportLegacy` alike. It kept the first, so after a host changed its
  witness, `Chain` kept returning the old witness's tick.

## v0.5.0 (2026-10-19)

Everything since v0.4.0: the originals store and per-record retention (#58),
and the deal features of evidencebook#36 (#61, #63, #65, #67, #68). #36's
end-to-end acceptance (a deal on the new book passing
`capsulectl verify --bundle`, an older deal store still verifying, a two-book
reconcile reaching `reconciled`) lands with capsule-cli#242.

- **BREAKING (#58)** for Go callers:
  - `Config.Payloads`, `PayloadResolver`, `PayloadDir` and `OpenPayloadDir`
    are removed. Set `Config.Originals` to an `ArtifactStore` instead
    (`OpenOriginalsFile` opens the JSONL backend), and move an existing
    payload directory's bytes into it once with `MigratePayloadDir`.
  - `Book.Retention(ctx, recordID)` and
    `Book.SetRetention(ctx, recordID, state, reason)` drop their payload
    digest argument: retention applies to the whole record.
  - `LifecycleStatement` drops `PayloadCommitment` (`payload_commitment`);
    a lifecycle record written before that names one is read as applying to
    the whole record.

### Added: a log's witnessed cadence chain for its bundle (#68)

evidencebook#36 §2.

- `Cadence.Chain(ctx, statement)` is capsule-cli's `cadenceChain` for a
  log's checkpoint, member for member, for the `x-cadence-witness/v0`
  bundle extension (`CadenceExtension`): `all` from a witnessed tick that
  carries the checkpoint, else `part` with the newest earlier witnessed
  checkpoint and a consistency proof to it. A profile adds its own members
  (a deal's step counts) and its pending states.
- `Cadence.Witnessed` now takes the receipt the host verified, stored with
  the tick for the chain.
- `testdata/cadence/chain-verified-by-capsule-cli-7a6b101.json` holds an
  `all` and a `part` chain this code built; capsule-cli 7a6b101's own
  verifier (`cadenceClaim`) passes both up to the witness receipt check,
  and fails each when its salt or consistency proof is changed.

### Added: the heartbeat cadence over a shelf, ended logs leaving the tick (#67)

evidencebook#36 §2 and §4.

- `Shelf.Cadence(ctx, CadenceConfig)` runs a profile's heartbeat ticks,
  ported from capsule-cli's `deal tick`. A tick is due on time alone
  (`Interval` 5m ± `Jitter` 1m by default, the next due time drawn after
  each tick). It checkpoints every live log locally at the tick's minute,
  commits the checkpoints as salted leaves of the cadence tree, appends the
  root (and padding to `PadBucket`) to the profile's cadence log, and
  checkpoints that log; the host delivers it to the witness and reports a
  verified receipt with `Cadence.Witnessed`.
- The live set: a log is checkpointed while it has not ended (the profile's
  `Ended` predicate, evaluated when needed) or has grown past the largest
  checkpoint of it a witnessed tick carries. Appends put a log back in the
  same transaction as its record; `Witnessed` takes logs out in one
  transaction, and a late receipt never moves a log back. Tick work is per
  live log; each tick keeps the lifetime count plus one occupied positions
  with seed-derived fillers.
- `Cadence.WitnessedTick` finds a bundle's tick from an index of witnessed
  log checkpoints (the very checkpoint, or the newest earlier one), not by
  scanning ticks. `Book.CheckpointAt` cuts a checkpoint at a given time.
- `Shelf.Legacy` joins a deal log from before the upgrade to the cadence as
  it is (never provisioning one); `LegacyLog.Append` runs the profile's own
  write under the shelf's lock and puts the log back in the live set.

### Added: salted per-field commitments and a three-way reconcile check (#61)

evidencebook#36 §3 (the deal profile on the book); the construction is the
one evidencebook#44 rules a History's deal-level commitments use.

- `Entry.Fields` commits texts by field name, each with a fresh 32-byte
  nonce, as `sha256-jcs-nonce256` (`CommitAlgNonce256`): SHA-256 over
  RFC 8785 of `{"nonce", "text"}`, agent-action-capsule
  presentation-contract §7.2 and capsule-cli's deal records. The header
  carries `field_commitments` (by field name; §7.2's records carry the same
  values as `<name>_commitment` members, so the placement differs and the
  values do not) and `commit_alg`; the openings
  (`{nonce, text}`) are originals in the artifact store, under the
  record's retention, never in the record. `Book.Opening` reads one;
  `VerifyOpening` checks one. Openings are not payloads:
  `payload_commitments` and `ResolvePayload` never include them.
  `schemas/record-header.schema.json` declares `field_commitments` and
  `commit_alg` (and requires them together, with `retention_state`).
  `Get`, `Query` and `Append` return records with their own copies of the
  header's maps and slices, so a caller cannot change what the book trusts.
- Reconcile: `Half` gains `FieldCommitments` and `Opened`. `OpenHalf`
  checks openings against a half's own commitments, and an opening that
  does not open makes the half untrusted (`UNRESOLVED`).
  `ReconcileHalvesWith` takes a `ContentCheck` returning `Agree`,
  `Disagree` or `Undecided`; `Undecided` is `INSUFFICIENT`, so a missing
  opening is never disagreement. `OpenedFieldsAgree(fields...)` needs both
  halves' openings, since each record's nonce is its own.
  `ReconcileHalves` and `Comparator` keep their meaning
  (`Comparator.Check`). `ReconcileInput` gains `Check` beside `Compare`
  (exactly one is set) and `OwnOpenings`/`PeerOpenings`.

### Added: a shelf of books, one log per deal (#63)

evidencebook#36 §1 (the deal profile on the book; one log per deal,
evidencebook#59).

- `OpenSQLiteShelf` opens one profile's books in one SQLite file: each book
  its own CLL log (`OpenCLLSQLite`, many logs in one file, as capsule-cli
  keeps a profile's deal logs) and its records and checkpoints in tables
  keyed by book id, with the profile's one artifact store shared. A deal's
  bundle is built from its book alone, so it carries no other deal's
  records.
- A shelf is the profile's only writer: it holds an exclusive advisory lock
  (`LockPath`, which a host points at its own writer lock) from open to
  `Release`, waiting for it until the context is done, and `Shelf.Open`
  returns one `*Book` per log id for its lifetime.
  `AuthenticatedIndexes` chooses each book's authenticated indexes (an
  empty list gives none, so a deal's log holds no `index_root` records).
  After `Release`, the shelf and its books refuse writes (`ErrReleased`; `Book.Release` now does
  the same for any book, `SetRetention`'s purge retry included).
  `OpenCLLSQLite` provisions only a log the file holds nothing of: a log
  with state that fails to open is reported, never initialized over.

### Added: the cadence tree, byte for byte capsule-cli's (#65)

evidencebook#36 §2 (the heartbeat cadence); the tick and live set come next.

- `cadence_tree.go` ports capsule-cli's cadence tree unchanged: leaf
  `SHA-256(0x00 ‖ JCS{checkpoint_sha256, log_id, salt, size})`, node
  `0x01`, empty `0x02`, filler `0x03 ‖ nonce`, depth 16
  (`CadenceDepth`), the sparse path and fold. `testdata/cadence` holds
  vectors written by capsule-cli 7a6b101's own code, and the port
  reproduces them.
- `TickLeaves` / `TickLeaf` are capsule-cli's stored tick leaves member for
  member, plus `filler_seed` and `filler_count`. `DerivedFillers` keeps a
  tick's occupancy at the profile's lifetime log count plus one when ended
  logs leave the tick, so a bundle's path shows no more than today; a tick
  stores one seed instead of a filler per ended log. `TickLeaves.Tree`
  rebuilds any tick's tree from what it stored; ticks a released build
  stored read unchanged.

### Changed (breaking): payload bytes live in an artifact store (#58)

capsule-cli#236: one originals store, kept per record.

- `Config.Payloads` and `PayloadResolver`/`PayloadDir`/`OpenPayloadDir` are
  removed. `Config.Originals` is an `ArtifactStore`, capsule-emit's
  artifact store contract (`Put`, `Get`, `Purge`); `OpenOriginalsFile`
  opens its JSONL backend, owner-only and synced on every write
  (`SyncedFile` gives a host's file-backed store the same). A store's `Put`
  and `Purge` must be durable when they return. A record with payloads is stored there with its
  capsule and envelope in one `Put`, after it is sealed and before it is
  stored. `MigratePayloadDir` moves a payload directory's bytes into the
  artifact store and changes no record, log entry or checkpoint; a record
  the store already holds without a payload the directory still has is an
  error, never skipped. It folds lifecycle records as `Retention` does: a
  record whose latest state is DELETED is not stored, and a payload an
  earlier per-payload deletion named is left out.
- Retention is kept per record. `Book.Retention(ctx, record)` and
  `Book.SetRetention(ctx, record, state, reason)` lose their payload
  digest, `LifecycleStatement` loses `payload_commitment`, and `DELETED`
  purges the record's originals. The dedupe-aware deletion is removed: each
  record keeps its own copy.
- A retention commitment holds every record it covers at `LEGAL_HOLD` until
  its `until`, so `SetRetention(..., DELETED, ...)` returns `ErrLegalHold`.

### Added: caller-sealed records and kept replies (#58)

- `Book.AppendCapsule`: a capsule its caller seals is the record itself,
  carrying its header in the `evidence_record_header` member
  (`RecordHeaderMember`), sealed under the book's writer lock and signed by
  the book's signer. `VerifyBundle` reads such a header from the capsule.
  A bundle discloses such a record's originals bound to its capsule's
  `agent_input` or `agent_output` digest as those members, so a neutral
  verifier matches them against the capsule.
- `RecordResponse` keeps the reply's octets as the response record's
  payload; `Book.ReplyOctets` reads them back.

## v0.4.0 (2026-10-19)

Everything since v0.3.0.

### Changed: one I-JSON reader and JCS, typed key sets, plain methods (#57)

- **BREAKING:** the UCP adapter reads its retained JWK Set from the new
  trust-policy field `key_sets`, not from `pins`.
  - `adapter.TrustPolicy` gains `KeySets map[string]json.RawMessage`
    (`key_sets`), one retained key set per name the adapter documents, kept
    as the JSON value it was written as.
  - Policy files must move `"pins": {"ucp.keys": "<JSON string>"}` to
    `"key_sets": {"ucp.keys": {...}}`, the JWK Set (or bare array) as a JSON
    value. A v0.3.0 `ucp.keys` pin is not read: the key check reports
    `NOT_CHECKED` with a detail that names `key_sets`.
  - Go callers: the exported constant `ucp.PinKeys` is renamed
    `ucp.KeySetName` (value unchanged, `"ucp.keys"`), and a policy built in
    code sets `KeySets["ucp.keys"]` instead of `Pins["ucp.keys"]`.
- x402 `payload_form` and ACP `object_form` now FAIL on invalid UTF-8 and on
  an escaped lone surrogate, instead of reading them as U+FFFD. All adapters
  share one I-JSON (RFC 7493) reader and one RFC 8785 serializer in
  `adapter/internal/ijson`, which adds no public API.
- Report JSON is no longer HTML-escaped: `PropertyResult.MarshalJSON` and
  `evidencebook adapt -explain` write `<`, `>` and `&` as themselves, so a
  method such as `relying party policy <id>` matches `-declare`.
- `docs/ADAPTERS.md` documents `key_sets` and the shared reader.

### Not in the Go module

- The Rust crate's store traits, received log and served summary (#56)
  shipped separately as `crates/evidencebook-v0.0.6`.

## v0.3.0 (2026-10-18)

Everything since v0.2.0.

### Added: the ACP and UCP adapters

- `adapter/acp`: the Agentic Commerce Protocol 2026-04-17 checkout session,
  completed session with order, order event and delegated payment token,
  after agent-action-capsule `spec/adapters/acp-v0.md` (2ef8aeb4). ACP signs
  none of them, so the adapter supports possession of exact octets only.
  - `octets_digest` is checked against a settlement-records leg's wrapped
    entry digest, or against the relying party's `acp.carried_digest` pin
    for a standalone adapt. The method names which.
  - `object_form` validates against ACP's JSON Schema, vendored by SHA-256.
    A completed session validates whole against JS-CHK, and an order
    event's data against OAS-WH `EventDataOrder`.
  - Native checks, each with `detail` `{"check", "object"}`:
    `checkout_session_signature`, `order_signature` and
    `delegate_payment_signature` are `NOT_PRESENT`. `order_event_hmac`, the
    webhook's shared-secret HMAC, is `NOT_CHECKED`, or `NOT_PRESENT` without
    the header.
  - It reads a settlement-records leg (wrapped types as proposed by acp-v0
    A-3, unregistered), a JSON body, an MCP JSON-RPC result, or an HTTP
    message. `evidencebook adapt acp`.
- `adapter/ucp`: the UCP adapter declaration v0 (agent-action-capsule
  `spec/adapters/ucp-v0.md`) for UCP v2026-08-25.
  - The checkout carrying `ap2.merchant_authorization` is checked natively:
    a detached JWS (ES256, ES384 or ES512) over the RFC 8785 form of the
    checkout without `ap2`, under a `keys[]` copy that the relying party
    retained and supplies in its trust policy.
  - An Order Event Webhook request held whole is checked with RFC 9421 and
    RFC 9530. A checkout without AP2, and an order body held alone, report
    `native_check` `NOT_PRESENT`: they prove possession only.
  - Objects are validated against UCP's JSON schemas, which are copied
    unchanged and pinned by SHA-256.
  - An operation response held whole adds `ucp.response_signature`. This
    is the RFC 9421 signature on the response, reported as its own
    property and outside the object's checks. An order, or a checkout
    without AP2, stays `NOT_PRESENT` whatever that signature says.
  - `evidencebook adapt ucp`.
- New dependency: `github.com/santhosh-tekuri/jsonschema/v6` v6.0.3 (JSON Schema
  2020-12, ACP and UCP schema validation), which brings in `golang.org/x/text`.

### Changed

- The ACP and UCP adapters, `docs/ADAPTERS.md` and the UCP vector pin cite the
  merged agent-action-capsule declarations (#300 `75009695`, #301 `2ef8aeb4`).
  The vendored UCP vectors are byte-identical to that commit.
- `docs/PRD-EVIDENCE-INTEROP.md` records M3 as landed for x402, UCP and ACP.

## v0.2.0 (2026-10-17)

Everything since v0.1.1.

### Added: foreign-evidence adapters, starting with x402

`docs/ADAPTERS.md` describes the adapter interface and how an adapter reports.

- `adapter`: one interface for every adapter. `Name` and `NativeObject` (the
  FR-19 declaration as data). `Check(raw, TrustPolicy)` returns profile §7.1
  reports. A protocol's named native checks are reported as `native_check`
  with the check's name in `detail` (`NativeCheckResult`, `CheckName`,
  `FindCheck`). `TrustPolicy` carries pins, keys, a clock and `recorded`
  observations. `Evaluate` maps the checks a requirement rests on to its
  status, sufficiency and verdict:
  - rows 5 and 6 for a FAIL, by whether the requirement is the check or is
    only supported by it;
  - `NOT_PRESENT`, `NOT_CHECKED` and `INCONCLUSIVE` as profile §8.1 rows 3-4,
    pending a profile row.
- `adapter/x402`: the x402 adapter declaration v0 (agent-action-capsule
  `spec/adapters/x402-v0.md` at `3c778daa`), reproducing its vectors
  (`vectors/adapters/x402/`) exactly. Each check is a `native_check` report
  with detail `{"check", "object"}` (`adapter.NativeCheckReport`), and the
  table includes `authorization_window`. The recorded chain observation has
  the node's result shapes.
  - It reports the thirteen checks of the declaration's §7 table.
  - The payer signature is checked by EIP-712 recovery under the policy's
    domain. A mismatch is `INCONCLUSIVE` unless the policy says `eoa`.
  - Settlement is checked from a recorded transaction lookup, including the
    validity window at block time, and is `NOT_CHECKED` offline.
  - Payee receipts: version 1, and version 2 under either open proposal.
  - Three input forms: a settlement-records leg (with the content check of
    each wrapped object and leg cross-checks), the bare payment payload, and
    an HTTP header block.
  - Fixtures are constructed with published development keys.
- `cmd/evidencebook`: `evidencebook adapt x402 [-policy file] [-explain]
  <file | ->` prints the reports as JSON and exits 1 on any `FAIL`.
  `-explain` adds a human-readable explanation to each report, and
  `-declare` prints the adapter's declaration.
- New dependencies: `golang.org/x/crypto` (Keccak-256) and
  `github.com/decred/dcrd/dcrec/secp256k1/v4` (public-key recovery).

### Added: the third-party verifier guide

`docs/THIRD-PARTY-VERIFIER.md` is new in this release. §1 to §5 are a path
for testing evidencebook's service-evidence responses with your own verifier,
reproduced by `test/interop/answer`.
`docs/PRD-EVIDENCE-INTEROP.md`, the evidence interop product requirements
draft, is new too.

`docs/THIRD-PARTY-VERIFIER.md` §6 covers the personal-evidence disclosure
that the third-party verifier test also requires: a History at the Basic level
of the Personal-Agent History Profile v0, with the conformance vectors
published at agent-action-capsule `0b7b1825` (`vectors/personal-history-basic-v0/`,
12 cases). It says how to reproduce the vectors' responder run, the result
specified for each case, and how to report the History apart from the
service-evidence response.

- `test/interop/personal-history` is the corpus's `producer/main.go`,
  unchanged. Its test checks that it writes the corpus's `responder-run.json`
  byte for byte (`run_sha256` `f7d77d56…`), and that every value the guide
  quotes is the corpus's (`testdata/vectors.json`, pinned by digest).
- CI: a `personal-history` job reproduces the run, rebuilds the corpus from it
  with the corpus generator, and runs the corpus test, with counts per case
  class. The `interop-profile` job moves to the same agent-action-capsule
  commit, at which `vectors/interop-profile-v0/` is unchanged.
- The service-evidence part (§1 to §5) moves to the same agent-action-capsule
  commit, so the guide pins one commit. Its results follow the 53-case vectors
  that CI runs. The positive class's row `envelope-7` is `UNKNOWN`, `UNKNOWN`,
  `not_evaluable`, where the earlier vectors gave `null`. The four
  signed-absence cases are routed under missing evidence. A test pins every
  case and result the §5 table quotes against those vectors
  (`test/interop/answer/testdata/`, pinned by digest).

### Added: the Rust `evidencebook` crate

The Rust implementation moved here from checkpointed-local-log with its
history (action-state-group/capsule-emit#290 stage B): `rust/evidencebook/`
in a `rust/` workspace, depending on `checkpointed-local-log` 0.3.1 from
crates.io. Rust CI (`rust.yml`: vocabulary gate, format, Clippy, tests,
rustdoc, package checks) and a crates.io publisher (`publish-rust.yml`,
`crates/evidencebook-v*` tags, separate from the Go module's `v*` tags) come
with it. The crate's own releases (0.0.4 and 0.0.5 so far) are in
`rust/evidencebook/CHANGELOG.md`.

### Changed: the Evidence Request protocol comes from agent-action-capsule

The request draft's protocol logic now lives in
`github.com/action-state-group/agent-action-capsule/go/evidencerequest`
(v0.8.0), next to `draft-mih-agent-evidence-request-00` and its conformance
vectors (action-state-group/agent-action-capsule#262). evidencebook builds on
it: request parsing and validation, the JSON and CBOR encodings and digests,
resolution (`Decide` with evidencebook's share policy, derivations and refusal
policy as its `Policy`), reply parsing, refusal and envelope signing, and the
CLL proof checks and encodings. The `Book`-bound responder and requester,
storage, and the MCP and A2A carriages stay here.

- **API.** Unchanged for callers: `EvidenceRequest`, `Subject`, `Coverage`,
  `Freshness`, `Refusal`, `Anchor`, `VerificationEntry`, `Envelope`, `Reply`
  and `Message` are now aliases of the package's types (same fields), the
  reason, subject and media-type constants are the package's values, and
  `EncodeRequest` and `ParseReply` errors still match `ErrInvalid`.
  `ErrInvalid` is now `evidencerequest.ErrInvalid` itself, so errors from the
  aliased types' own methods (`Refusal.Verify`, `Envelope.Verify`) match it
  too; its message reads `evidencerequest: invalid` instead of
  `evidencebook: invalid`.
- **Behaviour.** Stricter where the package is: a JSON request or reply with
  an unpaired UTF-16 surrogate escape is malformed instead of being decoded
  with U+FFFD, and a CLL inclusion or consistency proof deeper than the MMR can
  be is refused instead of panicking.

### Changed: sealed with `github.com/action-state-group/capsule-emit/go` v0.3.0

evidencebook now imports `github.com/action-state-group/capsule-emit/go`
v0.3.0 instead of `github.com/action-state-group/capsule-emit-go` v0.1.0, which
is archived. Go treats the two paths as different modules, so a build that
uses capsule-emit with evidencebook needs the new path too.

- **Wire.** A new record's capsule carries `spec_version`
  `draft-mih-scitt-agent-action-capsule-05` instead of `-04`, so its record id
  (capsule id) differs from what the previous writer sealed for the same
  entry. The third-party verifier guide quotes the new pins and record ids of
  `test/interop/answer`.
- **Old records.** `-04` records keep verifying and keep their ids
  (`testdata/legacy-citation-purpose/` is still `-04`).

### Breaking: record capsules no longer carry the link type as `citation_purpose`

Every link on an appended record used to be sealed as a `references[]`
entry with `citation_purpose` set to the link type (`cites`, `supersedes`,
`closes`, `acknowledges`, `rebuts`, `adjudicates`). Those are Evidence
Layer link types (agent-action-capsule `spec/REGISTRY.md` §18), not
registered citation purposes (§11). The entry now carries no
`citation_purpose`, which the Capsule draft allows (`-05`, "Cross-record
references": a `references` entry MAY carry it). No link type is mapped to
a citation purpose.

- **Wire.** A new record's capsule differs from what the previous writer
  sealed for the same entry, so its record id (capsule id) differs too.
  The header, including `links[].type`, is unchanged; it stays the one
  place the link type is recorded, committed through `agent_input_digest`.
- **Old records.** Records already sealed keep verifying and keep their
  ids: nothing here reads `citation_purpose` back (link types are read from
  the header), and a verifier reports an unregistered value as
  informational, never as a rejection. A book written by the previous
  writer opens, takes new records and bundles both
  (`testdata/legacy-citation-purpose/`, `TestLegacyCitationPurposeRecordsStillVerify`).
- **Conformance.** New CI job `registry-conformance` runs the AAC
  producer-side check `agent_action_capsule.registry_conformance` (AAC
  #241, pinned to its merge commit `7fabe9a5`)
  over every capsule a book seals with each link type and a bundle
  (`test/interop/capsules`), and requires the legacy records to fail it.

### Evidence interoperability profile: A2A carriage

`Book.A2A` returns an `A2AServer` that carries the evidence request exchange
over A2A 1.0 (JSON-RPC binding) as profile `evidence-interop/0` §11.4.4
writes it (agent-action-capsule `spec/interop-profile-v0.md` at main
`1ea8f75`). It is a carriage only: `Respond` answers, and the canonical
Artifact is the one the native stream and MCP get for the same request
octets.

- **Declaration.** `AgentCard` lists one extension,
  `https://datatracker.ietf.org/doc/draft-mih-agent-evidence-request/`, with
  `required` false and `Book.Manifest` as its `params` (byte for byte the
  manifest `evidence_manifest` serves), `application/cbor` and
  `application/json` as default modes, and no skill. The card is the same
  for every caller and carries nothing about what the book holds.
- **Request and reply.** `SendMessage` with `A2A-Version` `1.0` and the
  extension in `A2A-Extensions`, whose `Message` is `ROLE_USER`, names the
  extension and holds one `raw` part of one of the two media types, is
  answered by handing the decoded octets to `Respond` unchanged. The reply,
  envelope or refusal, is a `TASK_STATE_COMPLETED` `Task` with one `Artifact`
  marked with the extension URI and one `raw` part of the request's media
  type.
- **Authorization.** `Handle(ctx, request, authenticated)` takes the
  requester the host's A2A security scheme authenticated, the same value as
  `RespondOptions.AuthenticatedRequester`. Nothing A2A carries is read as an
  identity: not an Agent Card, `metadata`, `tenant`, `contextId` or
  `messageId`.
- **Not a request, not a refusal.** A `data` or `text` part, more than one
  part, another media type, a `taskId`, another role, a missing extension or
  version, or an unwanted output mode get a JSON-RPC or A2A error, which a
  requester treats as a transport condition. No task is ever left rejected
  or failed in place of a refusal.
- **No task state, no SDK.** No task is kept: `GetTask` and `CancelTask` find
  none and `ListTasks` lists none, so no task is visible to another client.
  Identifiers are 128 random bits. Streaming, push notifications and the
  extended card are declared off and answered with A2A's errors. The JSON-RPC
  surface is implemented with the standard library and the package's strict
  JSON decoder; the host supplies the HTTP endpoint and its authentication.
- **Conformance.** The reference verifier now runs every carriage vector
  over this responder's native, A2A and MCP replies (`carriage-invariance`,
  and the A2A and MCP negative halves of §11.4.8 as mutations of its
  replies); none is skipped. CI checks the canonical Artifact is
  byte-identical across the three.

### Evidence interoperability profile: MCP carriage

`Book.MCP` returns an `MCPServer` that carries the evidence request exchange
over MCP 2026-07-28 as profile `evidence-interop/0` §11.4.5 writes it
(agent-action-capsule `spec/interop-profile-v0.md` at main `a726b9f`). It is
a carriage only: `Respond` answers, and the canonical Artifact is the one the
native stream gets for the same request octets.

- **Tools.** `tools/list` lists exactly `evidence_manifest` and
  `evidence_request`, with the profile's input and output schemas, fixed
  descriptive text and no `x-mcp-header`; the list is the same for every
  caller. `evidence_manifest` returns `Book.Manifest` as its
  `structuredContent`, byte for byte the manifest served elsewhere.
  `evidence_request` decodes the base64url `request`, hands the octets to
  `Respond` unchanged, and returns `{"reply", "media_type"}` with `isError`
  false, for an envelope and for a refusal alike.
- **Authorization.** `Handle(ctx, message, authenticated)` takes the
  requester the host's transport authenticated, the same value as
  `RespondOptions.AuthenticatedRequester`. Nothing in the MCP message is
  read as an identity: not `_meta` (`clientInfo` included), not an argument,
  not the connection.
- **Not a request, not a refusal.** Arguments outside the schema, an unknown
  tool, a missing or other protocol version, a batch or a malformed message
  get a JSON-RPC error, which a requester treats as a transport condition.
  Notifications get no response. Carriage members (`_meta`,
  `inputResponses`, `requestState`, the JSON-RPC id) never reach `Respond`.
- **No SDK.** The MCP surface the profile needs is `server/discover`,
  `tools/list` and `tools/call` over JSON-RPC 2.0, implemented here with the
  standard library and the package's strict JSON decoder. The host supplies
  the transport (stdio or Streamable HTTP) and its authentication.
- **Conformance.** The reference verifier now runs the carriage vectors over
  this responder's native and MCP replies (`carriage-invariance`, and the
  MCP negative halves of §11.4.8 as mutations of its replies); CI checks the
  canonical Artifact is byte-identical across the two. (The A2A-only
  carriage vectors were skipped until the A2A carriage, above.)

### Evidence interoperability profile (responder)

`Respond` now implements the responder side of the Minimum Evidence
Interoperability Profile `evidence-interop/0` (agent-action-capsule
`spec/interop-profile-v0.md`, read at main `6cae247` and the conformance
vectors at `5abc0da7`), over the request map of
`draft-mih-agent-evidence-request-00`. It implements only what the profile
defines.

- **Wire.** Requests are read in JSON or deterministic CBOR (`Message`: octets
  and media type) in the draft's shape: a one-member subject map,
  `expected_pin` as a digest, `min_freshness` as a size or a time. Unknown
  members are ignored; duplicate members, trailing bytes and case-variant
  names are not members. The profile's validation order decides the reason,
  so both coverage members or neither is `coverage_unsatisfiable`. The
  request digest is over the octets as received. All 33 `request.json` and
  18 of 19 `resolution.json` vectors pass (the 19th describes a log with no
  positions, which a Checkpointed Local Log cannot be).
- **Envelope.** An answer is the profile's signed envelope (`Envelope`):
  `profile`, `request_digest`, `subject`, `derivation`, `anchor` (`digest`,
  and `freshness` under `min_freshness`), `artifact_digest`, `verification`,
  `responder` and `issued_at`, signed in JCS, with the Artifact and the
  verification material beside them. The reply is in the request's encoding.
  The anchor is a checkpoint named by the SHA-256 of its signed statement.
- **Canonical Artifact.** A `record` answer is the record's own octets, proven
  with the profile's `cll-checkpoint`, `cll-inclusion-proof` and
  `cll-consistency-proof` material. Other forms use the stream binding's
  member names. No product-named member and no producer self-report.
- **Derivations.** `history_card/1` is served for `full_history` and
  `checkpoints`, to anyone. A recipient-specific projection is a derivation
  named by the digest of its definition (`RegisterDerivation`), answered only
  to its recipient, with only its declared fields.
- **Disclosure hygiene.** Every artifact is scanned against its declared field
  set and for the book's principal and counterparty references before
  release. Records of answered requests and of disclosures are never served.
- **Cost and abuse.** `Config.Responder` (`ResponderPolicy`): per-requester
  rate limits, request and answer size caps (`policy_declined`), the
  `uniform` refusal policy, `deadline_unmet`, and `retention_expired` for a
  lapsed retention commitment (`CommitRetention`). Unauthenticated and
  malformed requests never grow the log or force a checkpoint.
- **Absence.** `RecordAbsence` writes the profile's absence record
  (`AbsenceRecord`) and takes the route from the committed request.
- **Discovery.** `Manifest` returns the profile's history-free manifest.
- **SCITT stub.** Its receipts are never served as checkpoints or proofs;
  its bundle inclusion evidence is labelled `scitt-receipt-unverified`.

### Breaking

- `Respond(ctx, Message, RespondOptions)` returns `(Reply, Record, error)`.
  The old JSON request shape (`subject.kind`, `expected_pin` as
  `{root, mmr_size}`, `min_freshness` as `{size, time}`), `ArtifactResponse`,
  `Response`, `Pin` and `ParsePin` are removed. `range` positions are
  zero-based log positions.
- `RecordResponse` takes the reply `Message` as received.
  `OutcomeArtifactFailed` is now `artifact_verification_failed`;
  `refusal_nonconformant` records a signed refusal with an unregistered
  reason. `ResponseStatement.ArtifactKind` is gone; `NotChecked` lists the
  properties the profile gives no method for.
- An answer is no longer an Evidence Bundle and never carries record headers.
  Header fields are disclosed only through a registered derivation. Holder
  disclosures stay on `Bundle`, checked with `VerifyBundleAnswer`.
- An unauthenticated `min_freshness` request no longer makes the book
  checkpoint: it is answered from an existing checkpoint or refused
  `coverage_unsatisfiable`.
- `AbsenceStatement` is replaced by `AbsenceRecord`.

### Security

- `Respond` now fails closed. Protected subjects (everything except
  `checkpoints` and the `history_card/1` derivation) are answered only to a
  requester the transport has authenticated, passed in the new
  `RespondOptions.AuthenticatedRequester`.
  An unauthenticated caller gets only public subjects; every other request is
  answered with a signed refusal, reason `not_authorized`. This holds under
  every `history_segments` tier, including `peers`.
- A declared requester id is no longer trusted. `RespondOptions.RequesterID`
  is deprecated and ignored: it selects no relationship and grants no scope.
  Counterparty scope comes only from an authenticated identity that matches a
  `counterparty_ref`; a `counterparty_ref` value alone grants nothing.
- Requester ids are no longer stored in answerable records. The
  `evidence_request_answered` record no longer carries `requester_ref` (the
  field is removed from `AnsweredStatement`), in any form. It still records the
  request digest, subject kind, outcome, reason, relationship class and anchor.

### Breaking

- Callers that relied on `RequesterID` to unlock answers must now
  authenticate the requester at the transport and set
  `AuthenticatedRequester`. Without that change, those callers receive
  `not_authorized` for every subject except `checkpoints` and the
  `history_card/1` derivation.
- Records sealed before this change are immutable and still contain the
  `requester_ref` they were written with. Operators of books with such
  history should take that into account before serving `full_history` or
  `range` subjects that cover it.

### Changed: the CLL Go module moves to `checkpointed-local-log/go`

evidencebook imports `github.com/action-state-group/checkpointed-local-log/go`
v0.1.0 instead of `github.com/action-state-group/cll-go`. Go treats the two
paths as different modules.

### Fixed: a bundle's checkpoint states its log id

- A book's bundle writes `checkpoint.log_id`, the log id its checkpoint
  statement signs (`BundleCheckpoint.LogID`). capsulectl's `verify --bundle`
  up to v0.1.0-rc8 required it and failed every book bundle without it.
- `VerifyBundle` refuses a stated `checkpoint.log_id` other than the signed
  one when the checkpoint is authenticated, and one that is present but not
  a non-empty string (`ErrInvalid`). A bundle that states none reads as
  before.
