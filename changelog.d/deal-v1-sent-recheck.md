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
