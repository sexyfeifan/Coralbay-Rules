# Mirrored UI icons

The files in `icons/` are UI icons from
[Koolson/Qure](https://github.com/Koolson/Qure), pinned at upstream commit
`b16b260625f873266f6a6a9b88710132774997b8`.

They are bundled so generated Mihomo/OpenClash configurations do not depend on
GitHub availability at runtime. They are not used for routing decisions.

The six continental icons (`Asia_Map`, `Europe_Map`, `America_Map`, `LA_Map`,
`Oceania_Map`, `Africa_Map`) use the same pinned Qure revision. The original
six regional icons keep their existing Qure style.

`icons/flags/` contains 251 country/territory/organization PNGs rendered at
160px width from [hampusborgos/country-flags](https://github.com/hampusborgos/country-flags)
revision `c09927e63705529bbf59ca6684cd9b23225dddad` (retrieved 2026-09-28).
That project's README identifies Wikimedia Commons as its source and describes
the flags as public domain, while noting other restrictions may apply to flag
use. The PNGs are rendered with `@resvg/resvg-js@2.6.2`, without system fonts;
they are not edited by an image-generation service. See
`scripts/render-country-flags.cjs` for the maintainer-only rendering procedure.

All 284 images are listed with dimensions, byte sizes and SHA-256 digests in
`icon-manifest.json`. Images and the inventory are bundled into the Docker
image **and compiled into the program**. `/_assets/icons/flags/de.png`, for
example, is available without login and without a successful first rule sync.
Rule synchronization still copies the complete tree into the local mirror;
the console separately reports bundled availability and verified mirror count.
No client-side GitHub/CDN requests are needed. Updating flag artwork is an
explicit versioned maintainer operation, not part of the rule update schedule.
