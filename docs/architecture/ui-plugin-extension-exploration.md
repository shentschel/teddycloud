# UI plugin extension exploration

Status: exploration
Scope: extending existing TeddyCloud screens without DOM patching or replacing
whole pages

## Decision summary

TeddyCloud should expose a versioned extension API based on stable UI slots and
commands. Plugins should be able to enhance the Tonies overview and library
without implementing a separate manager.

A hybrid model is recommended:

- Declarative contributions for menus, actions, badges, filters and panels.
- Trusted React modules for complex interactive components.
- Sandboxed iframes for untrusted or standalone applications.

CSS selectors, mutation observers and direct DOM replacement are explicitly not
part of the contract.

## Current limitation

The current plugin loader reads `plugin.json`, adds a navigation entry and opens
`/plugins/<id>/index.html` in an iframe. Plugins are therefore isolated pages.
They cannot use supported APIs to add an action to a Tonie card, a filter to the
overview or a panel to a core dialog.

The existing React UI already has clear component boundaries such as
`ToniesList`, `ToniesFilterPanel`, `TonieCard`, `LibraryPage` and `FileBrowser`.
These are suitable locations for explicit extension slots.

## Extension model

```text
plugin.json
   |
   v
Plugin loader -- validates API version and permissions
   |
   v
Extension registry
   |
   +-- commands
   +-- routes/navigation
   +-- filters and toolbar items
   +-- card actions/badges/content
   +-- dialog/detail panels
   +-- library file actions
   +-- event subscriptions
```

Plugins register contributions against semantic slot IDs. The host owns layout,
ordering, loading state, error boundaries, accessibility and lifecycle.

## Initial stable slots

| Slot ID | Purpose |
| --- | --- |
| `app.navigation.tonies` | Tonies navigation entries |
| `tonies.toolbar.primary` | Global actions above the overview |
| `tonies.filters` | Additional filter controls |
| `tonies.query.filters` | Validated filter contributions translated to the server query contract |
| `tonie.card.header` | Badges such as Original or Custom Card |
| `tonie.card.body.after` | Additional compact card information |
| `tonie.card.actions` | Actions such as Copy |
| `tonie.details.tabs` | Additional detail tabs/panels |
| `library.toolbar` | Library-wide commands |
| `library.file.actions` | Commands for a selected TAF/file |
| `settings.plugins` | Plugin-specific settings panels |

“Every UI location” should mean that new semantic slots can be added deliberately,
not that plugins may inject arbitrary HTML at any DOM node. This keeps the
contract understandable and update-safe.

## Manifest V2 sketch

```json
{
  "id": "org.example.tonie-copy",
  "name": "Tonie Copy",
  "version": "2.0.0",
  "pluginApi": "1.x",
  "ui": {
    "mode": "trusted-module",
    "entry": "assets/plugin.js"
  },
  "permissions": [
    "tags:read",
    "assignments:write",
    "events:read"
  ],
  "contributes": {
    "commands": [
      {
        "id": "tonie-copy.copy",
        "title": "Auf Karte kopieren"
      }
    ],
    "slots": [
      {
        "slot": "tonie.card.actions",
        "command": "tonie-copy.copy",
        "when": "tonie.kind == 'original'"
      }
    ]
  }
}
```

The expression language for `when` must be small, documented and interpreted by
the host. It must not evaluate arbitrary JavaScript.

## SDK contract

The host SDK should expose only stable services:

- typed tag, content and assignment API clients;
- commands and command context;
- dialogs, notifications and confirmation flows;
- current theme and approved design tokens;
- translations and plugin-owned translation namespaces;
- navigation;
- typed events such as tag placed, removed and playback changed;
- namespaced plugin storage;
- refresh/invalidation requests after mutations.

Plugins must not import internal application components or application state.
Reusable public UI components should live in a small versioned SDK package.
Filter contributions declare supported query fields and values. The host validates
and sends them through the paginated library API; plugins do not receive or replace
the complete result list, decide canonical identity, or remove arbitrary records.

