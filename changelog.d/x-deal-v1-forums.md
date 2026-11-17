- **x-deal-v1 agreements carry the recourse clause** (evidencebook#461; #404). An offer and its agreement name
  `forums`, the forums both sides accept for a dispute. The list is empty exactly when there is no adjudicator,
  which allows no dispute, and the agreement's forums must equal the offer's. `xdealv1.CheckDispute` and
  `CheckRuling` refuse a dispute or ruling outside them, so no forum is picked at dispute time.
