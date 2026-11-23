### Added: `report.Receipts.Gate`, the caller's own check of each carried receipt ([#735](https://github.com/action-state-group/evidencebook/issues/735))

- When set, `Receipts.Gate` runs on each carried receipt bundle after the page gate, and a non-nil error refuses the receipt, so no page is built. capsule-cli's page gate no longer reads deal sections (capsule-cli#609). The caller that defines the receipts' format checks them here, and `report` stays generic and links no format's code. capsulectl-book passes the deal plugin's `capsulectl deal verify --bundle`.
