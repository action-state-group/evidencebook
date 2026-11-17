<!-- changelog: python/CHANGELOG.md -->
- The ARD demo's seller window, reopened after a deal, no longer takes a deal request it already answered again (it left a failed "already open" line in the seller's log); a request whose first answer file exists is skipped. A window closed between admitting a deal and writing that first answer can still take the request again.
