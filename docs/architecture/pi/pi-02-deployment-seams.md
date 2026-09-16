# PI-02 deployment and plugin seams

Status: source-derived sidecar for PI-02/A; deployed state unverified

The local `teddycloud-enhancements` snapshot is `87f153a`. Its
`components.lock` binds six release archives in a fixed order: Common 1.0.1,
Tonie Manager 1.2.1, Current Tonie 1.0.1, Unknown Tonie 1.0.1,
YouTube2Tonie 1.1.1 and NFC Sync 1.1.2. `install.sh` checks names,
versions, archive digests and tar paths before installing; `--check` performs
the component checks in a disposable directory. This is source evidence, not
proof that the same archive revisions are deployed.

| Seam | Installed path or process | Compatibility fixture |
| --- | --- | --- |
| Common | `/opt/teddycloud/data/www/custom_card/teddycloud-common.js` | Asset presence, browser global `TeddyCloudTools`, pinned Common and contract versions |
| Manager, Current, Unknown | `/opt/teddycloud/data/www/plugins/{tonie-manager,current-tonie,unknown-tonie}` | Each `plugin.json` exists, discovery returns its directory name and page loads its pinned Common |
| YouTube2Tonie | `/opt/teddycloud/data/www/plugins/youtube2tonie`, backend under `/opt/youtube2tonie`, `youtube2tonie.service` | Discovery plus backend health/job/part-assignment proxy and interrupted install |
| NFC Sync | `/opt/teddycloud-nfc-sync`, `teddycloud-nfc-sync.service` and `.timer` | Separate worker schedule, dry-run configuration and controlled rollback |
| Meta installer | Six archives and `components.lock`; backups under `/opt/teddycloud/data/local-suite-backups` | Wrong digest/order/version rejection; idempotent second install; reverse-order rollback |

These paths are defaults, not universal installation requirements. The meta
installer accepts `TC_ROOT`, `TC_PREFIX`, `TC_BACKUP_DIR`, `SYSTEMD_DIR`,
`Y2T_ROOT` and `NFC_SYNC_INSTALL_ROOT` overrides. Its no-change second run
removes the unused newly created meta-backup directory. Do not classify every
backup as obsolete without checking the manifest and rollback dependency.

The installer-side v1 integration contract says a leading `-` in `TagValid`
denotes removal. The reviewed TeddyCloud server snapshot sends removal via
MQTT in `tbs_tag_removed` and does not call `sse_sendEvent` there. Existing
browser plugins also use RTNL-log removal. Fixture provenance must distinguish
accepted client inputs from server-emitted events. The installation contract
cannot be used alone as evidence of server behavior.

The router's `/api/plugins/get` handler reads plugin directory names and
serializes the array itself. It does not validate `plugin.json` or wrap the
result under a `plugins` key. The pinned Web UI submodule is uninitialized in
this checkout. Its exact source has since been inspected read-only in the
[Web UI seam note](pi-02-webui-seam.md); navigation/iframe runtime behavior
still needs disposable validation.

Sources in the sibling repository: `install.sh`, `components.lock`,
`scripts/test-meta-installer.sh`,
`docs/architecture/teddycloud-integration-contract-v1.md` and the six
component `install.sh` files. Server sources:
[plugin discovery](../../../src/handler_api.c) and
[box state events](../../../src/toniebox_state.c).

Acceptance for PI-02/A: label each seam as source-derived until disposable
installer tests or sanitized deployment observation confirms it. Include one
version-mismatch and one missing-Common case; retain installer rollback
reconciliation as a later migration gate.
