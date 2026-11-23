<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Fixed: the share gate reads a card number by its network prefix (#787)

- The share gate refused a copy for any 13 to 19 digit run that passed the card checksum (Luhn), so an honest merchant order number such as `1002003004005005` blocked `deal report --share`. It now reads a card number as a payment reference's check does (#772): a card network's prefix and the checksum. Troy (9792) joins the prefixes both checks read. Card numbers of each network are still refused, alone, with separators, or with a code after them.
