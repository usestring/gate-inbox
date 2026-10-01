# Changelog

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
