# Disclosure

Part of the [deal skill](../SKILL.md): its [Rules](../SKILL.md#rules-never-break-these) and [Points of no return](../SKILL.md#points-of-no-return) bind every step here, and `ID`, `OFFER` and `--profile deal` are as its [Procedures](../SKILL.md#procedures) say.

Giving anyone the user's name, phone, email, address, a pickup spot or area,
a login or a code: typed into a page (a checkout's delivery form included) or
sent in a message. Use the deal of the errand it belongs to.

1. Fill the form or message up to the point of sending, without sending.
2. **Final review:** `deal check` with `"action": "share_contact"` (or
   `"share_credentials"`), `"disclosing_to"` and `"disclosing"`: who receives
   it, and the class of every field you are about to give, for example
   `{"action":"share_contact","disclosing_to":"counterparty","disclosing":["name","email","address"]}`.
   `"disclosing_to"` is `"counterparty"`, `"platform"` (with `"recipient":
   {"name": ..., "domain": ...}`) or `"other"` (with a `"recipient"` naming
   them). The check pauses the first time a class goes to this recipient and
   passes with a `note` when you have told them before.
3. Any request to the user to go ahead uses the check's `approval_text`, as
   it is. On `"proceed": true`, note it (step 4) and then send. On `pause`,
   show the card, seal the user's answer as in [Purchase](purchase.md#purchase) step 8, and go on
   only if that returns `"proceed": true`.
4. With the form filled and **not yet sent**: `deal note --kind disclosure`
   naming what you are giving, by class, with the value (it stays on this
   device; the sealed record keeps only the class and a commitment). One
   disclosure per send, contact details and credentials separately:

   ```sh
   capsulectl --profile deal deal note --deal ID --kind disclosure --input d.json
   # d.json: {"channel":"sms","fields":[{"class":"phone","value":"the number you gave"}]}
   ```

   To a platform, add `"to":"platform"` and `"who":{...}` named as in its
   check; to anyone else (a courier), `"to":"other"` and `"who":{...}`:
   without them the note is for the counterparty, and a check that named
   someone else does not cover it. `class` is `name`, `phone`, `email`, `home_address`, `address`,
   `pickup_location`, `pickup_area`, `other_contact`, `credential`,
   `verification_code`, `payment_card` or `id_document`. If a class would go
   to this recipient for the first time and no check the user approved named
   it, the note seals nothing, returns `"proceed": false` with the classes
   `held`, and exits 7. **Do not send.** Check again naming them.
5. Send.
