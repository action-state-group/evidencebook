### Fixed: an x-deal-v1 holder no longer holds a counterparty agreement of its own role (#884)

- `xdealv1.ReceiveProfile.Admit` holds a counterparty's deal book only when its agreement takes the other role than the holder's agreement, or, at an offerer holding only its offer, the role the offer did not take. Before, such an agreement was held and the History card read the deal as "stamped by both".
