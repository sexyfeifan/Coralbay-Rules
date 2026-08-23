# Client template sources

`perfect-panel/` contains the MIT-licensed templates from
[perfect-panel/subscription-template](https://github.com/perfect-panel/subscription-template),
pinned at commit `e5b14404c28fee0382329ae0d5247b1e6209872c`.

At sync time CoralBay Rules:

- replaces the Clash template with the CoralBay 666OS Pro_cn variant;
- keeps Perfect Panel's Stash node renderer, and replaces its policy groups,
  regional filters, automatic selection, load balancing, rules, and providers
  with a Stash-native 666OS Pro_cn mapping;
- mirrors the remaining client templates unchanged, because those clients do
  not natively consume Mihomo MRS rule sets.

The upstream MIT license is preserved in `perfect-panel/LICENSE`.
