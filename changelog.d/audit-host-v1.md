### Fixed: `deal audit-host` reads x-deal-v1 deals (#758)

- **The bug.** `deal audit-host`, the daily check of host actions, read deals only through the x-deal-v0 path. So every consequential action on an x-deal-v1 deal was listed as `unrecorded`, even when the deal held it.
- **The fix.** It now also reads each x-deal-v1 deal's own records, as stored and writing nothing. A payment from `deal settle --sent` on covers the host's pay record, matched by deal id, or by amount, currency, merchant and time, exactly as on x-deal-v0. A payment or refund whose send failed covers nothing, and neither does a refund received, which is the other side's act. Each deal in a store holding both versions covers its own actions.
