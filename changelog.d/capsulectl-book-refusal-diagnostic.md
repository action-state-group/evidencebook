### Added: `capsulectl-book book respond` tells the holder's operator the reason the gate decided on (#580)

- Under the uniform refusal policy a refused request is sent `policy_declined` in place of a reason that would disclose what the policy protects (#473). When the two differ, `book respond` now prints one local diagnostic line on stderr naming the request digest, the reason sent and the reason decided, through `RespondOptions.OnRefusal` (#582). The decided reason is never in the reply, the refusal record, the book or the command's JSON result; a test holds each of those.
