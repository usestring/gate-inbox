# Changelog

## [0.11.0](https://github.com/usestring/gate-inbox/compare/v0.10.1...v0.11.0) (2026-10-03)


### Features

* keep newest turn full and expand shortened turns on hover ([#220](https://github.com/usestring/gate-inbox/issues/220)) ([489e83a](https://github.com/usestring/gate-inbox/commit/489e83a1119de61016ecf2f8182d91979a607378))
* read, relay, answer and preview child questions through each CLI's own record (S-145245) ([#171](https://github.com/usestring/gate-inbox/issues/171)) ([293baea](https://github.com/usestring/gate-inbox/commit/293baea21bb6dee4e97bce322b8df06ecea65437))
* read, relay, answer and verify OpenCode questions from the parent (S-145234) ([#173](https://github.com/usestring/gate-inbox/issues/173)) ([16fe36f](https://github.com/usestring/gate-inbox/commit/16fe36f6d3fcbdcc9455b1612894fe3eed3d60cd))
* **steering:** cut Gate Inbox's injected instructions to plain sentences ([#212](https://github.com/usestring/gate-inbox/issues/212)) ([05a129f](https://github.com/usestring/gate-inbox/commit/05a129ff38601dc7ec7e90d5e0762507d4c463a9))
* **ui:** fit more sessions on a phone-sized board ([#209](https://github.com/usestring/gate-inbox/issues/209)) ([a4dd15f](https://github.com/usestring/gate-inbox/commit/a4dd15fb35639c3ae3e694b7771b433eafa01542))
* **ui:** rank New Session prompt suggestions with JEV ([#227](https://github.com/usestring/gate-inbox/issues/227)) ([d09b9c9](https://github.com/usestring/gate-inbox/commit/d09b9c981fb1f9d86de402d7a3938a9f7ccab13f))
* **ui:** show session context tokens beside resource gauges ([#195](https://github.com/usestring/gate-inbox/issues/195)) ([b337e30](https://github.com/usestring/gate-inbox/commit/b337e30199bb1aca5dd1ae806e488230d7520983))


### Bug Fixes

* Automatically name untitled adopted panes ([#223](https://github.com/usestring/gate-inbox/issues/223)) ([8fe8c6a](https://github.com/usestring/gate-inbox/commit/8fe8c6ad996b71f8f9217fb628572a9e5122d055))
* keep children with background work running out of the auto-archive ([#218](https://github.com/usestring/gate-inbox/issues/218)) ([8884752](https://github.com/usestring/gate-inbox/commit/8884752b4b284032cc727df1b888362310fc6a2e))
* **mcp:** revive and migrate on the installed manager when the server is stale ([#215](https://github.com/usestring/gate-inbox/issues/215)) ([844971c](https://github.com/usestring/gate-inbox/commit/844971c18259a25e0ba1839e50159acc1a39cf03))
* open a Codex child in the directory it asked for ([#188](https://github.com/usestring/gate-inbox/issues/188)) ([72ef26f](https://github.com/usestring/gate-inbox/commit/72ef26fd97d0c6cb22ace117911b734ec1f92a33))
* **snippets:** trim the default shortcut set ([#219](https://github.com/usestring/gate-inbox/issues/219)) ([b709395](https://github.com/usestring/gate-inbox/commit/b7093957a3b6c8b6dfbc67b5498373f6e83c89ea))
* **ui:** keep live input visible in compressed focus [stack 3/3] ([#207](https://github.com/usestring/gate-inbox/issues/207)) ([2e4a5db](https://github.com/usestring/gate-inbox/commit/2e4a5db0dd7db7ec4b02342874ebe5434937d2e2))
* **ui:** keep the mobile layout on one panel when a phone zooms out ([#211](https://github.com/usestring/gate-inbox/issues/211)) ([5ebea04](https://github.com/usestring/gate-inbox/commit/5ebea0495037c525bb67ef46d123ad92be53534a))
* **ui:** keep the working spinner visible while reading older messages ([#225](https://github.com/usestring/gate-inbox/issues/225)) ([3c1ed63](https://github.com/usestring/gate-inbox/commit/3c1ed63758fa23e723f31c6ee54180d150cbe8f1))
* **ui:** make compressed focus an explicit experiment [stack 2/3] ([#206](https://github.com/usestring/gate-inbox/issues/206)) ([42c1273](https://github.com/usestring/gate-inbox/commit/42c12737512fc8566524bb3394466a6fef254044))
* **ui:** mark a child whose CLI exited and tell its spawner ([#189](https://github.com/usestring/gate-inbox/issues/189)) ([95b6887](https://github.com/usestring/gate-inbox/commit/95b6887cbad6ed292d91d392e7b64e6c4a336b66))

## [0.10.1](https://github.com/usestring/gate-inbox/compare/v0.10.0...v0.10.1) (2026-10-02)


### Bug Fixes

* **dialog:** read a question off the screen when the pending call is another dialog's ([#201](https://github.com/usestring/gate-inbox/issues/201)) ([7be1a47](https://github.com/usestring/gate-inbox/commit/7be1a471d0dd59507aab23e25572d577b355fbd2))
* **ui:** show the downstream TUI on focus by default ([#203](https://github.com/usestring/gate-inbox/issues/203)) ([13809b0](https://github.com/usestring/gate-inbox/commit/13809b0c91e74b772ff4e831b68dc775e9794270))

## [0.10.0](https://github.com/usestring/gate-inbox/compare/v0.9.0...v0.10.0) (2026-10-01)


### Features

* **ui:** default a focused session to its conversation ([#191](https://github.com/usestring/gate-inbox/issues/191)) ([a7722cd](https://github.com/usestring/gate-inbox/commit/a7722cddcda71362edc2a60fea5e8aa74b0bb5a1))


### Bug Fixes

* **dialog:** relay a permission prompt's command as it was written ([#197](https://github.com/usestring/gate-inbox/issues/197)) ([1041b6a](https://github.com/usestring/gate-inbox/commit/1041b6a5620192c8c7162765710ca416a2145a22))
* **hooks:** name each build's generated launch files after their content ([#192](https://github.com/usestring/gate-inbox/issues/192)) ([869cf65](https://github.com/usestring/gate-inbox/commit/869cf655e49db016ed5626fb031f9e9d4f844b32))
* **ui:** advance the drain when a hookless pane takes a submission ([#196](https://github.com/usestring/gate-inbox/issues/196)) ([256a052](https://github.com/usestring/gate-inbox/commit/256a0527cbd57e985c88b7a4e6eaa75a001bbbc1))

## [0.9.0](https://github.com/usestring/gate-inbox/compare/v0.8.0...v0.9.0) (2026-10-01)


### Features

* answer every child dialog kind through answer_session (S-145287) ([8651cc6](https://github.com/usestring/gate-inbox/commit/8651cc6c5fbc4e1b51d52d09951ab30714514861))
* clarify priority levels with directional triangles ([#182](https://github.com/usestring/gate-inbox/issues/182)) ([45b6e68](https://github.com/usestring/gate-inbox/commit/45b6e6861a1c237570422db09e98f63faefb06b6))
* **ui:** chain scroll keys into list navigation at pane edges ([b32a9f1](https://github.com/usestring/gate-inbox/commit/b32a9f1f80d7f8ac2fc788122a906d880e74cf5b))
* **ui:** suggest recurring prompts in New Session ([#183](https://github.com/usestring/gate-inbox/issues/183)) ([dd5c265](https://github.com/usestring/gate-inbox/commit/dd5c26523dbe6c21e6fb30a746671b62f3c39aae))
* verify a child's parent and attest relayed approvals (S-145286) ([#174](https://github.com/usestring/gate-inbox/issues/174)) ([1bc1571](https://github.com/usestring/gate-inbox/commit/1bc157133beaec2157265d080b68930d9001af15))


### Bug Fixes

* label / search as fuzzy session search, caret before placeholder ([#185](https://github.com/usestring/gate-inbox/issues/185)) ([cb50659](https://github.com/usestring/gate-inbox/commit/cb50659863fab8c2ac7436586c13953f152d3e18))
* **sessioncmd:** name the option a free-text answer landed on in a multi-question result (S-145331) ([#176](https://github.com/usestring/gate-inbox/issues/176)) ([38146a3](https://github.com/usestring/gate-inbox/commit/38146a36bfdac6e33d915e82a2b22e7111b72870))

## [0.8.0](https://github.com/usestring/gate-inbox/compare/v0.7.0...v0.8.0) (2026-10-01)


### Features

* add experimental JEV suggestions for existing sessions ([#161](https://github.com/usestring/gate-inbox/issues/161)) ([b282645](https://github.com/usestring/gate-inbox/commit/b282645fae55772a59222422c78077f246172d40))
* **ui:** separate the legend from the key map ([#175](https://github.com/usestring/gate-inbox/issues/175)) ([1c5cb4f](https://github.com/usestring/gate-inbox/commit/1c5cb4f12a238ad57e0b48484833672d51d11086))
* **ui:** show snippets in the list footer ([#169](https://github.com/usestring/gate-inbox/issues/169)) ([643db20](https://github.com/usestring/gate-inbox/commit/643db20ac248e8f14d2f4c33cbf1e1663ef96a02))


### Bug Fixes

* set an abandoned draft aside so queued messages reach the session ([#167](https://github.com/usestring/gate-inbox/issues/167)) ([3f3a987](https://github.com/usestring/gate-inbox/commit/3f3a98745ac84d452b06bb57975150b4e4770438))
* **ui:** make Left leave focus in triage instead of advancing ([#166](https://github.com/usestring/gate-inbox/issues/166)) ([b76dc6d](https://github.com/usestring/gate-inbox/commit/b76dc6d7780a2da6da023db62de3d9471a2c576d))

## [0.7.0](https://github.com/usestring/gate-inbox/compare/v0.6.0...v0.7.0) (2026-09-30)


### Features

* **ui:** group conversation messages by speaker [split 2/2] ([#163](https://github.com/usestring/gate-inbox/issues/163)) ([c7857cf](https://github.com/usestring/gate-inbox/commit/c7857cf24c1d14bc67ade18cafb9a0a444e903ca))
* **ui:** order a session's pull requests by what needs a person ([#160](https://github.com/usestring/gate-inbox/issues/160)) ([dbef576](https://github.com/usestring/gate-inbox/commit/dbef576273216d255147f74c6a3992c5df0fcb80))


### Bug Fixes

* display Codex opening prompts in the session rail ([#162](https://github.com/usestring/gate-inbox/issues/162)) ([34c807f](https://github.com/usestring/gate-inbox/commit/34c807f09ce3bda64626b961f54625ca5c9d5b55))
* keep sessions working while background work remains [split 1/2] ([#164](https://github.com/usestring/gate-inbox/issues/164)) ([2cd797f](https://github.com/usestring/gate-inbox/commit/2cd797f705a0bab791428eae659d0731c250850d))
* Require dialog-specific evidence before triage handover ([#156](https://github.com/usestring/gate-inbox/issues/156)) ([d513397](https://github.com/usestring/gate-inbox/commit/d513397da2b670adb16274fece570211943ebd2b))

## [0.6.0](https://github.com/usestring/gate-inbox/compare/v0.5.3...v0.6.0) (2026-09-30)


### Features

* relay every child dialog to its parent in full, and follow up while it stands (S-144508) ([#125](https://github.com/usestring/gate-inbox/issues/125)) ([ff1ae61](https://github.com/usestring/gate-inbox/commit/ff1ae614c985c1620cbb2ec169228835eb1ec104))
* **ui:** make space a hotkey-only snippet menu ([#118](https://github.com/usestring/gate-inbox/issues/118)) ([feb6158](https://github.com/usestring/gate-inbox/commit/feb6158c718b92d8a8ad035119d91d35d301fbdf))

## [0.5.3](https://github.com/usestring/gate-inbox/compare/v0.5.2...v0.5.3) (2026-09-30)


### Bug Fixes

* stop showing each session's opening prompt beneath its list row ([#154](https://github.com/usestring/gate-inbox/issues/154)) ([db6cbe4](https://github.com/usestring/gate-inbox/commit/db6cbe47e79294a7604cae10754bf741b5b25a01))
* **ui:** drop the rail's repeat of the cursor row's name and state ([#150](https://github.com/usestring/gate-inbox/issues/150)) ([ed04ab9](https://github.com/usestring/gate-inbox/commit/ed04ab9a92aa479413eb7c8d2c250d504a6c1464))
* Wait for tmux clients before taking over adopted panes ([#147](https://github.com/usestring/gate-inbox/issues/147)) ([7245c7b](https://github.com/usestring/gate-inbox/commit/7245c7b51800e5fdf2768634abc145ac81eb0d0d))

## [0.5.2](https://github.com/usestring/gate-inbox/compare/v0.5.1...v0.5.2) (2026-09-30)


### Bug Fixes

* write the OpenCode v2 config schema and carry its steering on the MCP server ([#146](https://github.com/usestring/gate-inbox/issues/146)) ([082bd73](https://github.com/usestring/gate-inbox/commit/082bd7369b4bd8bb0495150eb72f2ff6197e3f6e))

## [0.5.1](https://github.com/usestring/gate-inbox/compare/v0.5.0...v0.5.1) (2026-09-30)


### Bug Fixes

* delete an empty group on x instead of archiving it ([#140](https://github.com/usestring/gate-inbox/issues/140)) ([3a06325](https://github.com/usestring/gate-inbox/commit/3a0632559073e6ffd22e0c42b868bdb3fc22b705))
* stop relaying a detached session's lifecycle to its creator (S-144688) ([#123](https://github.com/usestring/gate-inbox/issues/123)) ([894762c](https://github.com/usestring/gate-inbox/commit/894762c571c6a1882c393ef1db2a6d9ae888341d))

## [0.5.0](https://github.com/usestring/gate-inbox/compare/v0.4.0...v0.5.0) (2026-09-29)


### Features

* **ui:** add a quick actions palette that teaches the keys ([#141](https://github.com/usestring/gate-inbox/issues/141)) ([cc2e188](https://github.com/usestring/gate-inbox/commit/cc2e18828bd2fa59ea46f332e2223e158109db35))

## [0.4.0](https://github.com/usestring/gate-inbox/compare/v0.3.0...v0.4.0) (2026-09-29)


### Features

* tell a session when its process tree hogs CPU or memory ([#120](https://github.com/usestring/gate-inbox/issues/120)) ([08b13fe](https://github.com/usestring/gate-inbox/commit/08b13fe81116a373c42997caa9ab7c178bd1056d))

## [0.3.0](https://github.com/usestring/gate-inbox/compare/v0.2.0...v0.3.0) (2026-09-29)


### Features

* open the key map with ctrl+h from the list, focus, and welcome ([#134](https://github.com/usestring/gate-inbox/issues/134)) ([2067713](https://github.com/usestring/gate-inbox/commit/2067713b775f0ad74b75d09b6cb9a1ec4f35a6a5))


### Miscellaneous Chores

* release 0.3.0 ([#137](https://github.com/usestring/gate-inbox/issues/137)) ([5094863](https://github.com/usestring/gate-inbox/commit/5094863f9ba0a2fefeae86e7637bb921348ea392))

## [0.2.0](https://github.com/usestring/gate-inbox/compare/v0.1.9...v0.2.0) (2026-09-29)


### Features

* add quota-paced Auto routing for new sessions ([#107](https://github.com/usestring/gate-inbox/issues/107)) ([f900dbb](https://github.com/usestring/gate-inbox/commit/f900dbbe6cd22bd4c0a153b2776ec2f0a789ef6d))
* nest a child agent's terminal under that child on the board (S-144474) ([#112](https://github.com/usestring/gate-inbox/issues/112)) ([29a7ba4](https://github.com/usestring/gate-inbox/commit/29a7ba43131444aac04fba0d2f556878f560636a))
* steer every managed CLI to Gate Inbox sessions over its own subagents ([#108](https://github.com/usestring/gate-inbox/issues/108)) ([392d9fe](https://github.com/usestring/gate-inbox/commit/392d9fec9b9df32735b42e6b7afb72dcd028ed8a))


### Bug Fixes

* give terminals the same cleanup as child sessions (S-144494) ([#115](https://github.com/usestring/gate-inbox/issues/115)) ([d9ecafe](https://github.com/usestring/gate-inbox/commit/d9ecafecee12a4c1dd895cd9da44dbbc0a0119f0))
* keep the triage drain off a session whose answer is still landing (S-144458) ([#110](https://github.com/usestring/gate-inbox/issues/110)) ([6b121e3](https://github.com/usestring/gate-inbox/commit/6b121e38a6de380428f147374e73f88bd575947b))

## [0.1.9](https://github.com/usestring/gate-inbox/compare/v0.1.8...v0.1.9) (2026-09-28)


### Bug Fixes

* release 0.1.9 through release-please ([#113](https://github.com/usestring/gate-inbox/issues/113)) ([7b0a153](https://github.com/usestring/gate-inbox/commit/7b0a153d4696e9ecc0f278096d197731076dc276))
