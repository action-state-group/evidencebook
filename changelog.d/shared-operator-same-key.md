### Fixed: `xdealv1.SharedOperator` labels a party's own key with no stamped time (#822)

- An adjudicator key equal to a party key is now labeled "not independent (shared operator)" whatever the stamped time, a zero one included. One key is one operator at every time, and only a binding's validity window needs the time. Before, a ruling with no witnessed stamp time went unlabeled even when its adjudicator was a party.
