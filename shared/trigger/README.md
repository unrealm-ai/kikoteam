# Shared trigger matcher

Pure Go, standard-library-only matching used by EventHub, Manager admission and
Platform's unsaved-rule preview. No persistence, identity or side effects.

- Keywords split on ASCII/Chinese comma, trim whitespace and match any nonempty
  token with case-insensitive substring comparison. This intentionally replaces
  Kiko's former case-sensitive whole-string behavior, per the approved migration.
- Regex is Go RE2 syntax. JS `/pattern/flags`, lookbehind and backreferences are
  rejected on save and preview. Inline Go flags such as `(?i)` are supported.
- Platform owns employee-level named rule metadata and enable/channel selection;
  only enabled rules are projected into channel runtime filters.

Run `go test -race ./...` here; Backend/EventHub use local module replacements.
