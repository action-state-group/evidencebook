### Fixed: on x-deal-v1, an answer that was not applied authorizes nothing (#790)

- `deal perform` and `deal settle --sent` refuse a check whose answer `deal note --kind approval` marked not applied: a later check or a changed detail came first, or the check was already answered. The refusal says to check again and seal the user's answer to the new check. x-deal-v0 already refused this.
