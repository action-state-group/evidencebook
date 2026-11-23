<!-- changelog: capsulectl/CHANGELOG-deal.md -->
### Fixed: the share gate reads a card number by its network prefix (#787)

- The share gate refused a copy for any 13 to 19 digit run that passed the card checksum (Luhn), so an honest merchant order number such as `1002003004005005` blocked `deal report --share`. It now reads a card number as a payment reference's check does (#772): a card network's prefix and the checksum. Both checks now also read Diners Club 30x, Troy, BORICA, China T-Union, GPN, Uzcard, Napas and HUMO. Card numbers of the major networks are still refused, alone, with separators, or with a code after them; UATP airline cards, which start with 1, are left out on purpose.
