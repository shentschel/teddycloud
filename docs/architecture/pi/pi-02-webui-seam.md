# PI-02/A pinned Web UI seam (CF-04)

Source-only checkpoint on `teddycloud` `dcc598d` and the `teddycloud_web`
gitlink `e2f40c2c69a3b2b46093ddc5542773dd0fa0067e`. The submodule is
**not initialized locally** (`git submodule status` prints `-e2f40c2…`;
`.git/modules/teddycloud_web` and the pinned object are absent). Its exact
commit tree and files were read through the public GitHub API/raw source,
without initializing it or using browser/production credentials. This is
source evidence, not a deployed UI or plugin compatibility test.

## Observed source contract

| Seam | Pinned source evidence | Compatibility consequence |
| --- | --- | --- |
| Base path and route | [`App.tsx` 157–201](https://github.com/toniebox-reverse-engineering/teddycloud_web/blob/e2f40c2c69a3b2b46093ddc5542773dd0fa0067e/src/App.tsx#L157-L201), [`App.tsx` 229–230, 299–308, 340–343](https://github.com/toniebox-reverse-engineering/teddycloud_web/blob/e2f40c2c69a3b2b46093ddc5542773dd0fa0067e/src/App.tsx#L229-L343), [`.env` 1–2](https://github.com/toniebox-reverse-engineering/teddycloud_web/blob/e2f40c2c69a3b2b46093ddc5542773dd0fa0067e/.env#L1-L2) | `BrowserRouter` uses `VITE_APP_TEDDYCLOUD_WEB_BASE` (`/web` in source `.env`); plugin pages have section routes and `/plugin/:pluginId` standalone route. Build/deployment value is unverified. |
| Discovery and manifest | [`TeddyCloudProvider.tsx` 321–381](https://github.com/toniebox-reverse-engineering/teddycloud_web/blob/e2f40c2c69a3b2b46093ddc5542773dd0fa0067e/src/provider/TeddyCloudProvider.tsx#L321-L381), [server route 123](../../../src/server.c#L123), [prefix dispatch 522–529](../../../src/server.c#L522-L529), [handler 4769–4801](../../../src/handler_api.c#L4769-L4801) | UI GETs `/api/plugins/getPlugins`, expects a **bare folder-name array**, sorts it, then GETs `/plugins/{folder}/plugin.json`. `pluginName` is required; folder is `pluginId`; `standalone`, section and icon come from the manifest. Server registers `/api/plugins/get`, but its current prefix match also catches `getPlugins`. This is an implicit alias, not an explicit route contract. Handler scans directories but does not validate manifests. |
| Navigation | [`ToniesSubNav.tsx` 35–64](https://github.com/toniebox-reverse-engineering/teddycloud_web/blob/e2f40c2c69a3b2b46093ddc5542773dd0fa0067e/src/components/tonies/ToniesSubNav.tsx#L35-L64), [`CommunitySubNav.tsx` 49–97](https://github.com/toniebox-reverse-engineering/teddycloud_web/blob/e2f40c2c69a3b2b46093ddc5542773dd0fa0067e/src/components/community/CommunitySubNav.tsx#L49-L97), [`PluginPage.tsx` 22–74](https://github.com/toniebox-reverse-engineering/teddycloud_web/blob/e2f40c2c69a3b2b46093ddc5542773dd0fa0067e/src/pages/community/PluginPage.tsx#L22-L74) | Section-filtered plugin links choose embedded section route or standalone `/plugin/{id}`. Page selects section subnav and passes folder ID to `PluginContainer`. Missing or invalid manifests are excluded from loaded navigation. |
| iframe and static files | [`PluginContainter.tsx` 19–70, 109–169](https://github.com/toniebox-reverse-engineering/teddycloud_web/blob/e2f40c2c69a3b2b46093ddc5542773dd0fa0067e/src/components/community/plugin/PluginContainter.tsx#L19-L169), [server static fallback 546–578](../../../src/server.c#L546-L578), [deployment paths](pi-02-deployment-seams.md) | Embedded page points at absolute `/plugins/{pluginId}/index.html`. Same-origin DOM access enables height measurement, style injection and `error-404` detection; cross-origin measurement is caught, but equivalent cross-origin behavior is not established. Static fallback serves paths below configured `wwwdir`; installer path/base-path alignment still needs disposable validation. |
| Library rendering | [`LibraryPage.tsx` 14–21, 50–67](https://github.com/toniebox-reverse-engineering/teddycloud_web/blob/e2f40c2c69a3b2b46093ddc5542773dd0fa0067e/src/pages/tonies/LibraryPage.tsx#L14-L67), [`useFileBrowserCore.tsx` 139–196](https://github.com/toniebox-reverse-engineering/teddycloud_web/blob/e2f40c2c69a3b2b46093ddc5542773dd0fa0067e/src/components/tonies/filebrowser/hooks/useFileBrowserCore.tsx#L139-L196), [server `queryPrepare` 138–173](../../../src/handler_api.c#L138-L173), [`fileIndexV2` 612–688](../../../src/handler_api.c#L612-L688) | `/tonies/library` switches `special=library` versus `special=custom_img`; `FileBrowser` expects `{files:[...]}` from `/api/fileIndexV2?path=...&special=...`. Server selects library root or `wwwdir/custom_img`. `custom_img` GET can create the directory, so it is not automatically read-only. |

CF-04 gaps: test the implicit `getPlugins` alias and bare-array/manifest chain
against disposable server files; verify iframe same-origin, missing-index error,
standalone and section navigation; validate `/web` build basename and
`/plugins` static-root installation. The locally absent submodule prevents a
local build/test in this checkpoint. No observed browser, deployment or
production behavior is claimed.

## CF-01…05 checkpoint disposition

`source-covered` means the cited source establishes the narrow mechanism;
runtime/wire assertions remain deferred.

| Finding | Disposition | Reason and remaining closure |
| --- | --- | --- |
| CF-01 GET write-back | source-covered | [`getTagInfoJson` calls `saveTonieInfo(..., true)`](../../../src/handler_api.c#L4352-L4360); exact before/after metadata diff and download effects deferred. |
| CF-02 removal/SSE | source-covered | [Placement emits SSE](../../../src/toniebox_state.c#L24-L39), [removal emits MQTT only](../../../src/toniebox_state.c#L42-L55), [SSE broadcasts active subscriptions](../../../src/handler_sse.c#L138-L156); client fallback, reconnect and two-box isolation deferred. |
| CF-03 source/auth/classification | source-covered (server side) | [`hasCloudAuth` and `sourceInfo` differ](../../../src/handler_api.c#L4390-L4396) [by assigned/content model](../../../src/handler_api.c#L4421-L4430); client adapter policy and physical-card classification deferred. |
| CF-04 pinned UI/deployment | source-covered (UI source) | Pinned navigation, discovery, iframe and library seams above; disposable build, manifest and installer tests deferred. |
| CF-05 statuses/framing | deferred | [Router error translation is incomplete](../../../src/server.c#L529-L578); one [synthetic CT-15 protobuf case](../../../tests/fixtures/compatibility/ct-15-freshness-protobuf.json) exists, but negative framing, auth/status cases and wire validation remain open. |