## Example: replacing the separate Tonie Manager

The existing manager capabilities can become contributions to the normal Tonies
screen:

| Capability | Contribution |
| --- | --- |
| Original/Custom badge | `tonie.card.header` |
| Custom Cards toggle | `tonies.filters` |
| Details toggle | `tonies.toolbar.primary` |
| Copy button | `tonie.card.actions` |
| UID/rUID details | `tonie.details.tabs` or `tonie.card.body.after` |
| Deduplicated preferred view | Core query option, not a DOM transform |

Classification, deduplication and preferred-record selection are domain logic.
They should move to the TeddyCloud API rather than remain UI plugin code.

## Loading and lifecycle

1. Fetch manifests and reject unsupported API versions.
2. Validate IDs, contributions and requested permissions.
3. Load modules lazily only when one of their slots is visible.
4. Register every contribution through the host registry.
5. Render each contribution inside an error boundary.
6. Require cleanup functions for events, timers and registrations.
7. Unregister everything on disable, upgrade or navigation teardown.

Ordering should be deterministic through `before`, `after` and numeric priority,
with plugin ID as the final tie-breaker.

## Security boundary

A same-origin React module has the same browser privileges as TeddyCloud itself.
Manifest permissions improve transparency and API discipline, but cannot sandbox
such code.

Therefore:

- Trusted modules require an explicit administrator trust decision.
- Unknown plugins default to a sandboxed iframe and `postMessage` bridge.
- The iframe receives short-lived capability handles, not unrestricted API
  access.
- Plugin files should have integrity hashes; optional signatures can follow.
- Backend functionality runs as a separate least-privileged sidecar, never as a
  dynamically loaded C library.
- CSP, path validation, upload limits and manifest schema validation are required.

For a private LAN installation, trusted mode is a reasonable convenience, but
the UI must clearly state that installing such a plugin is equivalent to running
local code with TeddyCloud access.

## Compatibility policy

- Version the plugin API independently from TeddyCloud releases.
- Support a documented compatibility range such as `pluginApi: 1.x`.
- Deprecate slots for at least one regular release before removal.
- Include an automated compatibility test kit for plugin repositories.
- Preserve iframe plugins as a legacy mode during migration.
- Never use source file paths or React component names as public extension IDs.

## Proposed implementation phases

### PLUGAPI-001 — Registry and command proof of concept

- Add a typed extension registry and command service.
- Add `tonie.card.actions` and `tonies.toolbar.primary` slots.
- Convert the Copy action into a proof-of-concept contribution.
- Add ordering, cleanup and error-boundary tests.

Estimated effort: 12-18 hours.

### PLUGAPI-002 — Manifest V2 and trusted module loader

- Define and validate the manifest schema.
- Enforce plugin API compatibility before loading.
- Add lazy module loading and lifecycle handling.
- Keep current iframe manifests compatible.

Estimated effort: 14-22 hours.

### PLUGAPI-003 — Tonies overview coverage

- Add filter, card header/body, detail panel and list query extension points.
- Define validated server-query contributions with deterministic composition.
- Move classification and preferred-record logic behind core API contracts.
- Migrate Tonie Manager features into the standard overview.

Estimated effort: 20-32 hours.

### PLUGAPI-004 — Library and settings coverage

- Add library toolbar/file actions and plugin settings panels.
- Provide selection context and refresh/invalidation APIs.
- Migrate suitable existing plugin workflows.

Estimated effort: 14-22 hours.

### PLUGAPI-005 — Sandboxed bridge and developer kit

- Define the iframe message protocol and capability handles.
- Add SDK types, example plugins, documentation and a compatibility test kit.
- Add CSP and integrity verification.

Estimated effort: 20-32 hours.

## Recommendation

Start with `tonie.card.actions` and `tonies.filters`, then migrate the existing
Copy button and Custom Cards filter. This validates the architecture against real
requirements before broad slot coverage is added.

The complete first-generation platform is approximately 80-126 hours. A focused
proof of concept for the Copy action is feasible in 12-18 hours.
