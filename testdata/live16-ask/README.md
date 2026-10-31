# live-16 07-ask: the capsules plugin's sealed requests and stevenmih-1's replies

Each file holds one request the capsules plugin sealed and the reply that came
back, from evidence-network/loop
`findings/2026-10-31-live-16/07-ask/stevenmih-1/attempts/` at commit
`2c9365b5a756315348c3eb5abd608881ec0229de`:

| File | Attempt | Profile |
|---|---|---|
| `checkpoints.json` | `20261031T123303Z` | `evidence-interop/0` |
| `history_card.json` | `20261031T123304Z` | `evidence-interop/0` |
| `exchange.json` | `20261031T123305Z` | `evidence-interop/1` |

- `request` is `requests[0].header.statement.request` of the attempt's
  `*.ledger.json`. It is the request as the plugin sent it, in JCS, including
  the members the plugin's deployment profile adds (`audience`,
  `requester_id`, `requester_auth`).
- `reply` is `answer` of the attempt's `*.response.json`, the envelope
  stevenmih-1 signed under key
  `1dd25d9938ba4841f5e127e39e8dcc71df82b2072d9186ece75e431063ab26d9`, which
  also signs its checkpoints.

`TestRequestSentLive16` (`request_sent_test.go`) records each request with
`Book.RequestSent` and checks that the recorded digest is the reply's
`request_digest` and that the reply verifies as an artifact
(evidencebook#245).
